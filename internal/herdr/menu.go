package herdr

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
)

// VisibleScreenLines is how many trailing lines count as the screen for a
// prompt id and a secret-line check. Menu detection reads the same window,
// so a card's id and the re-check before respond hash the same text.
const VisibleScreenLines = 30

// Agent-safe approve shortcuts used when the pane text reveals no menu.
var (
	ToolOptions     = []string{"yes, single permission", "trust, always allow", "no (tab to edit)"}
	SubagentOptions = []string{"approve all pending", "configure individually", "exit (cancel subagents)"}
	CursorOptions   = []string{"y", "a", "n"} // yes / always / no (labels on client)
	GenericYN       = []string{"y", "n"}
)

// menuItemRE matches a numbered TUI menu item: "❯ 1. Yes" / "  2) No" / "│ ❯ 1. Yes │".
// Leading/trailing box borders (Claude draws its prompt in a frame) and the
// focus marker are optional.
var menuItemRE = regexp.MustCompile(`^[\s│|┃╎┆]*(?:[❯>›»▶●•*\-]\s*)?[[\(]?([1-9])[\]\).:]\s+(\S.*?)[\s│|┃╎┆]*$`)

// borderRE matches leading box-drawing borders.
var borderRE = regexp.MustCompile(`^[│|┃╎┆]+`)

// parenRE matches "(y)es" / "(n)o" / "(a)lways" style shortcuts.
var parenRE = regexp.MustCompile(`\(([a-z0-9])\)\s*[a-z]`)

// ruleRE matches a horizontal rule (optionally inside a box border). Claude's
// AskUserQuestion draws one between its options and the trailing "Chat about
// this" item; it separates, it does not end, the menu.
var ruleRE = regexp.MustCompile(`^[\s│|┃╎┆]*[─━═┄┅┈┉╌╍\-–—_]{3,}[\s│|┃╎┆]*$`)

// isContinuation reports whether line is the wrapped tail of a long menu item.
// A wrapped label is indented under its item; interleaved prose starts at the
// margin. Blank lines never continue a menu.
func isContinuation(line string) bool {
	body := borderRE.ReplaceAllString(line, "")
	return strings.HasPrefix(body, "  ") && len(strings.TrimLeft(body, " ")) > 0
}

// ParseNumberedMenu parses a numbered TUI menu into (payloads, labels).
//
// Claude's permission prompt is a numbered select whose length varies with the
// tool. Reading the real items is the only way to avoid showing buttons the
// agent will not accept.
//
// Requires 1, 2, … in an unbroken block — only wrapped labels may sit between
// items — so numbered prose interleaved with other text is not mistaken for a
// menu. Returns nil when fewer than 2 items are found.
func ParseNumberedMenu(text string) (payloads, labels []string) {
	lines := strings.Split(text, "\n")
	var run []string
	prevLine := -1
	for i, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		m := menuItemRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		num, _ := strconv.Atoi(m[1])
		label := strings.TrimSpace(m[2])

		adjacent := prevLine == -1
		if prevLine != -1 {
			adjacent = true
			for j := prevLine + 1; j < i; j++ {
				between := strings.TrimRight(lines[j], "\r")
				if !isContinuation(between) && !ruleRE.MatchString(between) {
					adjacent = false
					break
				}
			}
		}

		switch {
		case num == len(run)+1 && adjacent:
			run = append(run, label)
			prevLine = i
		case num == 1:
			run = []string{label}
			prevLine = i
		default:
			run = nil
			prevLine = -1
		}
		if len(run) == 5 {
			break
		}
	}
	if len(run) < 2 {
		return nil, nil
	}
	payloads = make([]string, len(run))
	for n := range run {
		payloads[n] = strconv.Itoa(n + 1)
	}
	return payloads, run
}

// DetectOptions infers approve menu options from pane text, falling back to
// agent defaults. A real numbered menu on screen wins over any guess.
func DetectOptions(text, agent string) []string {
	if text == "" {
		return FallbackOptionsForAgent(agent)
	}
	lower := strings.ToLower(text)

	if nums, _ := ParseNumberedMenu(text); len(nums) > 0 {
		return nums
	}
	if strings.Contains(lower, "yes, single permission") {
		return copySlice(ToolOptions)
	}
	if strings.Contains(lower, "approve all pending") {
		return copySlice(SubagentOptions)
	}
	if keys := parenShortcuts(lower); len(keys) > 0 {
		return keys
	}
	// Cursor list UI: arrow + Enter, not y/n
	if strings.Contains(lower, "allow once") ||
		(strings.Contains(lower, "allow always") && strings.Contains(lower, "reject")) {
		return copySlice(CursorOptions)
	}
	if strings.Contains(lower, "allow") &&
		(strings.Contains(lower, "deny") || strings.Contains(lower, "reject") || strings.Contains(lower, "skip")) {
		if strings.Contains(strings.ToLower(strings.TrimSpace(agent)), "cursor") {
			return copySlice(CursorOptions)
		}
		return []string{"y", "n"}
	}
	if strings.Contains(lower, "do you want to run") ||
		(strings.Contains(lower, "run this command") && strings.Contains(lower, "skip")) {
		return copySlice(GenericYN)
	}
	return FallbackOptionsForAgent(agent)
}

// parenShortcuts reads a "(y)es (n)o (a)lways" prompt. A real one lists its
// keys on one line; matches scattered over the screen are prose ("the (v)erbose
// flag"), so only a line with at least two distinct keys counts — the last
// such line, being closest to the prompt.
func parenShortcuts(lower string) []string {
	lines := strings.Split(lower, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		var seen []string
		for _, p := range parenRE.FindAllStringSubmatch(lines[i], -1) {
			if !containsString(seen, p[1]) {
				seen = append(seen, p[1])
			}
		}
		if len(seen) >= 2 {
			if len(seen) > 5 {
				seen = seen[:5]
			}
			return seen
		}
	}
	return nil
}

// DetectMenu returns (options, labels) for a blocked pane. labels mirrors
// options when the on-screen menu gave real wording; it is nil when only
// keystrokes are available, and clients then label the keys themselves.
func DetectMenu(text, agent string) (options, labels []string) {
	opts := DetectOptions(text, agent)
	_, parsedLabels := ParseNumberedMenu(text)
	if len(parsedLabels) > 0 && len(parsedLabels) == len(opts) {
		return opts, parsedLabels
	}
	return opts, nil
}

// FallbackOptionsForAgent picks agent-safe approve shortcuts when pane text
// reveals no menu.
func FallbackOptionsForAgent(agent string) []string {
	a := strings.ToLower(strings.TrimSpace(agent))
	switch a {
	case "claude", "anthropic":
		// Claude's prompt is a numbered select whose items vary per tool; a
		// canned menu is usually wrong. Offer nothing rather than invent buttons.
		return nil
	case "cursor", "cursor-agent", "cursor_agent":
		return copySlice(CursorOptions)
	case "codex", "openai", "kiro", "devin", "copilot", "gemini", "grok", "pi", "opencode", "agy":
		return copySlice(GenericYN)
	}
	return copySlice(GenericYN)
}

func copySlice(s []string) []string {
	out := make([]string, len(s))
	copy(out, s)
	return out
}

func containsString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// claudeWorkLineRE is Claude Code's working line, whole: one spinner frame,
// the action ending in an ellipsis, and in parentheses the elapsed time, a
// middle dot, and the token count. Optional interrupt hint stays in the id.
// Shape matches herdr-web-ui steadyReader (WORKING_LINE_RE):
// "✢ Tempering… (1m 55s · ↓ 10.0k tokens)".
var claudeWorkLineRE = regexp.MustCompile(`^[·✢✳✶✻✽*] (\S(?:.*\S)?…) \((?:\d+h )?(?:\d+m )?\d+s · [↑↓] [\d.,]+[kKmM]? tokens( · esc to interrupt)?\)$`)

// PromptID is the sha256 hex of the visible screen. For a claude pane with
// exactly one working line, that line's elapsed time and token count are
// replaced with fixed placeholders and the line number is part of the hash.
// Zero matches, more than one match, or any other agent: the screen is hashed
// as read, with no replacement.
func PromptID(screen, agent string) string {
	text := normalizeNewlines(StripANSI(screen))
	lines := strings.Split(text, "\n")
	work := -1
	if strings.EqualFold(strings.TrimSpace(agent), "claude") {
		var hits []int
		for i, line := range lines {
			if claudeWorkLineRE.MatchString(cleanScreenLine(line)) {
				hits = append(hits, i)
			}
		}
		if len(hits) == 1 {
			work = hits[0]
		}
	}
	payload := text
	if work >= 0 {
		lines[work] = claudeWorkLineRE.ReplaceAllString(cleanScreenLine(lines[work]), "* ${1} (<time> · <tokens>${2})")
		payload = strings.Join(lines, "\n") + "\n#workline:" + strconv.Itoa(work)
	}
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

func normalizeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// cleanScreenLine trims one leading and one trailing box border so a working
// line drawn inside Claude's frame still matches.
func cleanScreenLine(line string) string {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "│") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "│"))
	}
	if strings.HasSuffix(line, "│") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "│"))
	}
	return strings.TrimSpace(line)
}

// NormalizeTail collapses whitespace after ANSI stripping. The client and the
// relay both compare secret prompts in this form.
func NormalizeTail(line string) string {
	return strings.Join(strings.Fields(StripANSI(line)), " ")
}

// LastNonEmptyLine returns the normalized last non-empty line of a screen.
func LastNonEmptyLine(screen string) string {
	lines := strings.Split(normalizeNewlines(StripANSI(screen)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return NormalizeTail(lines[i])
		}
	}
	return ""
}

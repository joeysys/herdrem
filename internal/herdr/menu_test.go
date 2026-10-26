package herdr

import (
	"reflect"
	"testing"
)

func TestParseNumberedMenu_ClaudeConfirm(t *testing.T) {
	text := `Claude wants to edit foo.go

❯ 1. Yes
  2. Yes, and don't ask again
  3. No`

	payloads, labels := ParseNumberedMenu(text)
	wantP := []string{"1", "2", "3"}
	wantL := []string{"Yes", "Yes, and don't ask again", "No"}
	if !reflect.DeepEqual(payloads, wantP) {
		t.Errorf("payloads = %v, want %v", payloads, wantP)
	}
	if !reflect.DeepEqual(labels, wantL) {
		t.Errorf("labels = %v, want %v", labels, wantL)
	}
}

func TestParseNumberedMenu_BoxBorders(t *testing.T) {
	text := `│ ❯ 1. Yes           │
│   2. No            │`

	payloads, labels := ParseNumberedMenu(text)
	if !reflect.DeepEqual(payloads, []string{"1", "2"}) {
		t.Errorf("payloads = %v", payloads)
	}
	if !reflect.DeepEqual(labels, []string{"Yes", "No"}) {
		t.Errorf("labels = %v", labels)
	}
}

func TestParseNumberedMenu_WrappedLabel(t *testing.T) {
	// A long option wraps; the wrapped tail is indented and must not break the run.
	text := `  1. Yes, and allow tmp/ access
      and similar commands
  2. No`

	payloads, labels := ParseNumberedMenu(text)
	if !reflect.DeepEqual(payloads, []string{"1", "2"}) {
		t.Fatalf("payloads = %v, want [1 2]", payloads)
	}
	if labels[0] != "Yes, and allow tmp/ access" {
		t.Errorf("labels[0] = %q", labels[0])
	}
}

func TestParseNumberedMenu_NumberedProseRejected(t *testing.T) {
	// Numbered prose interleaved with margin prose is NOT a menu.
	text := `I found 3 issues:
1. First thing
Some explanation at the margin.
2. Second thing
More prose.
3. Third thing`

	payloads, _ := ParseNumberedMenu(text)
	if len(payloads) != 0 {
		t.Errorf("expected no menu from prose, got %v", payloads)
	}
}

func TestParseNumberedMenu_NotContiguousRejected(t *testing.T) {
	// Starts at 2, no 1 before it → reset; never reaches 2 contiguous from 1.
	text := `  2. No
  3. Maybe`
	payloads, _ := ParseNumberedMenu(text)
	if len(payloads) != 0 {
		t.Errorf("expected no menu, got %v", payloads)
	}
}

func TestParseNumberedMenu_SingleItemRejected(t *testing.T) {
	text := `  1. Yes`
	payloads, _ := ParseNumberedMenu(text)
	if len(payloads) != 0 {
		t.Errorf("expected < 2 items rejected, got %v", payloads)
	}
}

func TestDetectOptions_NumberedMenuWins(t *testing.T) {
	text := `❯ 1. Yes
  2. No`
	opts := DetectOptions(text, "claude")
	if !reflect.DeepEqual(opts, []string{"1", "2"}) {
		t.Errorf("opts = %v, want [1 2]", opts)
	}
}

func TestDetectOptions_ParenShortcuts(t *testing.T) {
	text := `Do you want to proceed? (y)es (n)o (a)lways`
	opts := DetectOptions(text, "")
	if !reflect.DeepEqual(opts, []string{"y", "n", "a"}) {
		t.Errorf("opts = %v", opts)
	}
}

func TestDetectOptions_CursorAllowReject(t *testing.T) {
	text := `Allow once  ·  Allow always  ·  Reject`
	opts := DetectOptions(text, "cursor")
	if !reflect.DeepEqual(opts, CursorOptions) {
		t.Errorf("opts = %v, want %v", opts, CursorOptions)
	}
}

func TestDetectMenu_LabelsMatch(t *testing.T) {
	text := `❯ 1. Yes
  2. No`
	opts, labels := DetectMenu(text, "claude")
	if !reflect.DeepEqual(opts, []string{"1", "2"}) || !reflect.DeepEqual(labels, []string{"Yes", "No"}) {
		t.Errorf("opts=%v labels=%v", opts, labels)
	}
}

func TestDetectMenu_KeystrokesOnlyNoLabels(t *testing.T) {
	// No numbered menu → options are keystrokes, labels must be nil.
	text := `Allow / Deny?`
	opts, labels := DetectMenu(text, "")
	if len(opts) == 0 {
		t.Fatalf("expected keystroke options")
	}
	if labels != nil {
		t.Errorf("labels should be nil for keystroke-only, got %v", labels)
	}
}

func TestFallbackOptionsForAgent(t *testing.T) {
	cases := map[string][]string{
		"claude": nil,
		"cursor": CursorOptions,
		"codex":  GenericYN,
		"kiro":   GenericYN,
		"":       GenericYN,
	}
	for agent, want := range cases {
		got := FallbackOptionsForAgent(agent)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("FallbackOptionsForAgent(%q) = %v, want %v", agent, got, want)
		}
	}
}

func TestFallbackOptionsForAgent_ReturnsCopy(t *testing.T) {
	a := FallbackOptionsForAgent("codex")
	a[0] = "MUTATED"
	if GenericYN[0] == "MUTATED" {
		t.Error("fallback returned shared backing array, mutation leaked")
	}
}

// Claude's AskUserQuestion menu: options with indented descriptions, then a
// rule before the last item. The rule used to break the run, the menu was
// dropped, and the (x)es fallback picked letters out of prose above it.
const askUserQuestionScreen = `Some earlier output mentioning (e)dge cases,
a (0)-based index and the (v)erbose flag on separate lines.

 ☐ Conflict policy

In a normal browser tab, how should Ctrl/⌘ shortcuts that conflict with the browser be handled?

❯ 1. Restore only Ctrl+R (Recommended)
     Give Ctrl/⌘+R back to the browser for refresh; use the ⏱ button for input history.
  2. Restore both Ctrl+R and Ctrl+F
     Return both keys to the browser (refresh / find in page).
  3. Remap Ctrl+R to a conflict-free key
     Restore refresh in the tab; use Alt+R for input history.
  4. Type something.
──────────────────────────────────────────────────────────────────
  5. Chat about this

Enter to select · ↑/↓ to navigate · Esc to cancel`

func TestDetectMenu_AskUserQuestionWithRule(t *testing.T) {
	opts, labels := DetectMenu(askUserQuestionScreen, "claude")
	if !reflect.DeepEqual(opts, []string{"1", "2", "3", "4", "5"}) {
		t.Fatalf("opts = %v, want 1..5", opts)
	}
	want := []string{"Restore only Ctrl+R (Recommended)", "Restore both Ctrl+R and Ctrl+F", "Remap Ctrl+R to a conflict-free key", "Type something.", "Chat about this"}
	if !reflect.DeepEqual(labels, want) {
		t.Errorf("labels = %q", labels)
	}
}

func TestDetectOptions_ParenShortcutsMustShareALine(t *testing.T) {
	// one shortcut per line is prose, not a (y)es/(n)o prompt
	text := "Some earlier output mentioning (e)dge cases,\na (0)-based index and the (v)erbose flag.\nWaiting…"
	if opts := DetectOptions(text, "claude"); len(opts) != 0 {
		t.Errorf("opts = %v, want none for claude", opts)
	}
	// the prompt line closest to the bottom wins
	text = "earlier: (a)bc (b)cd\nProceed? (y)es (n)o"
	if opts := DetectOptions(text, ""); !reflect.DeepEqual(opts, []string{"y", "n"}) {
		t.Errorf("opts = %v, want [y n]", opts)
	}
}

func TestPromptID_ClaudeWorkLineIsStable(t *testing.T) {
	base := "Allow this edit?\n1. Yes\n2. No\n"
	a := base + "✢ Tempering… (1s · ↓ 10 tokens)\n"
	b := base + "✶ Tempering… (1m 55s · ↑ 10.0k tokens)\n"
	if PromptID(a, "claude") != PromptID(b, "claude") {
		t.Fatal("timer and token changes on the one working line must keep the id")
	}
	// the action words stay in the id
	c := base + "✢ Deleting… (1s · ↓ 10 tokens)\n"
	if PromptID(a, "claude") == PromptID(c, "claude") {
		t.Fatal("a different action must change the id")
	}
	// a different menu changes the id
	d := "Allow something else?\n1. Yes\n2. No\n✢ Tempering… (1s · ↓ 10 tokens)\n"
	if PromptID(a, "claude") == PromptID(d, "claude") {
		t.Fatal("a different screen must change the id")
	}
}

func TestPromptID_OnlyExactlyOneClaudeWorkLineIsNormalized(t *testing.T) {
	line := "✢ Tempering… (1s · ↓ 10 tokens)"
	later := "✶ Tempering… (9s · ↑ 2k tokens)"
	one := "menu\n" + line + "\n"
	two := one + later + "\n"
	if PromptID(one, "claude") != PromptID("menu\n"+later+"\n", "claude") {
		t.Fatal("one working line is normalized, so the spinner frame and the timer must not change the id")
	}
	rawA := PromptID(two, "claude")
	rawB := PromptID("menu\n"+line+"\n"+"✳ Tempering… (3s · ↓ 4 tokens)\n", "claude")
	if rawA == rawB {
		t.Fatal("two working lines are hashed as-is, so a timer change must change the id")
	}
	// other agents never normalize
	if PromptID(one, "codex") == PromptID("menu\n"+later+"\n", "codex") {
		t.Fatal("non-claude screens are hashed as read")
	}
	// the line number is part of the hash, so a screen that already shows the
	// placeholder text is not the same card as the normalized working line
	normalized := "menu\n* Tempering… (<time> · <tokens>)\n"
	if PromptID(one, "claude") == PromptID(normalized, "claude") {
		t.Fatal("work line index must distinguish a real working line from placeholder text")
	}
}

func TestPromptID_BorderAndInterruptHint(t *testing.T) {
	plain := "✢ Tempering… (1s · ↓ 10 tokens · esc to interrupt)"
	boxed := "│ ✢ Tempering… (4s · ↑ 20 tokens · esc to interrupt) │"
	if PromptID(plain, "Claude") != PromptID(boxed, "claude") {
		t.Fatal("one box border and case of the agent name must not change the id")
	}
	noHint := "✢ Tempering… (1s · ↓ 10 tokens)"
	if PromptID(plain, "claude") == PromptID(noHint, "claude") {
		t.Fatal("the interrupt hint is part of the line and must change the id")
	}
}

func TestLastNonEmptyLine(t *testing.T) {
	screen := "hello\n\n  [sudo] password for testuser:  \n\n"
	if got := LastNonEmptyLine(screen); got != "[sudo] password for testuser:" {
		t.Fatalf("tail = %q", got)
	}
	if NormalizeTail("  Password:  ") != LastNonEmptyLine("Password:") {
		t.Fatal("client and relay tails must compare equal after normalize")
	}
}

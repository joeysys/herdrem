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

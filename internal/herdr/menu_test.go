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

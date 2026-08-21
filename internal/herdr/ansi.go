package herdr

import "regexp"

// ansiRE matches ANSI escape sequences (CSI, OSC, and single-char escapes).
// Used relay-side to strip ANSI before menu parsing; clients receive raw ANSI.
var ansiRE = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\)|.)`)

// StripANSI removes ANSI escape sequences from text.
func StripANSI(text string) string {
	return ansiRE.ReplaceAllString(text, "")
}

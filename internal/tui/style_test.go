package tui

import "testing"

func TestCleanTakesOutWhatWouldBreakTheScreenAndKeepsTheRest(t *testing.T) {
	for in, want := range map[string]string{
		"plain line":             "plain line",
		"naïve café 日本語 🎉":       "naïve café 日本語 🎉",
		"\x1b[31mred\x1b[0m":     "red",
		"a\tb":                   "a    b",
		"bell\x07 and\r\nreturn": "bell andreturn",
		"bad \xff byte":          "bad  byte",
		"c1\u0085control":        "c1control",
	} {
		if got := clean(in); got != want {
			t.Errorf("clean(%q) = %q, want %q", in, got, want)
		}
	}
}

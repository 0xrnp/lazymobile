package changelog

import (
	"strings"
	"testing"
)

const sample = `# Changelog

Some words before the first version.

## Unreleased

- Not out yet

## 0.5.0 - 2026-11-25

### Run
- Run an app

## 0.4.0 - 2026-11-17

- Four

## 0.3.0

- Three
`

func TestParseReadsEachVersionNewestFirst(t *testing.T) {
	entries := Parse(sample)
	var got []string
	for _, e := range entries {
		got = append(got, e.Version+"|"+e.Date)
	}
	if want := "Unreleased|,0.5.0|2026-11-25,0.4.0|2026-11-17,0.3.0|"; strings.Join(got, ",") != want {
		t.Fatalf("got %v, want %s", got, want)
	}
	if entries[1].Notes != "### Run\n- Run an app" || entries[0].Notes != "- Not out yet" {
		t.Errorf("notes: %q and %q", entries[0].Notes, entries[1].Notes)
	}
	if len(Parse("no headings here")) != 0 {
		t.Error("text without a version heading made an entry")
	}
}

func TestReleasedTellsReleasesFromBuildsInBetween(t *testing.T) {
	for version, want := range map[string]bool{
		"0.4.0": true, "v0.4.0": true, "v10.2.13": true,
		"dev": false, "": false, "v0.4.1-0.20261119073222-07cac82c18a4+dirty": false, "0.4": false, "(devel)": false,
	} {
		if Released(version) != want {
			t.Errorf("Released(%q) = %v", version, !want)
		}
	}
}

func TestCompareOrdersByNumberNotByText(t *testing.T) {
	for _, tt := range []struct {
		a, b string
		want int
	}{
		{"0.4.0", "0.5.0", -1}, {"v0.10.0", "0.9.0", 1}, {"0.4.0", "v0.4.0", 0}, {"1.0.0", "0.99.99", 1},
		{"dev", "0.1.0", -1}, {"", "", 0},
	} {
		if got := Compare(tt.a, tt.b); got != tt.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestBetweenIsWhatAnUpgradeBrought(t *testing.T) {
	entries := Parse(sample)
	names := func(from, to string) string {
		var out []string
		for _, e := range Between(entries, from, to) {
			out = append(out, e.Version)
		}
		return strings.Join(out, ",")
	}
	for _, tt := range []struct{ from, to, want string }{
		{"0.4.0", "0.5.0", "0.5.0"},
		{"0.3.0", "0.5.0", "0.5.0,0.4.0"},
		{"0.5.0", "0.5.0", ""},
		{"0.5.0", "0.4.0", ""},
		{"", "0.5.0", ""},
		{"0.4.0", "dev", ""},
		{"v0.3.0", "v0.4.0", "0.4.0"},
	} {
		if got := names(tt.from, tt.to); got != tt.want {
			t.Errorf("Between(%q, %q) = %q, want %q", tt.from, tt.to, got, tt.want)
		}
	}
}

func TestTextForATerminal(t *testing.T) {
	entry, ok := Find(Parse(sample), "v0.5.0")
	if !ok {
		t.Fatal("not found")
	}
	if got, want := entry.Text(), "0.5.0 (2026-11-25)\n\nRun\n- Run an app"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if _, ok := Find(Parse(sample), "9.9.9"); ok {
		t.Error("an unknown version was found")
	}
	if e, ok := Find(Parse(sample), "unreleased"); !ok || e.Version != Unreleased {
		t.Errorf("Unreleased was not found: %v %v", e, ok)
	}
}

func TestTextJoinsWrappedBullets(t *testing.T) {
	e := Entry{Version: "0.5.0", Notes: "### Run\n- one bullet that was\n  wrapped over\n  three lines\n- another\n\n### Other\n- last"}
	want := "0.5.0\n\nRun\n- one bullet that was wrapped over three lines\n- another\n\nOther\n- last"
	if got := e.Text(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

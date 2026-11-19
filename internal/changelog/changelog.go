// Package changelog reads CHANGELOG.md, which is built into the program: one "## <version> - <date>"
// heading per release, newest first, and "## Unreleased" for what is not out yet.
package changelog

import (
	"regexp"
	"strconv"
	"strings"
)

// Unreleased is the version name of the notes of what is not out yet.
const Unreleased = "Unreleased"

// Entry is the notes of one version.
type Entry struct {
	// Version is "0.4.0", or Unreleased.
	Version string `json:"version"`
	Date    string `json:"date,omitempty"`
	// Notes is everything under the heading, without the blank lines around it.
	Notes string `json:"notes"`
}

var heading = regexp.MustCompile(`^## (Unreleased|\d+\.\d+\.\d+)(?: - (\d{4}-\d\d-\d\d))?\s*$`)

// Parse reads the entries of text, newest first as the file lists them. Text before the first
// heading, such as the title, is left out.
func Parse(text string) []Entry {
	var entries []Entry
	var body []string
	flush := func() {
		if len(entries) > 0 {
			entries[len(entries)-1].Notes = strings.TrimSpace(strings.Join(body, "\n"))
		}
		body = nil
	}
	for _, line := range strings.Split(text, "\n") {
		if m := heading.FindStringSubmatch(line); m != nil {
			flush()
			entries = append(entries, Entry{Version: m[1], Date: m[2]})
			continue
		}
		if len(entries) > 0 {
			body = append(body, line)
		}
	}
	flush()
	return entries
}

// Normalize is a version without a leading "v", as the release build and `go install` differ.
func Normalize(version string) string {
	return strings.TrimPrefix(strings.TrimSpace(version), "v")
}

var released = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// Released reports whether version is a release such as "0.4.0" or "v0.4.0", and not a build in
// between releases such as "v0.4.1-0.20261119-abc+dirty" or "dev".
func Released(version string) bool {
	return released.MatchString(Normalize(version))
}

// Compare returns -1, 0 or 1 as release version a is older than, the same as, or newer than b. A
// version that is not a release counts as older than every release.
func Compare(a, b string) int {
	pa, pb := parts(a), parts(b)
	for i := range pa {
		switch {
		case pa[i] < pb[i]:
			return -1
		case pa[i] > pb[i]:
			return 1
		}
	}
	return 0
}

func parts(version string) [3]int {
	var out [3]int
	if !Released(version) {
		return out
	}
	for i, s := range strings.Split(Normalize(version), ".") {
		out[i], _ = strconv.Atoi(s)
	}
	return out
}

// Between returns the releases after from, up to and including to, newest first. Unreleased is
// never in it. An empty from means no release was seen before, so nothing is between.
func Between(entries []Entry, from, to string) []Entry {
	if !Released(from) || !Released(to) {
		return nil
	}
	var out []Entry
	for _, e := range entries {
		if e.Version != Unreleased && Compare(e.Version, from) > 0 && Compare(e.Version, to) <= 0 {
			out = append(out, e)
		}
	}
	return out
}

// Find returns the entry of a version, such as "0.4.0" or "v0.4.0", or of Unreleased.
func Find(entries []Entry, version string) (Entry, bool) {
	version = Normalize(version)
	for _, e := range entries {
		if strings.EqualFold(e.Version, version) {
			return e, true
		}
	}
	return Entry{}, false
}

// Title is "0.4.0 (2026-11-17)", or just the version when there is no date.
func (e Entry) Title() string {
	if e.Date == "" {
		return e.Version
	}
	return e.Version + " (" + e.Date + ")"
}

// Text is the entry for a terminal: its title, then its notes with the "###" marks taken off the
// section names, and a bullet that was wrapped over several lines in the file joined into one, so
// a window can wrap it to its own width.
func (e Entry) Text() string {
	lines := []string{}
	for _, line := range strings.Split(e.Notes, "\n") {
		last := len(lines) - 1
		if strings.HasPrefix(line, "  ") && last >= 0 && lines[last] != "" {
			lines[last] += " " + strings.TrimSpace(line)
			continue
		}
		lines = append(lines, strings.TrimPrefix(line, "### "))
	}
	return e.Title() + "\n\n" + strings.Join(lines, "\n")
}

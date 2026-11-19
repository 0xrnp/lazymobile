package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

const changelogSample = `# Changelog

## Unreleased

- Next thing

## 0.4.0 - 2026-11-17

### Apps
- Four

## 0.3.0 - 2026-11-15

- Three
`

func TestChangelogPrintsTheRunningVersionsNotes(t *testing.T) {
	e := newEnv(t)
	e.app.Changelog = changelogSample
	e.app.Version = "0.4.0"
	if code := e.run("changelog"); code != ExitOK || e.out.String() != "0.4.0 (2026-11-17)\n\nApps\n- Four\n" {
		t.Errorf("code %d, stdout %q, stderr %q", code, e.out.String(), e.errs.String())
	}
	// A build in between releases has no notes of its own, so the newest are shown.
	e.app.Version = "v0.4.1-0.20261119-abc+dirty"
	if code := e.run("changelog"); code != ExitOK || !strings.HasPrefix(e.out.String(), "Unreleased\n\n- Next thing") {
		t.Errorf("code %d, stdout %q", code, e.out.String())
	}
}

func TestChangelogCanPrintOneVersionOrAll(t *testing.T) {
	e := newEnv(t)
	e.app.Changelog = changelogSample
	if code := e.run("changelog", "v0.3.0"); code != ExitOK || e.out.String() != "0.3.0 (2026-11-15)\n\n- Three\n" {
		t.Errorf("one: code %d, stdout %q", code, e.out.String())
	}
	if code := e.run("changelog", "--all"); code != ExitOK || strings.Count(e.out.String(), "\n\n0.") != 2 || !strings.Contains(e.out.String(), "Unreleased") {
		t.Errorf("all: code %d, stdout %q", code, e.out.String())
	}
	if code := e.run("changelog", "--all", "--json"); code != ExitOK {
		t.Fatalf("json: code %d", code)
	}
	var got struct {
		Entries []struct{ Version, Date, Notes string }
	}
	if err := json.Unmarshal(e.out.Bytes(), &got); err != nil || len(got.Entries) != 3 || got.Entries[1].Version != "0.4.0" || got.Entries[1].Date != "2026-11-17" {
		t.Errorf("json: %v, %+v", err, got)
	}
}

func TestChangelogFailuresAndUsage(t *testing.T) {
	e := newEnv(t)
	e.app.Changelog = changelogSample
	if code := e.run("changelog", "9.9.9"); code != ExitFailure || !strings.Contains(e.errs.String(), "No notes for version 9.9.9") {
		t.Errorf("unknown version: code %d, stderr %q", code, e.errs.String())
	}
	for _, args := range [][]string{{"changelog", "0.4.0", "0.3.0"}, {"changelog", "0.4.0", "--all"}, {"changelog", "--nope"}} {
		if code := e.run(args...); code != ExitUsage {
			t.Errorf("%v: code %d, want %d", args, code, ExitUsage)
		}
	}
	e.app.Changelog = ""
	if code := e.run("changelog"); code != ExitFailure {
		t.Errorf("no changelog: code %d", code)
	}
}

package tui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0xrnp/lazymobile/internal/state"
)

const dashboardChangelog = `# Changelog

## 0.6.0 - 2026-12-01

- Six

## 0.5.0 - 2026-11-25

### Run
- Run an app

## 0.4.0 - 2026-11-17

- Four

## 0.3.0 - 2026-11-15

- Three

## 0.2.0 - 2026-11-14

- Two
`

// startedWith starts the dashboard with this version, after seen was the last one started.
func startedWith(t *testing.T, store state.Store, version string) *harness {
	t.Helper()
	h := startWith(t, Memory{Store: store, Version: version, Changelog: dashboardChangelog}, iphone)
	h.m.whatsNew()
	return h
}

func seenStore(t *testing.T, seen string) state.Store {
	t.Helper()
	store := state.Store{Path: filepath.Join(t.TempDir(), "state.json")}
	if seen != "" {
		if err := store.Update(func(f *state.File) { f.SeenVersion = seen }); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

func TestWhatIsNewIsShownOnceAfterAnUpgrade(t *testing.T) {
	store := seenStore(t, "0.4.0")
	h := startedWith(t, store, "0.5.0")
	h.wantScreen("What's new in lazymobile 0.5.0", "0.5.0 (2026-11-25)", "Run an app")
	h.wantNoScreen("Four")
	if got := store.Load().SeenVersion; got != "0.5.0" {
		t.Errorf("seen version is %q", got)
	}
	// The next start has nothing new.
	h = startedWith(t, store, "0.5.0")
	h.wantNoScreen("What's new")
}

func TestWhatIsNewCoversEveryVersionSkippedButNotMoreThanThree(t *testing.T) {
	h := startedWith(t, seenStore(t, "0.3.0"), "0.5.0")
	h.wantScreen("0.5.0 (2026-11-25)", "0.4.0 (2026-11-17)")
	h = startedWith(t, seenStore(t, "0.1.0"), "0.6.0")
	h.wantScreen("0.6.0 (2026-12-01)", "0.5.0 (2026-11-25)", "0.4.0 (2026-11-17)", "lazymobile changelog --all")
	h.wantNoScreen("0.3.0 (2026-11-15)")
}

func TestNothingIsShownOnAFirstStartABuildInBetweenOrAnOlderVersion(t *testing.T) {
	store := seenStore(t, "")
	h := startedWith(t, store, "0.5.0")
	h.wantNoScreen("What's new")
	if got := store.Load().SeenVersion; got != "0.5.0" {
		t.Errorf("a first start did not record the version: %q", got)
	}

	for name, version := range map[string]string{"build in between": "v0.5.1-0.20261201-abc+dirty", "dev": "dev", "older": "0.4.0"} {
		store := seenStore(t, "0.5.0")
		startedWith(t, store, version).wantNoScreen("What's new")
		if got := store.Load().SeenVersion; got != "0.5.0" {
			t.Errorf("%s changed the seen version to %q", name, got)
		}
	}
}

func updateHarness(t *testing.T, version string, latest func(context.Context) (string, error)) *harness {
	t.Helper()
	return startWith(t, Memory{Version: version, LatestVersion: latest}, iphone)
}

func TestANewerReleaseIsSaidInANote(t *testing.T) {
	h := updateHarness(t, "0.4.0", func(context.Context) (string, error) { return "0.5.0", nil })
	h.run(h.m.checkUpdate())
	h.wantScreen("lazymobile 0.5.0 is out, you have 0.4.0", "brew upgrade lazymobile")
}

func TestNothingIsSaidWhenThereIsNothingNewOrNothingKnown(t *testing.T) {
	for name, tt := range map[string]struct {
		version string
		latest  func(context.Context) (string, error)
	}{
		"same version":     {"0.5.0", func(context.Context) (string, error) { return "0.5.0", nil }},
		"older release":    {"0.5.0", func(context.Context) (string, error) { return "0.4.0", nil }},
		"check is off":     {"0.4.0", func(context.Context) (string, error) { return "", nil }},
		"check failed":     {"0.4.0", func(context.Context) (string, error) { return "0.5.0", errors.New("no network") }},
		"not a version":    {"0.4.0", func(context.Context) (string, error) { return "latest", nil }},
		"build in between": {"v0.4.1-0.20261119-abc+dirty", func(context.Context) (string, error) { t.Error("a build in between looked"); return "0.5.0", nil }},
		"no check wired":   {"0.4.0", nil},
	} {
		h := updateHarness(t, tt.version, tt.latest)
		h.run(h.m.checkUpdate())
		if strings.Contains(h.screen(), " is out, you have") {
			t.Errorf("%s: an update was announced", name)
		}
	}
}

package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/eas"
)

type fakeBuilds struct {
	mu       sync.Mutex
	calls    []string
	list     []eas.Build
	listErr  error
	profiles []string
	fail     map[string]error
}

func (f *fakeBuilds) record(format string, args ...any) error {
	call := fmt.Sprintf(format, args...)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
	for prefix, err := range f.fail {
		if strings.HasPrefix(call, prefix) {
			return err
		}
	}
	return nil
}

func (f *fakeBuilds) called(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, call := range f.calls {
		if strings.HasPrefix(call, prefix) {
			n++
		}
	}
	return n
}

func (f *fakeBuilds) Name() string       { return "resident" }
func (f *fakeBuilds) Profiles() []string { return f.profiles }
func (f *fakeBuilds) StartCommand(profile, platform string) []string {
	return []string{"eas", "build", "--profile", profile, "--platform", platform, "--non-interactive", "--no-wait"}
}
func (f *fakeBuilds) List(context.Context, int) ([]eas.Build, error) {
	_ = f.record("list")
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]eas.Build(nil), f.list...), f.listErr
}
func (f *fakeBuilds) Download(_ context.Context, id string) (string, error) {
	return "/cache/" + id + ".apk", f.record("download %s", id)
}
func (f *fakeBuilds) Cancel(_ context.Context, id string) error { return f.record("cancel %s", id) }
func (f *fakeBuilds) Start(_ context.Context, profile, platform string) error {
	return f.record("start %s %s", profile, platform)
}

var (
	androidBuild = eas.Build{ID: "aaaaaaaa-1111", Status: eas.StatusFinished, Platform: "android", Profile: "development",
		Distribution: "INTERNAL", HasArchive: true, AppVersion: "1.4.0", CommitMessage: "Fix blank feed",
		URL: "https://expo.dev/accounts/sample-org/projects/sample-app/builds/aaaaaaaa-1111"}
	runningBuild = eas.Build{ID: "bbbbbbbb-2222", Status: eas.StatusInProgress, Platform: "ios", Profile: "preview", Simulator: true}
	failedBuild  = eas.Build{ID: "cccccccc-3333", Status: eas.StatusErrored, Platform: "ios", Profile: "production", Error: "Provisioning profile has expired"}
	deviceBuild  = eas.Build{ID: "dddddddd-4444", Status: eas.StatusFinished, Platform: "ios", Profile: "preview", HasArchive: true}
)

// startWithBuilds opens the dashboard inside an Expo project.
func startWithBuilds(t *testing.T, builds *fakeBuilds, devices ...device.Device) *harness {
	t.Helper()
	h := start(t, devices...)
	for i := range builds.list {
		if builds.list[i].CreatedAt.IsZero() {
			builds.list[i].CreatedAt = h.clock.Add(-time.Duration(i+1) * time.Hour)
		}
	}
	if builds.fail == nil {
		builds.fail = map[string]error{}
	}
	h.m.builds = builds
	return h
}

func (h *harness) selectBuild(id string) {
	h.t.Helper()
	for i, b := range h.m.build.list {
		if b.ID == id {
			h.m.build.cursor = i
			return
		}
	}
	h.t.Fatalf("build %s is not in the list", id)
}

// Opening the dashboard must not run eas: it runs the project's code and uses the Expo login.
func TestBuildsAreNotLoadedUntilAsked(t *testing.T) {
	builds := &fakeBuilds{list: []eas.Build{androidBuild}}
	h := startWithBuilds(t, builds, pixel)
	h.clock = h.clock.Add(time.Hour)
	_, cmd := h.m.Update(tickMsg{})
	h.run(cmd)
	if builds.called("list") != 0 {
		t.Error("eas ran without being asked")
	}
	h.wantScreen("Builds: resident", "Press enter to load the builds")

	h.press("tab", "enter")
	if builds.called("list") != 1 {
		t.Errorf("list calls = %d", builds.called("list"))
	}
	// Created an hour before the start, and an hour has passed since.
	h.wantScreen("android", "development", "finished", "2h")
}

func TestBuildsPaneKeepsTheScreenExact(t *testing.T) {
	builds := &fakeBuilds{list: []eas.Build{androidBuild, runningBuild, failedBuild}}
	h := startWithBuilds(t, builds, pixel, iphone)
	h.press("tab", "enter")
	for _, size := range [][2]int{{120, 30}, {100, 12}, {80, 24}, {44, 10}} {
		h.m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for pane := 0; pane < 3; pane++ {
			lines := strings.Split(h.screen(), "\n")
			if len(lines) != size[1] {
				t.Errorf("%dx%d: %d lines", size[0], size[1], len(lines))
			}
			for i, line := range lines {
				if w := ansi.StringWidth(line); w != size[0] {
					t.Errorf("%dx%d: line %d is %d wide", size[0], size[1], i, w)
				}
			}
			h.press("tab")
		}
	}
}

func TestBuildsRefreshMoreOftenWhileOneIsRunning(t *testing.T) {
	builds := &fakeBuilds{list: []eas.Build{androidBuild}}
	h := startWithBuilds(t, builds)
	h.press("tab", "enter")
	tick := func(after time.Duration) {
		h.clock = h.clock.Add(after)
		_, cmd := h.m.Update(tickMsg{})
		h.run(cmd)
	}
	tick(30 * time.Second)
	if builds.called("list") != 1 {
		t.Errorf("an idle list was refreshed after 30s")
	}
	tick(2 * time.Minute)
	if builds.called("list") != 2 {
		t.Errorf("list calls = %d, want 2", builds.called("list"))
	}
	builds.list = []eas.Build{runningBuild}
	h.press("r")
	tick(30 * time.Second)
	if builds.called("list") != 4 {
		t.Errorf("list calls = %d, want 4 (a running build is watched)", builds.called("list"))
	}
}

func TestBuildProblemsAreExplained(t *testing.T) {
	builds := &fakeBuilds{listErr: apperr.New(apperr.CommandFailed, "You are not logged in to Expo").WithHint("Run: eas login")}
	h := startWithBuilds(t, builds)
	h.press("tab", "enter")
	h.wantScreen("You are not logged in to Expo", "Run: eas login")
	h.wantNoScreen("No builds yet")
}

func TestBuildDetails(t *testing.T) {
	builds := &fakeBuilds{list: []eas.Build{androidBuild, failedBuild, deviceBuild}}
	h := startWithBuilds(t, builds)
	h.press("tab", "enter")
	h.selectBuild(failedBuild.ID)
	h.press("enter")
	h.wantScreen("Build cccccccc", "failed", "production", "Provisioning profile has expired")
	h.press("esc")
	h.selectBuild(deviceBuild.ID)
	h.press("enter")
	h.wantScreen("for real devices")
}

func TestInstallABuild(t *testing.T) {
	builds := &fakeBuilds{list: []eas.Build{androidBuild}}
	// The booted iPhone simulator and the stopped tablet are not places for an Android build.
	h := startWithBuilds(t, builds, pixel, tablet, iphone)
	h.press("tab", "enter", "i")
	h.wantScreen("Install on which device?", "Pixel 9a")
	if window := h.window(); strings.Contains(window, "Tablet") || strings.Contains(window, "iPhone 17") {
		t.Errorf("only the running Android device should be offered:\n%s", window)
	}
	h.press("enter")
	if builds.called("download aaaaaaaa-1111") != 1 {
		t.Errorf("builds calls = %v", builds.calls)
	}
	h.wantCalls("install Pixel_9a /cache/aaaaaaaa-1111.apk", 1)
	h.wantScreen("Installed the build on Pixel 9a")
}

func TestInstallSaysWhyItCannot(t *testing.T) {
	builds := &fakeBuilds{list: []eas.Build{androidBuild, runningBuild, deviceBuild}}
	h := startWithBuilds(t, builds, iphone)
	h.press("tab", "enter")

	h.selectBuild(androidBuild.ID)
	h.press("i")
	h.wantScreen("No device to install on", "running Android emulator or phone")
	h.press("esc")

	h.selectBuild(runningBuild.ID)
	h.press("i")
	h.wantScreen("has not finished")
	h.press("esc")

	h.selectBuild(deviceBuild.ID)
	h.press("i")
	h.wantScreen("Only simulator builds can be installed")
	if builds.called("download") != 0 {
		t.Error("something was downloaded")
	}
}

func TestCancelABuild(t *testing.T) {
	builds := &fakeBuilds{list: []eas.Build{androidBuild, runningBuild}}
	h := startWithBuilds(t, builds)
	h.press("tab", "enter")

	h.selectBuild(androidBuild.ID)
	h.press("x")
	h.wantScreen("Only a build that is waiting or running")

	h.selectBuild(runningBuild.ID)
	h.press("x")
	h.wantScreen("Cancel this build?")
	h.press("n")
	if builds.called("cancel") != 0 {
		t.Error("cancelled without a yes")
	}
	h.press("x", "y")
	if builds.called("cancel bbbbbbbb-2222") != 1 {
		t.Errorf("calls = %v", builds.calls)
	}
}

func TestNewBuildShowsTheCommandAndAsks(t *testing.T) {
	builds := &fakeBuilds{profiles: []string{"development", "preview", "production"}}
	h := startWithBuilds(t, builds)
	h.press("tab", "enter", "n")
	h.wantScreen("Build profile", "development", "production")
	h.press("down", "enter")
	h.wantScreen("Platform for the preview build")
	h.press("down", "enter")
	h.wantScreen("Start this build?", "uses build time on your Expo account",
		"eas build --profile preview --platform ios --non-interactive --no-wait")
	h.press("enter")
	if builds.called("start") != 0 {
		t.Error("enter started a build")
	}
	h.press("y")
	if builds.called("start preview ios") != 1 {
		t.Errorf("calls = %v", builds.calls)
	}
	h.wantScreen("Started the preview build for ios")
	if builds.called("list") != 2 {
		t.Errorf("the list was not reloaded after starting: %v", builds.calls)
	}
}

func TestAProductionBuildNeedsItsNameTyped(t *testing.T) {
	builds := &fakeBuilds{profiles: []string{"development", "production"}}
	h := startWithBuilds(t, builds)
	h.press("tab", "enter", "n", "down", "enter", "enter")
	h.wantScreen("Start a production build?", "Type the profile name to confirm")
	h.press("y", "enter")
	h.wantScreen("Type production exactly")
	if builds.called("start") != 0 {
		t.Error("a production build started from one key")
	}
	h.press("ctrl+u")
	h.typeText("production")
	h.press("enter")
	if builds.called("start production android") != 1 {
		t.Errorf("calls = %v", builds.calls)
	}
}

func TestOpenTheBuildPage(t *testing.T) {
	builds := &fakeBuilds{list: []eas.Build{androidBuild, runningBuild}}
	h := startWithBuilds(t, builds)
	h.press("tab", "enter", "o")
	h.wantCalls("open https://expo.dev/accounts/sample-org/projects/sample-app/builds/aaaaaaaa-1111", 1)
	h.selectBuild(runningBuild.ID)
	h.press("o")
	h.wantCalls("open", 1)
}

func TestWithoutAProjectThereIsNoBuildsPane(t *testing.T) {
	h := start(t, pixel)
	h.wantNoScreen("Builds")
	h.press("tab")
	if h.m.focus != paneLogs {
		t.Errorf("tab went to pane %d, want the logs", h.m.focus)
	}
}

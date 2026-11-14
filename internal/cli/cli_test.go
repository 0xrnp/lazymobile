package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/0xrnp/lazymobile/internal/android"
	"github.com/0xrnp/lazymobile/internal/doctor"
	"github.com/0xrnp/lazymobile/internal/eas"
	"github.com/0xrnp/lazymobile/internal/mobile"
	"github.com/0xrnp/lazymobile/internal/run"
	"github.com/0xrnp/lazymobile/internal/run/runtest"
)

const simctlList = `{"devices": {"com.apple.CoreSimulator.SimRuntime.iOS-18-2": [
  {"udid": "00000000-0000-4000-8000-0000000000A1", "name": "iPhone 16", "state": "Shutdown", "isAvailable": true},
  {"udid": "00000000-0000-4000-8000-0000000000A2", "name": "iPhone 16 Pro", "state": "Booted", "isAvailable": true}
]}}`

// env is a command-line app whose tools are faked: two simulators, and no Android SDK.
type env struct {
	app        *App
	fake       *runtest.Fake
	out, errs  bytes.Buffer
	dashboards int
	report     doctor.Report
	project    *eas.Client
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{}
	e.fake = &runtest.Fake{Answer: func(command string, argv []string, _ run.Options) run.Result {
		switch {
		case strings.Contains(command, "simctl list devices"):
			return runtest.Out(simctlList)
		case strings.Contains(command, "devicectl list devices"):
			if err := os.WriteFile(argv[len(argv)-1], []byte(`{"result": {"devices": []}}`), 0o600); err != nil {
				t.Error(err)
			}
			return run.Result{}
		case strings.HasPrefix(command, "xcrun simctl"), strings.HasPrefix(command, "open "):
			return run.Result{}
		case strings.HasPrefix(command, "xcode-select"):
			return runtest.Fail(2, "no developer directory")
		}
		return run.Result{Missing: true}
	}}
	options := android.Options{
		OS: "darwin", Home: t.TempDir(), Getenv: func(string) string { return "" },
		LookPath:     func(string) (string, error) { return "", errors.New("not found") },
		SDKFallbacks: []string{}, JavaFallbacks: []string{},
	}
	e.app = &App{
		Manager: mobile.New(e.fake, options, "darwin"),
		Version: "1.2.3",
		Stdout:  &e.out,
		Stderr:  &e.errs,
		Dashboard: func(context.Context, *mobile.Manager) error {
			e.dashboards++
			return nil
		},
		Doctor: func(context.Context) doctor.Report { return e.report },
		Builds: func() BuildLister {
			if e.project == nil {
				return nil
			}
			return e.project
		},
	}
	return e
}

func (e *env) run(args ...string) int {
	e.out.Reset()
	e.errs.Reset()
	return e.app.Run(context.Background(), args)
}

func TestHelpAndVersion(t *testing.T) {
	e := newEnv(t)
	if code := e.run("help"); code != ExitOK || !strings.Contains(e.out.String(), "lazymobile boot <device>") {
		t.Errorf("help: code %d, output %q", code, e.out.String())
	}
	if code := e.run("version"); code != ExitOK || e.out.String() != "lazymobile 1.2.3\n" {
		t.Errorf("version: code %d, output %q", code, e.out.String())
	}
}

func TestUsageMistakesExitWith2(t *testing.T) {
	tests := map[string][]string{
		"unknown command":          {"frobnicate"},
		"unknown flag":             {"list", "--nope"},
		"missing device":           {"boot"},
		"two devices":              {"shutdown", "a", "b"},
		"list with an argument":    {"list", "extra"},
		"install without a path":   {"install", "iPhone 16"},
		"unknown log level":        {"logs", "iPhone 16 Pro", "--level", "loud"},
		"dashboard with a command": {"--no-window", "frobnicate"},
	}
	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			if code := e.run(args...); code != ExitUsage {
				t.Errorf("code = %d, want %d. stderr: %s", code, ExitUsage, e.errs.String())
			}
			if !strings.Contains(e.errs.String(), "lazymobile help") {
				t.Errorf("stderr should point at the help: %q", e.errs.String())
			}
		})
	}
}

func TestListAsText(t *testing.T) {
	e := newEnv(t)
	if code := e.run("list"); code != ExitOK {
		t.Fatalf("code = %d, stderr: %s", code, e.errs.String())
	}
	lines := strings.Split(strings.TrimSpace(e.out.String()), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "NAME") {
		t.Fatalf("output:\n%s", e.out.String())
	}
	// Running devices come first.
	if !strings.Contains(lines[1], "iPhone 16 Pro") || !strings.Contains(lines[1], "booted") || !strings.Contains(lines[1], "iOS 18.2") {
		t.Errorf("first row = %q", lines[1])
	}
	// The Android tools are missing here: that is a warning, not a failure.
	if !strings.Contains(e.errs.String(), "warning: adb was not found") {
		t.Errorf("stderr = %q", e.errs.String())
	}
}

func TestListAsJSON(t *testing.T) {
	e := newEnv(t)
	if code := e.run("list", "--json"); code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	var got struct {
		Devices []struct {
			ID, Name, Platform, Kind, State string
			OSVersion                       string `json:"os_version"`
		}
		Errors []struct{ Code, Message, Hint string }
	}
	if err := json.Unmarshal(e.out.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, e.out.String())
	}
	if len(got.Devices) != 2 || got.Devices[0].Name != "iPhone 16 Pro" || got.Devices[0].State != "booted" ||
		got.Devices[0].Platform != "ios" || got.Devices[0].Kind != "simulator" || got.Devices[0].OSVersion != "18.2" {
		t.Errorf("devices = %+v", got.Devices)
	}
	if len(got.Errors) != 2 || got.Errors[0].Code != "tool_missing" || got.Errors[0].Hint == "" {
		t.Errorf("errors = %+v", got.Errors)
	}
	if e.errs.Len() != 0 {
		t.Errorf("JSON mode wrote to stderr: %q", e.errs.String())
	}
}

func TestBootFindsTheDeviceByName(t *testing.T) {
	e := newEnv(t)
	if code := e.run("boot", "iphone 16", "--no-window"); code != ExitOK {
		t.Fatalf("code = %d, stderr: %s", code, e.errs.String())
	}
	if !e.fake.Called("simctl boot 00000000-0000-4000-8000-0000000000A1") {
		t.Errorf("ran %v", e.fake.Calls())
	}
	if e.fake.Called("open ") {
		t.Error("--no-window still opened a window")
	}
	if e.out.String() != "iPhone 16 is starting\n" {
		t.Errorf("output = %q", e.out.String())
	}
}

func TestBootOpensAWindowAndCanWait(t *testing.T) {
	e := newEnv(t)
	if code := e.run("boot", "--wait", "00000000-0000-4000-8000-0000000000A1"); code != ExitOK {
		t.Fatalf("code = %d, stderr: %s", code, e.errs.String())
	}
	if !e.fake.Called("open -b com.apple.iphonesimulator") || !e.fake.Called("simctl bootstatus") {
		t.Errorf("ran %v", e.fake.Calls())
	}
	if e.out.String() != "iPhone 16 is ready\n" {
		t.Errorf("output = %q", e.out.String())
	}
}

func TestDataIsOnlyRemovedWithYes(t *testing.T) {
	for _, args := range [][]string{
		{"erase", "iPhone 16"},
		{"delete", "iPhone 16"},
		{"boot", "iPhone 16", "--wipe"},
	} {
		e := newEnv(t)
		if code := e.run(args...); code != ExitUsage || !strings.Contains(e.errs.String(), "--yes") {
			t.Errorf("%v: code %d, stderr %q", args, code, e.errs.String())
		}
		for _, verb := range []string{"simctl erase", "simctl delete", "simctl boot"} {
			if e.fake.Called(verb) {
				t.Errorf("%v ran %q without --yes", args, verb)
			}
		}
	}

	e := newEnv(t)
	if code := e.run("erase", "iPhone 16", "--yes"); code != ExitOK || !e.fake.Called("simctl erase 00000000-0000-4000-8000-0000000000A1") {
		t.Errorf("erase --yes: code %d, ran %v", code, e.fake.Calls())
	}
	if code := e.run("delete", "--yes", "iPhone 16"); code != ExitOK || !e.fake.Called("simctl delete 00000000-0000-4000-8000-0000000000A1") {
		t.Errorf("delete --yes: code %d, ran %v", code, e.fake.Calls())
	}
}

func TestDevicesThatCannotBeFound(t *testing.T) {
	e := newEnv(t)
	if code := e.run("shutdown", "Galaxy"); code != ExitFailure || !strings.Contains(e.errs.String(), `No device matches "Galaxy"`) {
		t.Errorf("unknown: code %d, stderr %q", code, e.errs.String())
	}
	// "iphone" matches both, and guessing could shut down the wrong one.
	if code := e.run("shutdown", "iphone"); code != ExitFailure || !strings.Contains(e.errs.String(), "matches several devices") ||
		!strings.Contains(e.errs.String(), "00000000-0000-4000-8000-0000000000A2") {
		t.Errorf("ambiguous: code %d, stderr %q", code, e.errs.String())
	}
	if e.fake.Called("simctl shutdown") {
		t.Error("something was shut down")
	}
}

func TestID(t *testing.T) {
	e := newEnv(t)
	if code := e.run("id", "iPhone 16 Pro"); code != ExitOK || e.out.String() != "00000000-0000-4000-8000-0000000000A2\n" {
		t.Errorf("code %d, output %q", code, e.out.String())
	}
}

func TestLogsNeedARunningDevice(t *testing.T) {
	e := newEnv(t)
	if code := e.run("logs", "iPhone 16"); code != ExitFailure || !strings.Contains(e.errs.String(), "is not running") {
		t.Errorf("code %d, stderr %q", code, e.errs.String())
	}
}

func TestInstallChecksTheKindOfFile(t *testing.T) {
	e := newEnv(t)
	if code := e.run("install", "iPhone 16 Pro", "build.apk"); code != ExitFailure || !strings.Contains(e.errs.String(), ".app folder") {
		t.Errorf("code %d, stderr %q", code, e.errs.String())
	}
	if code := e.run("install", "iPhone 16 Pro", "/builds/Homes.app/"); code != ExitOK ||
		!e.fake.Called("simctl install 00000000-0000-4000-8000-0000000000A2 /builds/Homes.app/") {
		t.Errorf("code %d, ran %v, stderr %q", code, e.fake.Calls(), e.errs.String())
	}
	if code := e.run("install", "iPhone 16", "/builds/Homes.app"); code != ExitFailure || !strings.Contains(e.errs.String(), "is not running") {
		t.Errorf("stopped device: code %d, stderr %q", code, e.errs.String())
	}
}

func TestDashboardOpensWithoutACommand(t *testing.T) {
	e := newEnv(t)
	if code := e.run(); code != ExitOK || e.dashboards != 1 {
		t.Errorf("code %d, dashboards %d", code, e.dashboards)
	}
	if code := e.run("--no-window"); code != ExitOK || e.dashboards != 2 || e.app.Manager.OpenWindow {
		t.Errorf("code %d, dashboards %d, OpenWindow %v", code, e.dashboards, e.app.Manager.OpenWindow)
	}
}

func TestDoctor(t *testing.T) {
	e := newEnv(t)
	e.report = doctor.Report{
		OS:    "linux",
		Found: []doctor.Found{{Name: "adb", Detail: "/sdk/platform-tools/adb"}},
		Problems: []doctor.Problem{
			{Code: "android_emulator_missing", Platform: "android", Severity: doctor.Unavailable, Message: "The Android emulator is not installed", Hint: `Install it with: sdkmanager "emulator"`},
		},
	}
	if code := e.run("doctor"); code != ExitOK {
		t.Errorf("a missing tool is not a failure: code %d", code)
	}
	for _, part := range []string{"ok  adb: /sdk/platform-tools/adb", "The Android emulator is not installed", `sdkmanager "emulator"`, "iOS needs macOS"} {
		if !strings.Contains(e.out.String(), part) {
			t.Errorf("output should contain %q:\n%s", part, e.out.String())
		}
	}

	e.report.Problems = append(e.report.Problems, doctor.Problem{Code: "android_sdk_invalid", Platform: "android", Severity: doctor.Wrong, Message: "ANDROID_HOME points to /nope, which does not exist"})
	if code := e.run("doctor"); code != ExitFailure {
		t.Errorf("a wrong setting should fail: code %d", code)
	}

	e.report = doctor.Report{OS: "darwin"}
	if code := e.run("doctor", "--json"); code != ExitOK || !strings.Contains(e.out.String(), `"problems": []`) || !strings.Contains(e.out.String(), `"found": []`) {
		t.Errorf("code %d, output %s", code, e.out.String())
	}
}

func TestBuilds(t *testing.T) {
	e := newEnv(t)
	if code := e.run("builds"); code != ExitFailure || !strings.Contains(e.errs.String(), "No Expo project was found here") {
		t.Errorf("outside a project: code %d, stderr %q", code, e.errs.String())
	}

	fake := runtest.Table(map[string]run.Result{"build:list": runtest.Out(`[{"id": "BUILD-1", "status": "FINISHED", "platform": "ANDROID",
		"buildProfile": "preview", "createdAt": "2026-11-14T08:00:00.000Z"}]`)})
	e.project = &eas.Client{Runner: fake, Project: eas.Project{Dir: "/work/sample", Command: []string{"eas"}}}
	if code := e.run("builds", "--limit", "5"); code != ExitOK {
		t.Fatalf("code %d, stderr %q", code, e.errs.String())
	}
	if !fake.Called("--limit 5") || !strings.Contains(e.out.String(), "android") || !strings.Contains(e.out.String(), "BUILD-1") {
		t.Errorf("ran %v, output %q", fake.Calls(), e.out.String())
	}
	if code := e.run("builds", "--json"); code != ExitOK || !strings.Contains(e.out.String(), `"project": "sample"`) ||
		!strings.Contains(e.out.String(), `"profile": "preview"`) {
		t.Errorf("code %d, output %s", code, e.out.String())
	}
	if code := e.run("builds", "--limit", "0"); code != ExitUsage {
		t.Errorf("--limit 0: code %d", code)
	}
}

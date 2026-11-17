package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/0xrnp/lazymobile/internal/android"
	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/doctor"
	"github.com/0xrnp/lazymobile/internal/eas"
	"github.com/0xrnp/lazymobile/internal/mobile"
	"github.com/0xrnp/lazymobile/internal/run"
	"github.com/0xrnp/lazymobile/internal/run/runtest"
	"github.com/0xrnp/lazymobile/internal/state"
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
		case strings.Contains(command, "simctl listapps"):
			return runtest.Out("{}")
		case strings.HasPrefix(command, "plutil -convert json"):
			return runtest.Out(`{"com.example.installed": {"ApplicationType": "User", "CFBundleDisplayName": "Installed"}, "com.apple.mobilesafari": {"ApplicationType": "System"}}`)
		case strings.Contains(command, "simctl ui") && strings.HasSuffix(command, "appearance"):
			return runtest.Out("light\n")
		case strings.Contains(command, "simctl get_app_container"):
			return runtest.Out("/path/App.app\n")
		case strings.Contains(command, "simctl io"):
			if err := os.WriteFile(argv[len(argv)-1], []byte("\x89PNG\r\n\x1a\n"), 0o644); err != nil {
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
		State:  state.Store{Path: filepath.Join(t.TempDir(), "state.json")},
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
	app := filepath.Join(t.TempDir(), "Homes.app")
	if err := os.Mkdir(app, 0o755); err != nil {
		t.Fatal(err)
	}
	if code := e.run("install", "iPhone 16 Pro", "/builds/Homes.app/"); code != ExitFailure || !strings.Contains(e.errs.String(), "nothing at /builds/Homes.app/") {
		t.Errorf("a path with nothing there: code %d, stderr %q", code, e.errs.String())
	}
	if code := e.run("install", "iPhone 16 Pro", app+"/"); code != ExitOK ||
		!e.fake.Called("simctl install 00000000-0000-4000-8000-0000000000A2 "+app+"/") {
		t.Errorf("code %d, ran %v, stderr %q", code, e.fake.Calls(), e.errs.String())
	}
	if code := e.run("install", "iPhone 16", app); code != ExitFailure || !strings.Contains(e.errs.String(), "is not running") {
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

func TestQuitSimulatorAppUnlessKept(t *testing.T) {
	e := newEnv(t)
	if code := e.run("shutdown", "iPhone 16", "--keep-simulator-app"); code != ExitOK || e.fake.Called("pgrep") || e.fake.Called("osascript") {
		t.Errorf("kept: code %d, ran %v", code, e.fake.Calls())
	}
	// Neither app runs here, so they are looked for and nothing is quit.
	code := e.run("shutdown", "iPhone 16")
	calls := e.fake.Calls()
	if code != ExitOK || !e.fake.Called("pgrep -x DeviceHub") || e.fake.Called("osascript") {
		t.Errorf("default: code %d, ran %v, stderr %q", code, calls, e.errs.String())
	}
	if code := e.run("--keep-simulator-app"); code != ExitOK || e.app.Manager.QuitSimulatorApp {
		t.Errorf("dashboard: code %d, QuitSimulatorApp %v", code, e.app.Manager.QuitSimulatorApp)
	}
}

func TestShutdownWarnsWhenTheSimulatorAppStaysOpen(t *testing.T) {
	e := newEnv(t)
	answer := e.fake.Answer
	shutDown := false
	e.fake.Answer = func(command string, argv []string, opts run.Options) run.Result {
		switch {
		case strings.Contains(command, "simctl shutdown"):
			shutDown = true
		case shutDown && strings.Contains(command, "simctl list devices"):
			// After the shutdown, no simulator runs.
			return runtest.Out(`{"devices": {}}`)
		case strings.HasPrefix(command, "pgrep -x DeviceHub"):
			return run.Result{}
		case strings.HasPrefix(command, "pgrep"):
			return runtest.Fail(1, "")
		case strings.HasPrefix(command, "osascript"):
			return runtest.Fail(1, "Not authorized to send Apple events to DeviceHub. (-1743)")
		}
		return answer(command, argv, opts)
	}
	code := e.run("shutdown", "iPhone 16")
	if code != ExitOK || e.out.String() != "Shut down iPhone 16\n" {
		t.Errorf("code %d, stdout %q", code, e.out.String())
	}
	if !strings.Contains(e.errs.String(), "warning: The simulator shut down, but DeviceHub could not be quit") {
		t.Errorf("stderr = %q", e.errs.String())
	}
}

func TestAppsListsTheOnesUsedBeforeThenTheInstalledOnes(t *testing.T) {
	e := newEnv(t)
	if err := e.app.State.Update(func(f *state.File) { f.RememberApp(device.IOS, "com.used.before") }); err != nil {
		t.Fatal(err)
	}
	if code := e.run("apps", "iPhone 16 Pro", "--json"); code != ExitOK {
		t.Fatalf("code %d: %s", code, e.errs.String())
	}
	var got struct {
		Device string            `json:"device"`
		Apps   []state.AppChoice `json:"apps"`
	}
	if err := json.Unmarshal(e.out.Bytes(), &got); err != nil {
		t.Fatalf("%v: %s", err, e.out.String())
	}
	want := []state.AppChoice{{ID: "com.used.before", Recent: true}, {ID: "com.example.installed", Name: "Installed", Installed: true}}
	if got.Device != "iPhone 16 Pro" || !slices.Equal(got.Apps, want) {
		t.Errorf("got %+v", got)
	}

	// A simulator that is not running has no installed apps to list, only the ones used before.
	if code := e.run("apps", "iPhone 16"); code != ExitOK {
		t.Fatalf("code %d: %s", code, e.errs.String())
	}
	if !strings.Contains(e.out.String(), "com.used.before") || strings.Contains(e.out.String(), "installed") {
		t.Errorf("output: %s", e.out.String())
	}
	if !strings.Contains(e.errs.String(), "is not running") {
		t.Errorf("stderr: %s", e.errs.String())
	}
}

// A connected iPhone, and the apps devicectl lists on it, in the layout Xcode 27 writes.
const (
	devicectlPhone = `{"result": {"devices": [{"properties": {
	  "hardware": {"udid": "00000000-0000-4000-8000-0000000000B1", "reality": "physical", "deviceType": "iPhone"},
	  "state": {"name": "Test iPhone"},
	  "connection": {"transportType": "wired", "state": "connected", "pairingState": "paired"},
	  "software": {"osVersionNumber": "27.0"}}}]}}`
	devicectlPhoneApps = `{"result": {"apps": [
	  {"bundleIdentifier": "com.example.app.staging", "name": "Example Staging", "version": "1.4.2", "bundleVersion": "87"},
	  {"bundleIdentifier": "com.example.preview", "name": "Example Preview", "version": "2.0.0", "bundleVersion": "1"}]}}`
)

func TestAppsOfAnIPhoneShowTheirVersionAndBuild(t *testing.T) {
	e := newEnv(t)
	base := e.fake.Answer
	e.fake.Answer = func(command string, argv []string, opts run.Options) run.Result {
		output := ""
		switch {
		case strings.Contains(command, "devicectl list devices"):
			output = devicectlPhone
		case strings.Contains(command, "devicectl device info apps --device 00000000-0000-4000-8000-0000000000B1"):
			output = devicectlPhoneApps
		default:
			return base(command, argv, opts)
		}
		if err := os.WriteFile(argv[len(argv)-1], []byte(output), 0o600); err != nil {
			t.Error(err)
		}
		return run.Result{}
	}

	if code := e.run("apps", "Test iPhone"); code != ExitOK {
		t.Fatalf("code %d: %s", code, e.errs.String())
	}
	for _, want := range []string{"VERSION", "com.example.app.staging  Example Staging  1.4.2 (87)  installed", "com.example.preview"} {
		if !strings.Contains(e.out.String(), want) {
			t.Errorf("the table should contain %q:\n%s", want, e.out.String())
		}
	}

	if code := e.run("apps", "Test iPhone", "--json"); code != ExitOK {
		t.Fatalf("code %d: %s", code, e.errs.String())
	}
	var got struct {
		Apps []state.AppChoice `json:"apps"`
	}
	if err := json.Unmarshal(e.out.Bytes(), &got); err != nil {
		t.Fatalf("%v: %s", err, e.out.String())
	}
	want := []state.AppChoice{
		{ID: "com.example.app.staging", Name: "Example Staging", Version: "1.4.2", Build: "87", Installed: true},
		{ID: "com.example.preview", Name: "Example Preview", Version: "2.0.0", Build: "1", Installed: true},
	}
	if !slices.Equal(got.Apps, want) {
		t.Errorf("got %+v", got.Apps)
	}
}

func TestLastAppNeedsAnAppUsedBefore(t *testing.T) {
	e := newEnv(t)
	if code := e.run("logs", "iPhone 16 Pro", "--last-app"); code != ExitFailure || !strings.Contains(e.errs.String(), "No iOS app id has been used yet") {
		t.Errorf("code %d, stderr %s", code, e.errs.String())
	}
	if code := e.run("logs", "iPhone 16 Pro", "--last-app", "--app", "com.x"); code != ExitUsage {
		t.Errorf("both flags: code %d", code)
	}
	if code := e.run("logs", "iPhone 16 Pro", "--app", "com.x; reboot"); code != ExitUsage {
		t.Errorf("an invalid app id: code %d", code)
	}
	if apps := e.app.State.Load().Apps; apps != nil {
		t.Errorf("an invalid app id was remembered: %v", apps)
	}
}

func TestUninstallNeedsYesAndAnInstalledApp(t *testing.T) {
	e := newEnv(t)
	if code := e.run("uninstall", "iPhone 16 Pro", "com.example.installed"); code != ExitUsage || !strings.Contains(e.errs.String(), "--yes") {
		t.Errorf("without --yes: code %d, stderr %q", code, e.errs.String())
	}
	if code := e.run("uninstall", "iPhone 16 Pro", "--yes"); code != ExitUsage {
		t.Errorf("without an app id: code %d", code)
	}
	// simctl reports success for an app that is not there, so a typo must not get that far.
	if code := e.run("uninstall", "iPhone 16 Pro", "com.example.typo", "--yes"); code != ExitFailure || !strings.Contains(e.errs.String(), "not an installed app") {
		t.Errorf("a typo: code %d, stderr %q", code, e.errs.String())
	}
	if code := e.run("uninstall", "iPhone 16", "com.example.installed", "--yes"); code != ExitFailure || !strings.Contains(e.errs.String(), "is not running") {
		t.Errorf("a stopped simulator: code %d, stderr %q", code, e.errs.String())
	}
	if e.fake.Called("simctl uninstall") {
		t.Errorf("an uninstall ran: %v", e.fake.Calls())
	}

	if code := e.run("uninstall", "--yes", "iPhone 16 Pro", "com.example.installed"); code != ExitOK ||
		!e.fake.Called("simctl uninstall 00000000-0000-4000-8000-0000000000A2 com.example.installed") {
		t.Errorf("code %d, ran %v, stderr %q", code, e.fake.Calls(), e.errs.String())
	}
	if !strings.Contains(e.out.String(), "Uninstalled com.example.installed from iPhone 16 Pro") {
		t.Errorf("stdout %q", e.out.String())
	}
}

func TestLaunchAndStopAnApp(t *testing.T) {
	e := newEnv(t)
	const booted = "00000000-0000-4000-8000-0000000000A2"
	if code := e.run("launch", "iPhone 16 Pro", "com.example.installed"); code != ExitOK ||
		!e.fake.Called("simctl launch "+booted+" com.example.installed") ||
		!strings.Contains(e.out.String(), "Launched com.example.installed on iPhone 16 Pro") {
		t.Errorf("launch: code %d, out %q, calls %v", code, e.out.String(), e.fake.Calls())
	}
	if code := e.run("launch", "--restart", "iPhone 16 Pro", "com.example.installed"); code != ExitOK ||
		!e.fake.Called("simctl launch --terminate-running-process "+booted+" com.example.installed") ||
		!strings.Contains(e.out.String(), "Restarted com.example.installed on iPhone 16 Pro") {
		t.Errorf("restart: code %d, out %q, calls %v", code, e.out.String(), e.fake.Calls())
	}
	if code := e.run("stop", "iPhone 16 Pro", "com.example.installed"); code != ExitOK ||
		!e.fake.Called("simctl terminate "+booted+" com.example.installed") ||
		!strings.Contains(e.out.String(), "Stopped com.example.installed on iPhone 16 Pro") {
		t.Errorf("stop: code %d, out %q, calls %v", code, e.out.String(), e.fake.Calls())
	}
	// A launched app is offered next time, like one whose log was opened.
	if recent := e.app.State.Load().Apps[device.IOS]; len(recent) != 1 || recent[0] != "com.example.installed" {
		t.Errorf("remembered %v", recent)
	}
}

func TestLaunchAndStopUsageAndDevicesThatAreNotRunning(t *testing.T) {
	e := newEnv(t)
	for _, args := range [][]string{{"launch", "iPhone 16 Pro"}, {"stop"}, {"stop", "iPhone 16 Pro", "a", "b"}, {"stop", "iPhone 16 Pro", "a", "--restart"}} {
		if code := e.run(args...); code != ExitUsage {
			t.Errorf("%v: code %d, want %d", args, code, ExitUsage)
		}
	}
	if code := e.run("launch", "iPhone 16", "com.example.installed"); code != ExitFailure || !strings.Contains(e.errs.String(), "is not running") {
		t.Errorf("a stopped simulator: code %d, stderr %q", code, e.errs.String())
	}
	if e.fake.Called("simctl launch") || e.fake.Called("simctl terminate") {
		t.Errorf("an app was touched: %v", e.fake.Calls())
	}
	if recent := e.app.State.Load().Apps[device.IOS]; len(recent) != 0 {
		t.Errorf("a failed launch was remembered: %v", recent)
	}
}

// object is what a command printed with --json, which must be one JSON object.
func (e *env) object(t *testing.T) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(e.out.Bytes(), &out); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, e.out.String())
	}
	return out
}

func TestAFailureWithJSONIsAnErrorObjectOnStdout(t *testing.T) {
	e := newEnv(t)
	tests := []struct {
		name string
		args []string
		exit int
		code string
		hint bool
	}{
		{"a device that does not exist", []string{"shutdown", "Galaxy", "--json"}, ExitFailure, "not_found", true},
		{"a name that fits two devices", []string{"id", "--json", "iphone"}, ExitFailure, "invalid_state", true},
		{"a device that is not running", []string{"launch", "iPhone 16", "com.example.installed", "--json"}, ExitFailure, "invalid_state", true},
		{"a missing argument", []string{"boot", "--json"}, ExitUsage, "usage", false},
		{"a flag that does not exist", []string{"list", "--json", "--bogus"}, ExitUsage, "usage", false},
		{"a command that does not exist", []string{"fly", "--json"}, ExitUsage, "usage", false},
		{"a destructive command without --yes", []string{"erase", "iPhone 16", "--json"}, ExitUsage, "usage", false},
	}
	for _, tt := range tests {
		if code := e.run(tt.args...); code != tt.exit {
			t.Errorf("%s: exit code %d, want %d", tt.name, code, tt.exit)
		}
		if e.errs.Len() != 0 {
			t.Errorf("%s: stderr has %q, want nothing", tt.name, e.errs.String())
		}
		failure, _ := e.object(t)["error"].(map[string]any)
		if failure["code"] != tt.code || failure["message"] == "" || (failure["hint"] != nil) != tt.hint {
			t.Errorf("%s: error object = %v, want code %s (hint %v)", tt.name, failure, tt.code, tt.hint)
		}
	}
}

func TestAFailureWithoutJSONStaysText(t *testing.T) {
	e := newEnv(t)
	if code := e.run("shutdown", "Galaxy"); code != ExitFailure || e.out.Len() != 0 || !strings.Contains(e.errs.String(), "No device matches") {
		t.Errorf("code %d, stdout %q, stderr %q", code, e.out.String(), e.errs.String())
	}
}

func TestEveryChangeWithJSONSaysWhatItDidToWhichDevice(t *testing.T) {
	e := newEnv(t)
	apk := filepath.Join(t.TempDir(), "App.app")
	if err := os.Mkdir(apk, 0o755); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		args   []string
		action string
		app    string
		path   string
	}{
		{[]string{"boot", "iPhone 16", "--no-window", "--json"}, "boot", "", ""},
		{[]string{"shutdown", "iPhone 16 Pro", "--json"}, "shutdown", "", ""},
		{[]string{"erase", "iPhone 16", "--yes", "--json"}, "erase", "", ""},
		{[]string{"delete", "iPhone 16", "--yes", "--json"}, "delete", "", ""},
		{[]string{"install", "iPhone 16 Pro", apk, "--json"}, "install", "", apk},
		{[]string{"uninstall", "iPhone 16 Pro", "com.example.installed", "--yes", "--json"}, "uninstall", "com.example.installed", ""},
		{[]string{"launch", "iPhone 16 Pro", "com.example.installed", "--json"}, "launch", "com.example.installed", ""},
		{[]string{"launch", "iPhone 16 Pro", "com.example.installed", "--restart", "--json"}, "restart", "com.example.installed", ""},
		{[]string{"stop", "iPhone 16 Pro", "com.example.installed", "--json"}, "stop", "com.example.installed", ""},
	}
	for _, tt := range tests {
		if code := e.run(tt.args...); code != ExitOK {
			t.Errorf("%v: exit code %d, stderr %q", tt.args, code, e.errs.String())
			continue
		}
		out := e.object(t)
		dev, _ := out["device"].(map[string]any)
		if out["ok"] != true || out["action"] != tt.action || dev["name"] == "" || dev["id"] == "" {
			t.Errorf("%v: printed %v", tt.args, out)
		}
		if got, _ := out["app"].(string); got != tt.app {
			t.Errorf("%v: app = %q, want %q", tt.args, got, tt.app)
		}
		if got, _ := out["path"].(string); got != tt.path {
			t.Errorf("%v: path = %q, want %q", tt.args, got, tt.path)
		}
	}
}

func TestIDWithJSONGivesTheDeviceAndItsID(t *testing.T) {
	e := newEnv(t)
	if code := e.run("id", "iPhone 16 Pro", "--json"); code != ExitOK {
		t.Fatalf("exit code %d", code)
	}
	out := e.object(t)
	if out["id"] != "00000000-0000-4000-8000-0000000000A2" || out["device"].(map[string]any)["name"] != "iPhone 16 Pro" {
		t.Errorf("printed %v", out)
	}
}

func TestDoctorWithJSONPrintsOnlyItsReportWhenSomethingIsWrong(t *testing.T) {
	e := newEnv(t)
	e.report = doctor.Report{OS: "darwin", Problems: []doctor.Problem{{Code: "android_sdk_invalid", Platform: "android", Severity: doctor.Wrong, Message: "ANDROID_HOME is wrong"}}}
	if code := e.run("doctor", "--json"); code != ExitFailure {
		t.Errorf("a wrong setting should still fail: code %d", code)
	}
	out := e.object(t)
	if _, hasError := out["error"]; hasError || out["problems"] == nil {
		t.Errorf("printed %v, want the report and no error object", out)
	}
}

func TestListWithJSONKeepsItsErrorsInTheSameShape(t *testing.T) {
	e := newEnv(t)
	if code := e.run("list", "--json"); code != ExitOK {
		t.Fatalf("exit code %d", code)
	}
	problems, _ := e.object(t)["errors"].([]any)
	for _, p := range problems {
		if p.(map[string]any)["code"] == nil {
			t.Errorf("an error without a code: %v", p)
		}
	}
}

func TestLinkOpensAWebAddressOrADeepLink(t *testing.T) {
	e := newEnv(t)
	if code := e.run("link", "iPhone 16 Pro", "myapp://home?id=1&x=2"); code != ExitOK ||
		!e.fake.Called("simctl openurl 00000000-0000-4000-8000-0000000000A2 myapp://home?id=1&x=2") ||
		!strings.Contains(e.out.String(), "Opened myapp://home?id=1&x=2 on iPhone 16 Pro") {
		t.Errorf("code %d, out %q, calls %v", code, e.out.String(), e.fake.Calls())
	}
	if code := e.run("link", "iPhone 16 Pro", "https://example.com", "--json"); code != ExitOK {
		t.Fatalf("code %d", code)
	}
	if out := e.object(t); out["action"] != "link" || out["url"] != "https://example.com" || out["ok"] != true {
		t.Errorf("printed %v", out)
	}
}

func TestLinkUsageAndRefusals(t *testing.T) {
	e := newEnv(t)
	for _, args := range [][]string{{"link"}, {"link", "iPhone 16 Pro"}, {"link", "iPhone 16 Pro", "a", "b"}} {
		if code := e.run(args...); code != ExitUsage {
			t.Errorf("%v: code %d, want %d", args, code, ExitUsage)
		}
	}
	for _, args := range [][]string{
		{"link", "iPhone 16", "https://example.com"}, // not running
		{"link", "iPhone 16 Pro", "not a link"},      // a bad link
	} {
		if code := e.run(args...); code != ExitFailure {
			t.Errorf("%v: code %d, want %d", args, code, ExitFailure)
		}
	}
	// Something that looks like a flag is a typing mistake, and never reaches a device.
	if code := e.run("link", "iPhone 16 Pro", "-a Calculator"); code != ExitUsage {
		t.Errorf("an option as a link: code %d, want %d", code, ExitUsage)
	}
	if e.fake.Called("openurl") {
		t.Errorf("a link was opened: %v", e.fake.Calls())
	}
}

func TestScreenshotSavesAPNGAndNeverReplacesAFile(t *testing.T) {
	e := newEnv(t)
	path := filepath.Join(t.TempDir(), "shot.png")
	if code := e.run("screenshot", "iPhone 16 Pro", "--out", path); code != ExitOK || !strings.Contains(e.out.String(), "Saved "+path) {
		t.Fatalf("code %d, out %q, stderr %q", code, e.out.String(), e.errs.String())
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("file %v, error %v", info, err)
	}
	if code := e.run("screenshot", "iPhone 16 Pro", "--out", path, "--json"); code != ExitFailure {
		t.Errorf("a file that exists: code %d", code)
	}
	if failure, _ := e.object(t)["error"].(map[string]any); failure["code"] != "invalid_state" || failure["hint"] == nil {
		t.Errorf("printed %v", e.out.String())
	}
	other := filepath.Join(t.TempDir(), "other.png")
	if code := e.run("screenshot", "iPhone 16 Pro", "--out", other, "--json"); code != ExitOK {
		t.Fatalf("code %d", code)
	}
	if out := e.object(t); out["action"] != "screenshot" || out["path"] != other {
		t.Errorf("printed %v", out)
	}
	if code := e.run("screenshot"); code != ExitUsage {
		t.Errorf("without a device: code %d", code)
	}
}

func TestScreenshotWithoutAFileNameGoesInTheCurrentFolderUnderADatedName(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	t.Chdir(dir)
	if code := e.run("screenshot", "iPhone 16 Pro"); code != ExitOK {
		t.Fatalf("code %d, stderr %q", code, e.errs.String())
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "lazymobile-iPhone-16-Pro-*.png"))
	if len(matches) != 1 {
		t.Errorf("files %v, out %q", matches, e.out.String())
	}
}

func TestAppearanceShowsOrSwitchesTheMode(t *testing.T) {
	e := newEnv(t)
	if code := e.run("appearance", "iPhone 16 Pro"); code != ExitOK || strings.TrimSpace(e.out.String()) != "light" {
		t.Errorf("read: code %d, out %q", code, e.out.String())
	}
	if code := e.run("appearance", "iPhone 16 Pro", "--json"); code != ExitOK || e.object(t)["appearance"] != "light" {
		t.Errorf("read as JSON: code %d, out %q", code, e.out.String())
	}
	if code := e.run("appearance", "iPhone 16 Pro", "dark"); code != ExitOK || !e.fake.Called("simctl ui 00000000-0000-4000-8000-0000000000A2 appearance dark") ||
		!strings.Contains(e.out.String(), "iPhone 16 Pro is now in dark mode") {
		t.Errorf("set: code %d, out %q, calls %v", code, e.out.String(), e.fake.Calls())
	}
	if code := e.run("appearance", "iPhone 16 Pro", "dark", "--json"); code != ExitOK {
		t.Fatalf("code %d", code)
	}
	if out := e.object(t); out["action"] != "appearance" || out["appearance"] != "dark" || out["ok"] != true {
		t.Errorf("printed %v", out)
	}
	for _, args := range [][]string{{"appearance"}, {"appearance", "iPhone 16 Pro", "dark", "extra"}} {
		if code := e.run(args...); code != ExitUsage {
			t.Errorf("%v: code %d, want %d", args, code, ExitUsage)
		}
	}
	if code := e.run("appearance", "iPhone 16 Pro", "sepia"); code != ExitFailure || !strings.Contains(e.errs.String(), "Use dark or light") {
		t.Errorf("a mode that is not one: code %d, stderr %q", code, e.errs.String())
	}
}

func TestLocationAndStatusBarOnASimulator(t *testing.T) {
	e := newEnv(t)
	const booted = "00000000-0000-4000-8000-0000000000A2"
	if code := e.run("location", "iPhone 16 Pro", "37.7749,-122.4194"); code != ExitOK ||
		!e.fake.Called("simctl location "+booted+" set 37.7749,-122.4194") ||
		!strings.Contains(e.out.String(), "iPhone 16 Pro is now at 37.7749,-122.4194") {
		t.Errorf("set: code %d, out %q, calls %v", code, e.out.String(), e.fake.Calls())
	}
	if code := e.run("location", "iPhone 16 Pro", "clear", "--json"); code != ExitOK || e.object(t)["location"] != "cleared" {
		t.Errorf("clear: code %d, out %q", code, e.out.String())
	}
	if code := e.run("location", "iPhone 16 Pro", "12.5,-3", "--json"); code != ExitOK || e.object(t)["location"] != "12.5,-3" {
		t.Errorf("set as JSON: code %d, out %q", code, e.out.String())
	}
	if code := e.run("statusbar", "iPhone 16 Pro", "clean", "--json"); code != ExitOK || e.object(t)["action"] != "statusbar_clean" ||
		!e.fake.Called("simctl status_bar "+booted+" override --time 9:41") {
		t.Errorf("clean: code %d, out %q", code, e.out.String())
	}
	if code := e.run("statusbar", "iPhone 16 Pro", "reset"); code != ExitOK || !e.fake.Called("simctl status_bar "+booted+" clear") ||
		!strings.Contains(e.out.String(), "back to normal") {
		t.Errorf("reset: code %d, out %q", code, e.out.String())
	}
}

func TestLocationAndStatusBarUsageAndRefusals(t *testing.T) {
	e := newEnv(t)
	for _, args := range [][]string{{"location"}, {"location", "iPhone 16 Pro"}, {"statusbar", "iPhone 16 Pro"}, {"statusbar", "iPhone 16 Pro", "shiny"}} {
		if code := e.run(args...); code != ExitUsage {
			t.Errorf("%v: code %d, want %d", args, code, ExitUsage)
		}
	}
	for _, args := range [][]string{
		{"location", "iPhone 16 Pro", "999,2"},
		{"location", "iPhone 16 Pro", "somewhere"},
		{"location", "iPhone 16", "1,2"}, // not running
		{"statusbar", "iPhone 16", "clean"},
	} {
		if code := e.run(args...); code != ExitFailure {
			t.Errorf("%v: code %d, want %d", args, code, ExitFailure)
		}
	}
	if e.fake.Called("simctl location") || e.fake.Called("status_bar") {
		t.Errorf("a tool ran: %v", e.fake.Calls())
	}
}

func TestPermissionGrantsAndRevokesOnASimulator(t *testing.T) {
	e := newEnv(t)
	const booted = "00000000-0000-4000-8000-0000000000A2"
	if code := e.run("permission", "iPhone 16 Pro", "grant", "photos", "com.example.installed"); code != ExitOK ||
		!e.fake.Called("simctl privacy "+booted+" grant photos com.example.installed") ||
		!strings.Contains(e.out.String(), "Granted photos to com.example.installed on iPhone 16 Pro") {
		t.Errorf("grant: code %d, out %q, calls %v", code, e.out.String(), e.fake.Calls())
	}
	if code := e.run("permission", "iPhone 16 Pro", "revoke", "location", "com.example.installed"); code != ExitOK ||
		!strings.Contains(e.out.String(), "Revoked location from com.example.installed on iPhone 16 Pro") {
		t.Errorf("revoke: code %d, out %q", code, e.out.String())
	}
	if code := e.run("permission", "iPhone 16 Pro", "revoke", "location", "com.example.installed", "--json"); code != ExitOK {
		t.Fatalf("code %d", code)
	}
	if out := e.object(t); out["action"] != "permission_revoke" || out["permission"] != "location" || out["app"] != "com.example.installed" {
		t.Errorf("printed %v", out)
	}
	for _, args := range [][]string{
		{"permission"}, {"permission", "iPhone 16 Pro", "grant", "photos"}, {"permission", "iPhone 16 Pro", "allow", "photos", "a.b"},
	} {
		if code := e.run(args...); code != ExitUsage {
			t.Errorf("%v: code %d, want %d", args, code, ExitUsage)
		}
	}
	if code := e.run("permission", "iPhone 16 Pro", "grant", "camera", "com.example.installed"); code != ExitFailure {
		t.Errorf("a permission a simulator does not have: code %d", code)
	}
}

func TestClearDataNeedsYesAndAnEmulator(t *testing.T) {
	e := newEnv(t)
	if code := e.run("cleardata", "iPhone 16 Pro", "com.example.installed"); code != ExitUsage || !strings.Contains(e.errs.String(), "--yes") {
		t.Errorf("without --yes: code %d, stderr %q", code, e.errs.String())
	}
	// An iOS simulator has no way to do it, and says what to do instead.
	if code := e.run("cleardata", "iPhone 16 Pro", "com.example.installed", "--yes"); code != ExitFailure || !strings.Contains(e.errs.String(), "Uninstall the app") {
		t.Errorf("on a simulator: code %d, stderr %q", code, e.errs.String())
	}
	if e.fake.Called("rm ") || e.fake.Called("get_app_container") {
		t.Errorf("a tool ran: %v", e.fake.Calls())
	}
}

func TestRecordRefusesBeforeAnyToolRuns(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	if code := e.run("record", "iPhone 16 Pro", "--duration", "-5s"); code != ExitUsage {
		t.Errorf("a negative duration: code %d", code)
	}
	if code := e.run("record", "iPhone 16 Pro", "--out", filepath.Join(dir, "no", "a.mov")); code != ExitFailure {
		t.Errorf("a folder that is not there: code %d", code)
	}
	if code := e.run("record"); code != ExitUsage {
		t.Errorf("no device: code %d", code)
	}
	if e.fake.Called("recordVideo") {
		t.Errorf("a recording started: %v", e.fake.Calls())
	}
}

func TestBuildsShowACommandFromTheSettingsFileAndNeedYes(t *testing.T) {
	e := newEnv(t)
	fake := runtest.Table(map[string]run.Result{"build:list": runtest.Out(`[]`)})
	e.project = &eas.Client{Runner: fake, Project: eas.Project{Dir: "/work/sample", Command: []string{"pnpm", "exec", "eas"}, Configured: true}}
	code := e.run("builds")
	if code != ExitUsage || !strings.Contains(e.errs.String(), "pnpm exec eas") || !strings.Contains(e.errs.String(), "--yes") {
		t.Errorf("without --yes: code %d, stderr %q", code, e.errs.String())
	}
	if len(fake.Calls()) != 0 {
		t.Errorf("the command ran without --yes: %v", fake.Calls())
	}
	if code := e.run("builds", "--yes"); code != ExitOK || !fake.Called("build:list") {
		t.Errorf("with --yes: code %d, ran %v, stderr %q", code, fake.Calls(), e.errs.String())
	}
}

func TestDevUsageAndRefusals(t *testing.T) {
	e := newEnv(t)
	for _, args := range [][]string{{"dev"}, {"dev", "iPhone 16 Pro"}, {"dev", "iPhone 16 Pro", "explode"}, {"dev", "iPhone 16 Pro", "reload", "extra"}} {
		if code := e.run(args...); code != ExitUsage {
			t.Errorf("%v: code %d, want %d", args, code, ExitUsage)
		}
	}
	if code := e.run("dev", "iPhone 16 Pro", "reverse", "--port", "99999"); code != ExitUsage {
		t.Errorf("a port that cannot be: code %d, want %d", code, ExitUsage)
	}
	// A simulator cannot be sent a key, so it is refused before any tool runs.
	if code := e.run("dev", "iPhone 16 Pro", "menu"); code != ExitFailure || e.fake.Called("input") {
		t.Errorf("code %d, calls %v", code, e.fake.Calls())
	}
}

func TestLogsTailPrintsTheLastLinesAndEnds(t *testing.T) {
	dir := t.TempDir()
	body := "#!/bin/sh\nfor i in 1 2 3 4 5; do echo \"2026-11-13 20:56:3$i.411 Df Runner[30348:6e9c9] line$i\"; done\n"
	if err := os.WriteFile(filepath.Join(dir, "xcrun"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	e := newEnv(t)
	if code := e.run("logs", "iPhone 16 Pro", "--tail", "2"); code != ExitOK {
		t.Fatalf("code %d, stderr %q", code, e.errs.String())
	}
	out := e.out.String()
	if strings.Contains(out, "line3") || !strings.Contains(out, "line4") || !strings.Contains(out, "line5") {
		t.Errorf("printed %q, want only the last two lines", out)
	}
}

func TestLogsTailAndSinceTakeOnlyPositiveValues(t *testing.T) {
	e := newEnv(t)
	for _, args := range [][]string{{"logs", "iPhone 16 Pro", "--tail", "-1"}, {"logs", "iPhone 16 Pro", "--since", "-5m"}, {"logs", "iPhone 16 Pro", "--since", "soon"}} {
		if code := e.run(args...); code != ExitUsage {
			t.Errorf("%v: code %d, want %d", args, code, ExitUsage)
		}
	}
}

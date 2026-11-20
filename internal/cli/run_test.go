package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0xrnp/lazymobile/internal/actions"
	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/mobile"
	"github.com/0xrnp/lazymobile/internal/run"
	"github.com/0xrnp/lazymobile/internal/run/runtest"
)

const (
	runningSim = "00000000-0000-4000-8000-0000000000A2"
	stoppedSim = "00000000-0000-4000-8000-0000000000A1"
)

// phoneList is `devicectl list devices` with one connected real phone.
const phoneList = `{"result": {"devices": [{"identifier": "CORE-1",
  "hardwareProperties": {"udid": "00008000-000000000000001E", "deviceType": "iPhone", "reality": "physical"},
  "deviceProperties": {"name": "Someone's iPhone", "osVersionNumber": "18.2", "developerModeStatus": "enabled"},
  "connectionProperties": {"tunnelState": "connected", "pairingState": "paired"}}]}}`

// runEnv is an environment where `run` looks at a folder and records the command instead of
// running it.
type runEnv struct {
	*env
	dir   string
	ran   [][]string
	where []string
}

func newRunEnv(t *testing.T, files ...string) *runEnv {
	t.Helper()
	e := &runEnv{env: newEnv(t), dir: t.TempDir()}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(e.dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	e.app.Getwd = func() (string, error) { return e.dir, nil }
	e.app.RunTool = func(_ context.Context, dir string, argv []string) error {
		e.ran = append(e.ran, argv)
		e.where = append(e.where, dir)
		return nil
	}
	return e
}

func TestRunUsesTheOneRunningDeviceForAFlutterProject(t *testing.T) {
	e := newRunEnv(t, "pubspec.yaml")
	if code := e.run("run"); code != ExitOK {
		t.Fatalf("code %d, stderr %q", code, e.errs.String())
	}
	if len(e.ran) != 1 || strings.Join(e.ran[0], " ") != "flutter run -d "+runningSim || e.where[0] != e.dir {
		t.Errorf("ran %v in %v", e.ran, e.where)
	}
	if !strings.Contains(e.errs.String(), "Running `flutter run -d "+runningSim+"`") {
		t.Errorf("the command was not shown: %q", e.errs.String())
	}
	if e.fake.Called("simctl boot") {
		t.Error("a running device was booted again")
	}
}

func TestRunStartsADeviceThatIsOffAndPassesTheToolsFlagsThrough(t *testing.T) {
	e := newRunEnv(t, "pubspec.yaml")
	if code := e.run("run", "iPhone 16", "--", "--flavor", "dev"); code != ExitOK {
		t.Fatalf("code %d, stderr %q", code, e.errs.String())
	}
	if !e.fake.Called("simctl boot "+stoppedSim) || !e.fake.Called("simctl bootstatus") {
		t.Errorf("the device was not started and waited for: %v", e.fake.Calls())
	}
	if len(e.ran) != 1 || strings.Join(e.ran[0], " ") != "flutter run -d "+stoppedSim+" --flavor dev" {
		t.Errorf("ran %v", e.ran)
	}
}

func TestRunNeedsADeviceNamedWhenNoneOrSeveralAreRunning(t *testing.T) {
	for name, list := range map[string]string{
		"none":    strings.ReplaceAll(simctlList, `"Booted"`, `"Shutdown"`),
		"several": strings.ReplaceAll(simctlList, `"Shutdown"`, `"Booted"`),
	} {
		e := newRunEnv(t, "pubspec.yaml")
		answer := e.fake.Answer
		e.fake.Answer = func(command string, argv []string, opts run.Options) run.Result {
			if strings.Contains(command, "simctl list devices") {
				return runtest.Out(list)
			}
			return answer(command, argv, opts)
		}
		if code := e.run("run"); code != ExitFailure || len(e.ran) != 0 || !strings.Contains(e.errs.String(), "lazymobile run <device>") {
			t.Errorf("%s: code %d, ran %v, stderr %q", name, code, e.ran, e.errs.String())
		}
		if e.fake.Called("simctl boot") {
			t.Errorf("%s: something was started without being named", name)
		}
	}
}

func TestRunNeverPicksARealPhoneOnItsOwn(t *testing.T) {
	e := newRunEnv(t, "pubspec.yaml")
	answer := e.fake.Answer
	e.fake.Answer = func(command string, argv []string, opts run.Options) run.Result {
		if strings.Contains(command, "devicectl list devices") {
			if err := os.WriteFile(argv[len(argv)-1], []byte(phoneList), 0o600); err != nil {
				t.Error(err)
			}
			return run.Result{}
		}
		return answer(command, argv, opts)
	}
	if code := e.run("run"); code != ExitOK || len(e.ran) != 1 || e.ran[0][3] != runningSim {
		t.Fatalf("code %d, ran %v, stderr %q", code, e.ran, e.errs.String())
	}
	if code := e.run("run", "Someone's iPhone"); code != ExitOK || len(e.ran) != 2 || e.ran[1][3] != "00008000-000000000000001E" {
		t.Errorf("a named phone was not used: code %d, ran %v, stderr %q", code, e.ran, e.errs.String())
	}
}

func TestRunSaysWhatItDoesNotKnow(t *testing.T) {
	e := newRunEnv(t)
	if code := e.run("run"); code != ExitFailure || len(e.ran) != 0 ||
		!strings.Contains(e.errs.String(), "Flutter, Expo and React Native") || !strings.Contains(e.errs.String(), ".lazymobile.json") {
		t.Errorf("code %d, ran %v, stderr %q", code, e.ran, e.errs.String())
	}
}

func TestRunUsesTheProjectsOwnRunActionAfterYes(t *testing.T) {
	e := newRunEnv(t, "pubspec.yaml")
	e.app.Actions = func() ([]actions.Action, error) {
		return []actions.Action{{Name: "run", Command: []string{"make", "run-dev", "DEVICE={device_id}"}, Dir: e.dir}}, nil
	}
	code := e.run("run", "iPhone 16 Pro")
	if code != ExitUsage || len(e.ran) != 0 || !strings.Contains(e.errs.String(), "make run-dev DEVICE="+runningSim) ||
		!strings.Contains(e.errs.String(), "--yes") {
		t.Fatalf("without --yes: code %d, ran %v, stderr %q", code, e.ran, e.errs.String())
	}
	if code := e.run("run", "iPhone 16 Pro", "--yes", "--", "FLAVOR=dev"); code != ExitOK ||
		len(e.ran) != 1 || strings.Join(e.ran[0], " ") != "make run-dev DEVICE="+runningSim+" FLAVOR=dev" {
		t.Errorf("with --yes: code %d, ran %v, stderr %q", code, e.ran, e.errs.String())
	}
}

func TestRunDoesNotStartADeviceBeforeYes(t *testing.T) {
	e := newRunEnv(t)
	e.app.Actions = func() ([]actions.Action, error) {
		return []actions.Action{{Name: "run", Command: []string{"make", "run"}, Dir: e.dir}}, nil
	}
	if code := e.run("run", "iPhone 16"); code != ExitUsage || e.fake.Called("simctl boot") {
		t.Errorf("code %d, ran %v", code, e.fake.Calls())
	}
}

func TestRunRemembersTheDeviceForNextTimeInThatFolder(t *testing.T) {
	e := newRunEnv(t, "pubspec.yaml")
	// Both simulators run, so without a memory the choice would be refused.
	both := strings.ReplaceAll(simctlList, `"Shutdown"`, `"Booted"`)
	answer := e.fake.Answer
	e.fake.Answer = func(command string, argv []string, opts run.Options) run.Result {
		if strings.Contains(command, "simctl list devices") {
			return runtest.Out(both)
		}
		return answer(command, argv, opts)
	}
	if code := e.run("run"); code != ExitFailure {
		t.Fatalf("with two running and nothing remembered: code %d, stderr %q", code, e.errs.String())
	}
	if code := e.run("run", "iPhone 16"); code != ExitOK {
		t.Fatalf("code %d, stderr %q", code, e.errs.String())
	}
	if code := e.run("run"); code != ExitOK || len(e.ran) != 2 || e.ran[1][3] != stoppedSim ||
		!strings.Contains(e.errs.String(), "last run on") {
		t.Errorf("the device was not remembered: code %d, ran %v, stderr %q", code, e.ran, e.errs.String())
	}
	// Another folder has its own memory.
	e.dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(e.dir, "pubspec.yaml"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := e.run("run"); code != ExitFailure {
		t.Errorf("another folder used the memory: code %d, ran %v", code, e.ran)
	}
}

func TestRunDoesNotRememberARealPhone(t *testing.T) {
	e := newRunEnv(t, "pubspec.yaml")
	answer := e.fake.Answer
	e.fake.Answer = func(command string, argv []string, opts run.Options) run.Result {
		if strings.Contains(command, "devicectl list devices") {
			if err := os.WriteFile(argv[len(argv)-1], []byte(phoneList), 0o600); err != nil {
				t.Error(err)
			}
			return run.Result{}
		}
		return answer(command, argv, opts)
	}
	if code := e.run("run", "Someone's iPhone"); code != ExitOK {
		t.Fatalf("code %d, stderr %q", code, e.errs.String())
	}
	if code := e.run("run"); code != ExitOK || e.ran[1][3] != runningSim {
		t.Errorf("the phone was remembered: code %d, ran %v", code, e.ran)
	}
}

// expoProject writes a package.json that lists expo, and the project's own expo tool in the folder
// above it, as a monorepo keeps it.
func expoProject(t *testing.T, e *runEnv, withTool bool) string {
	t.Helper()
	root := t.TempDir()
	app := filepath.Join(root, "apps", "shop")
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "package.json"), []byte(`{"dependencies": {"expo": "~54.0.0", "react-native": "0.81.0"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "node_modules", ".bin", "expo")
	if withTool {
		if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	e.dir = app
	return bin
}

func TestRunRunsAnExpoProjectWithItsOwnExpoOnTheDevice(t *testing.T) {
	e := newRunEnv(t)
	bin := expoProject(t, e, true)
	if code := e.run("run", "iPhone 16 Pro", "--", "--no-install"); code != ExitOK {
		t.Fatalf("code %d, stderr %q", code, e.errs.String())
	}
	if len(e.ran) != 1 || strings.Join(e.ran[0], " ") != bin+" run:ios --device "+runningSim+" --no-install" || e.where[0] != e.dir {
		t.Errorf("ran %v in %v", e.ran, e.where)
	}
}

func TestExpoIsGivenTheDeviceNameItMatchesOnAndroid(t *testing.T) {
	// expo run:android --device matches a name (Expo 54, resolveFromNameAsync), and run:ios a name
	// or UDID.
	for _, tc := range []struct {
		d    device.Device
		want string
	}{
		{device.Device{ID: runningSim, Platform: device.IOS, Kind: device.Simulator}, runningSim},
		{device.Device{ID: "Pixel_9a", Serial: "emulator-5554", Platform: device.Android, Kind: device.Emulator}, "Pixel_9a"},
		{device.Device{ID: "R5CT", Model: "SM_S911B", Platform: device.Android, Kind: device.Physical}, "SM_S911B"},
		{device.Device{ID: "R5CT", Platform: device.Android, Kind: device.Physical}, "Device R5CT"},
	} {
		if got := toolDevice("expo", tc.d); strings.Join(got, " ") != "--device "+tc.want {
			t.Errorf("%s: %q, want --device %s", tc.d.ID, got, tc.want)
		}
	}
}

func TestRunDoesNotDownloadExpoWhenTheProjectHasNone(t *testing.T) {
	e := newRunEnv(t)
	expoProject(t, e, false)
	if code := e.run("run", "iPhone 16 Pro"); code != ExitFailure || len(e.ran) != 0 || !strings.Contains(e.errs.String(), "expo is not installed") {
		t.Errorf("code %d, ran %v, stderr %q", code, e.ran, e.errs.String())
	}
}

// writeProject writes package.json and an empty file for each of the others, such as a lock file
// or node_modules/.bin/react-native, in e's folder.
func (e *runEnv) writeProject(t *testing.T, packageJSON string, others ...string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(e.dir, "package.json"), []byte(packageJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range others {
		path := filepath.Join(e.dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRunRunsAReactNativeProjectWithItsOwnCLI(t *testing.T) {
	e := newRunEnv(t)
	e.writeProject(t, `{"dependencies": {"react-native": "0.81.0"}}`, "node_modules/.bin/react-native")
	bin := filepath.Join(e.dir, "node_modules", ".bin", "react-native")
	if code := e.run("run", "iPhone 16 Pro", "--", "--scheme", "Dev"); code != ExitOK {
		t.Fatalf("code %d, stderr %q", code, e.errs.String())
	}
	if len(e.ran) != 1 || strings.Join(e.ran[0], " ") != bin+" run-ios --udid "+runningSim+" --scheme Dev" {
		t.Errorf("ran %v", e.ran)
	}
}

func TestRunOffersThePackageScriptsThatRunTheApp(t *testing.T) {
	e := newRunEnv(t)
	e.writeProject(t, `{"scripts": {
		"ios": "APP_ENV=dev expo run:ios",
		"ios:release": "pnpm prebuild && expo run:ios --configuration Release",
		"ios:mine": "expo run:ios --device 'My iPhone'",
		"android": "expo run:android",
		"start": "expo start",
		"test": "jest"
	}, "dependencies": {"expo": "~54.0.0"}}`, "pnpm-lock.yaml")
	asked := e.picks(1)
	// The device is an iPhone simulator, so the android script is not offered.
	if code := e.run("run", "iPhone 16 Pro", "--", "--no-bundler"); code != ExitOK {
		t.Fatalf("code %d, stderr %q", code, e.errs.String())
	}
	if want := []string{"How to run it?", "pnpm run ios", "pnpm run ios:release"}; len(*asked) != 1 || strings.Join((*asked)[0], "|") != strings.Join(want, "|") {
		t.Errorf("asked %q, want %q", *asked, want)
	}
	if len(e.ran) != 1 || strings.Join(e.ran[0], " ") != "pnpm run ios:release --device "+runningSim+" --no-bundler" {
		t.Errorf("ran %v", e.ran)
	}
	// A script runs whatever it says, so its text is shown, not only its name.
	if !strings.Contains(e.errs.String(), "The script is `pnpm prebuild && expo run:ios --configuration Release`") {
		t.Errorf("the script was not shown: %q", e.errs.String())
	}
	// The choice is remembered for the folder, and needs no --yes: it is the project's own script.
	if code := e.run("run", "iPhone 16 Pro"); code != ExitOK || len(e.ran) != 2 || e.ran[1][2] != "ios:release" || len(*asked) != 1 {
		t.Errorf("second run: code %d, ran %v, asked %v", code, e.ran, *asked)
	}
}

func TestAProjectWithOnlyAnIOSScriptRunsOnAndroidWithItsOwnTool(t *testing.T) {
	e := newRunEnv(t)
	e.writeProject(t, `{"scripts": {"ios": "expo run:ios"}, "dependencies": {"expo": "~54.0.0"}}`, "node_modules/.bin/expo")
	emulator := device.Device{ID: "Pixel_9a", Name: "Pixel 9a", Serial: "emulator-5554", Platform: device.Android, Kind: device.Emulator, State: device.Booted}
	plans, err := e.app.runPlans(context.Background(), e.dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range plans {
		names = append(names, p.name+" "+string(p.platform))
	}
	if strings.Join(names, ", ") != "npm run ios ios, expo run android" {
		t.Fatalf("plans: %v", names)
	}
	// The ios script never runs on an Android device, even when it is chosen.
	if _, err := plans[0].argv(emulator); apperr.CodeOf(err) != apperr.Unsupported {
		t.Errorf("the ios script on an emulator: %v", err)
	}
	kept := plansFor(plans, device.Android)
	if len(kept) != 1 {
		t.Fatalf("for Android: %d plans", len(kept))
	}
	argv, err := kept[0].argv(emulator)
	if want := filepath.Join(e.dir, "node_modules", ".bin", "expo") + " run:android --device Pixel_9a"; err != nil || strings.Join(argv, " ") != want {
		t.Errorf("%q, %v; want %s", argv, err, want)
	}
}

func TestRunGivesNpmTheDeviceAfterDashes(t *testing.T) {
	e := newRunEnv(t)
	e.writeProject(t, `{"scripts": {"dev": "flutter run --flavor dev"}}`)
	if code := e.run("run", "iPhone 16 Pro"); code != ExitOK || len(e.ran) != 1 || strings.Join(e.ran[0], " ") != "npm run dev -- -d "+runningSim {
		t.Errorf("code %d, ran %v, stderr %q", code, e.ran, e.errs.String())
	}
}

func TestScriptTool(t *testing.T) {
	for script, want := range map[string]string{
		"expo run:ios":                           "expo ios",
		"npx expo run:android --variant debug":   "expo android",
		"react-native run-ios --scheme Dev":      "react-native ios",
		"yarn build && react-native run-android": "react-native android",
		"flutter run -t lib/main_dev.dart":       "flutter ",
		"expo start":                             "",
		"expo run:ios && echo done":              "",
		"expo run:ios --device 'My iPhone'":      "",
		"flutter run -d emulator-5554":           "",
		"react-native run-ios --udid=ABC":        "",
		"expo run:ios | tee build.log":           "",
		"jest":                                   "",
	} {
		tool, platform, ok := scriptTool(script)
		got := ""
		if ok {
			got = tool + " " + string(platform)
		}
		if got != want {
			t.Errorf("%q: %q, want %q", script, got, want)
		}
	}
}

func TestPackageManager(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "apps", "shop")
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := packageManager(app, packageJSON{}); got != "npm" {
		t.Errorf("no lock file: %s", got)
	}
	if err := os.WriteFile(filepath.Join(root, "yarn.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := packageManager(app, packageJSON{}); got != "yarn" {
		t.Errorf("a lock file at the top of a monorepo: %s", got)
	}
	if got := packageManager(app, packageJSON{PackageManager: "pnpm@9.12.0"}); got != "pnpm" {
		t.Errorf("the packageManager field: %s", got)
	}
}

func TestRunTellsADashboardInTheFolderWhereItRuns(t *testing.T) {
	e := newRunEnv(t, "pubspec.yaml")
	if code := e.run("run", "iPhone 16 Pro"); code != ExitOK {
		t.Fatalf("code %d, stderr %q", code, e.errs.String())
	}
	got := e.app.State.Load().Runs[e.dir]
	if got.Last != "ios:simulator:"+runningSim || got.Device != got.Last || got.Started.IsZero() {
		t.Errorf("remembered %+v", got)
	}
	if !strings.Contains(e.errs.String(), "add --here to run it inside lazymobile, or run lazymobile in another terminal in this folder (or add --split). It opens on iPhone 16 Pro") {
		t.Errorf("the other terminal is not told about: %q", e.errs.String())
	}

	// A phone is followed by a dashboard, but is not the device used next time.
	answer := e.fake.Answer
	e.fake.Answer = func(command string, argv []string, opts run.Options) run.Result {
		if strings.Contains(command, "devicectl list devices") {
			if err := os.WriteFile(argv[len(argv)-1], []byte(phoneList), 0o600); err != nil {
				t.Error(err)
			}
			return run.Result{}
		}
		return answer(command, argv, opts)
	}
	if code := e.run("run", "Someone's iPhone"); code != ExitOK {
		t.Fatalf("code %d, stderr %q", code, e.errs.String())
	}
	got = e.app.State.Load().Runs[e.dir]
	if got.Last != "ios:physical:00008000-000000000000001E" || got.Device != "ios:simulator:"+runningSim {
		t.Errorf("after the phone: %+v", got)
	}
}

// profiles gives the app's folder three ways to run it, as a .lazymobile.json would.
func (e *runEnv) profiles(names ...string) {
	e.app.Actions = func() ([]actions.Action, error) {
		var all []actions.Action
		for _, name := range names {
			all = append(all, actions.Action{Name: name, Command: []string{"make", strings.ReplaceAll(name, " ", "-"), "DEVICE={device_id}"}, Dir: e.dir})
		}
		return all, nil
	}
}

// picks makes the app a terminal that answers each question with the next of answers, and records
// the titles and the lines it was asked about.
func (e *runEnv) picks(answers ...int) *[][]string {
	asked := &[][]string{}
	e.app.Terminal = true
	e.app.Pick = func(_ context.Context, title string, items []PickItem, _ int) (int, error) {
		if len(answers) == 0 {
			return 0, ErrCancelled
		}
		line := []string{title}
		for _, item := range items {
			line = append(line, item.Label)
		}
		*asked = append(*asked, line)
		answer := answers[0]
		answers = answers[1:]
		return answer, nil
	}
	return asked
}

func TestRunAsksWhichProfileOnceAndRemembersIt(t *testing.T) {
	e := newRunEnv(t)
	e.profiles("run dev", "run prod")
	asked := e.picks(1)
	if code := e.run("run", "iPhone 16 Pro", "--yes"); code != ExitOK || len(e.ran) != 1 || e.ran[0][1] != "run-prod" {
		t.Fatalf("code %d, ran %v, stderr %q", code, e.ran, e.errs.String())
	}
	if len(*asked) != 1 || (*asked)[0][0] != "How to run it?" || len((*asked)[0]) != 3 {
		t.Errorf("asked %v", *asked)
	}
	// The second run in the folder does not ask again, and uses the same profile.
	if code := e.run("run", "iPhone 16 Pro", "--yes"); code != ExitOK || len(e.ran) != 2 || e.ran[1][1] != "run-prod" || len(*asked) != 1 {
		t.Errorf("second run: ran %v, asked %v", e.ran, *asked)
	}
	// --choose asks again.
	e.picks(0)
	if code := e.run("run", "iPhone 16 Pro", "--yes", "--choose"); code != ExitOK || e.ran[2][1] != "run-dev" {
		t.Errorf("--choose: ran %v, stderr %q", e.ran, e.errs.String())
	}
}

func TestRunProfileNamesOneWithoutAsking(t *testing.T) {
	e := newRunEnv(t)
	e.profiles("run dev", "run prod")
	asked := e.picks()
	if code := e.run("run", "iPhone 16 Pro", "--yes", "--profile", "run dev"); code != ExitOK || e.ran[0][1] != "run-dev" || len(*asked) != 0 {
		t.Errorf("code %d, ran %v, asked %v", code, e.ran, *asked)
	}
	if code := e.run("run", "iPhone 16 Pro", "--yes", "--profile", "nope"); code != ExitUsage || !strings.Contains(e.errs.String(), "run dev, run prod") {
		t.Errorf("code %d, stderr %q", code, e.errs.String())
	}
}

func TestRunWithSeveralProfilesAndNoTerminalNeedsOneToBeChosen(t *testing.T) {
	e := newRunEnv(t)
	e.profiles("run dev", "run prod")
	if code := e.run("run", "iPhone 16 Pro", "--yes"); code != ExitUsage || len(e.ran) != 0 || !strings.Contains(e.errs.String(), "--profile") {
		t.Errorf("code %d, ran %v, stderr %q", code, e.ran, e.errs.String())
	}
	// A plain "run" is what a project with one has always used, so it still wins without a terminal.
	e.profiles("run", "run prod")
	if code := e.run("run", "iPhone 16 Pro", "--yes"); code != ExitOK || e.ran[0][1] != "run" {
		t.Errorf("code %d, ran %v, stderr %q", code, e.ran, e.errs.String())
	}
}

func TestRunProfilesInTheFileWinOverTheBuiltInTool(t *testing.T) {
	e := newRunEnv(t, "pubspec.yaml")
	e.profiles("run")
	asked := e.picks()
	if code := e.run("run", "iPhone 16 Pro", "--yes"); code != ExitOK || e.ran[0][0] != "make" || len(*asked) != 0 {
		t.Errorf("code %d, ran %v, asked %v", code, e.ran, *asked)
	}
}

func TestRunAsksWhichDeviceWhenSeveralAreRunningAndRemembersIt(t *testing.T) {
	e := newRunEnv(t, "pubspec.yaml")
	asked := e.picks(0)
	// The fake machine has one running simulator, so also ask with --choose, which always lists them.
	if code := e.run("run", "--choose"); code != ExitOK || len(e.ran) != 1 {
		t.Fatalf("code %d, ran %v, stderr %q", code, e.ran, e.errs.String())
	}
	if len(*asked) != 1 || (*asked)[0][0] != "Run on which device?" || len((*asked)[0]) < 3 {
		t.Fatalf("asked %v", *asked)
	}
	if !strings.Contains(e.ran[0][3], "-") {
		t.Errorf("ran %v", e.ran[0])
	}
	// What was chosen is used next time without asking.
	again := e.picks()
	if code := e.run("run"); code != ExitOK || len(*again) != 0 || e.ran[1][3] != e.ran[0][3] {
		t.Errorf("second run: ran %v, asked %v", e.ran, *again)
	}
}

func TestRunStopsWhenTheUserCancelsAList(t *testing.T) {
	e := newRunEnv(t, "pubspec.yaml")
	e.picks()
	if code := e.run("run", "--choose"); code == ExitOK || len(e.ran) != 0 || e.fake.Called("simctl boot") {
		t.Errorf("code %d, ran %v", code, e.ran)
	}
}

func TestRunChooseNeedsATerminal(t *testing.T) {
	e := newRunEnv(t, "pubspec.yaml")
	if code := e.run("run", "--choose"); code != ExitUsage || len(e.ran) != 0 {
		t.Errorf("code %d, ran %v", code, e.ran)
	}
}

func TestRunListsOnlyActionsNamedAsWaysToRun(t *testing.T) {
	e := newRunEnv(t)
	// run-tests is how a project names its tests for the R key, not a way to run the app.
	e.profiles("run-tests", "run dev")
	asked := e.picks()
	if code := e.run("run", "iPhone 16 Pro", "--yes"); code != ExitOK || len(*asked) != 0 || e.ran[0][1] != "run-dev" {
		t.Errorf("code %d, ran %v, asked %v, stderr %q", code, e.ran, *asked, e.errs.String())
	}
	for _, name := range []string{"run", "Run", "run dev", "run:prod"} {
		if !runProfile(name) {
			t.Errorf("%q is a way to run", name)
		}
	}
	for _, name := range []string{"run-tests", "runner", "rerun", "run_e2e"} {
		if runProfile(name) {
			t.Errorf("%q is not a way to run", name)
		}
	}
}

func TestRunHereOpensTheDashboardWithTheToolInsteadOfRunningIt(t *testing.T) {
	e := newRunEnv(t, "pubspec.yaml")
	var got []HereRun
	e.app.Terminal = true
	e.app.Here = func(_ context.Context, _ *mobile.Manager, run HereRun) error {
		got = append(got, run)
		return nil
	}
	if code := e.run("run", "iPhone 16", "--here", "--", "--flavor", "dev"); code != ExitOK {
		t.Fatalf("code %d, stderr %q", code, e.errs.String())
	}
	if len(e.ran) != 0 || len(got) != 1 {
		t.Fatalf("ran %v, here %v", e.ran, got)
	}
	run := got[0]
	if strings.Join(run.Argv, " ") != "flutter run -d "+stoppedSim+" --flavor dev" || run.Dir != e.dir || run.Folder != e.dir ||
		run.Title != "flutter run" || run.Device.ID != stoppedSim {
		t.Errorf("here got %+v", run)
	}
	// The device was started first, as without --here, and the run is recorded for the dashboard to follow.
	if !e.fake.Called("simctl boot " + stoppedSim) {
		t.Error("the device was not started")
	}
	if r := e.app.State.Load().Runs[e.dir]; r.Last == "" || r.Started.IsZero() {
		t.Errorf("the run was not recorded: %+v", r)
	}
}

func TestRunHereNamesAPlainRunActionByItsCommand(t *testing.T) {
	e := newRunEnv(t)
	e.profiles("run")
	var title string
	e.app.Terminal = true
	e.app.Here = func(_ context.Context, _ *mobile.Manager, run HereRun) error { title = run.Title; return nil }
	if code := e.run("run", "iPhone 16 Pro", "--yes", "--here"); code != ExitOK || title != "make" {
		t.Errorf("code %d, title %q, stderr %q", code, title, e.errs.String())
	}
}

func TestRunHereNeedsATerminalAndIsNotSplit(t *testing.T) {
	e := newRunEnv(t, "pubspec.yaml")
	e.app.Here = func(context.Context, *mobile.Manager, HereRun) error { t.Error("the dashboard opened"); return nil }
	if code := e.run("run", "iPhone 16 Pro", "--here"); code != ExitUsage || !strings.Contains(e.errs.String(), "needs a terminal") {
		t.Errorf("without a terminal: code %d, stderr %q", code, e.errs.String())
	}
	e.app.Terminal = true
	if code := e.run("run", "iPhone 16 Pro", "--here", "--split"); code != ExitUsage || !strings.Contains(e.errs.String(), "use one") {
		t.Errorf("with --split: code %d, stderr %q", code, e.errs.String())
	}
	if len(e.ran) != 0 || e.fake.Called("simctl boot") {
		t.Errorf("something ran: %v", e.ran)
	}
}

func TestRunHereNoLogIsPassedOnAndNeedsHere(t *testing.T) {
	e := newRunEnv(t, "pubspec.yaml")
	var got []HereRun
	e.app.Terminal = true
	e.app.Here = func(_ context.Context, _ *mobile.Manager, run HereRun) error {
		got = append(got, run)
		return nil
	}
	if code := e.run("run", "iPhone 16", "--here", "--no-log"); code != ExitOK || len(got) != 1 || !got[0].NoLog {
		t.Fatalf("code %d, stderr %q, here %+v", code, e.errs.String(), got)
	}
	got = nil
	if code := e.run("run", "iPhone 16", "--here"); code != ExitOK || len(got) != 1 || got[0].NoLog {
		t.Errorf("without --no-log: code %d, here %+v", code, got)
	}
	// Without --here there is no log beside the tool to hide, and nothing is booted or run.
	before := len(e.ran)
	if code := e.run("run", "iPhone 16", "--no-log"); code != ExitUsage || !strings.Contains(e.errs.String(), "add --here") {
		t.Errorf("--no-log alone: code %d, stderr %q", code, e.errs.String())
	}
	if len(e.ran) != before {
		t.Errorf("something ran: %v", e.ran)
	}
}

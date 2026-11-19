package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0xrnp/lazymobile/internal/actions"
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
		!strings.Contains(e.errs.String(), "Flutter and Expo") || !strings.Contains(e.errs.String(), ".lazymobile.json") {
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

func TestRunDoesNotDownloadExpoWhenTheProjectHasNone(t *testing.T) {
	e := newRunEnv(t)
	expoProject(t, e, false)
	if code := e.run("run", "iPhone 16 Pro"); code != ExitFailure || len(e.ran) != 0 || !strings.Contains(e.errs.String(), "expo is not installed") {
		t.Errorf("code %d, ran %v, stderr %q", code, e.ran, e.errs.String())
	}
}

func TestAPackageWithoutExpoIsNotRunAsOne(t *testing.T) {
	e := newRunEnv(t, "package.json")
	if err := os.WriteFile(filepath.Join(e.dir, "package.json"), []byte(`{"dependencies": {"react-native": "0.81.0"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := e.run("run", "iPhone 16 Pro"); code != ExitFailure || len(e.ran) != 0 || !strings.Contains(e.errs.String(), "Cannot tell") {
		t.Errorf("code %d, ran %v, stderr %q", code, e.ran, e.errs.String())
	}
}

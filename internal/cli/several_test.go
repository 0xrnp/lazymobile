package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0xrnp/lazymobile/internal/devicesets"
	"github.com/0xrnp/lazymobile/internal/run"
	"github.com/0xrnp/lazymobile/internal/run/runtest"
)

func TestBootSeveralBootsTheStoppedOnesAndSaysWhatItSkipped(t *testing.T) {
	e := newEnv(t)
	code := e.run("boot", "--all", "iPhone*")
	if code != ExitOK {
		t.Fatalf("code %d, stderr %q", code, e.errs.String())
	}
	if !e.fake.Called("simctl boot 00000000-0000-4000-8000-0000000000A1") || e.fake.Called("simctl boot 00000000-0000-4000-8000-0000000000A2") {
		t.Errorf("ran %v", e.fake.Calls())
	}
	out := e.out.String()
	if !strings.Contains(out, "ok       iPhone 16: is starting") || !strings.Contains(out, "skipped  iPhone 16 Pro: already running") {
		t.Errorf("output:\n%s", out)
	}
}

func TestBootSeveralNeedsAPatternThatMatches(t *testing.T) {
	e := newEnv(t)
	if code := e.run("boot", "--all"); code != ExitUsage {
		t.Errorf("no pattern: code %d, stderr %q", code, e.errs.String())
	}
	if code := e.run("boot", "--all", "Galaxy*"); code != ExitFailure || !strings.Contains(e.errs.String(), `No device matches "Galaxy*"`) {
		t.Errorf("no match: code %d, stderr %q", code, e.errs.String())
	}
	if code := e.run("boot", "--all", "iPhone*", "--wipe"); code != ExitUsage {
		t.Errorf("--wipe: code %d", code)
	}
	if code := e.run("boot", "--all", "--set", "x", "iPhone*"); code != ExitUsage {
		t.Errorf("--all with --set: code %d", code)
	}
	if e.fake.Called("simctl boot") {
		t.Errorf("something booted: %v", e.fake.Calls())
	}
}

func TestBootSeveralFailsWhenADeviceFails(t *testing.T) {
	e := newEnv(t)
	inner := e.fake.Answer
	e.fake.Answer = func(command string, argv []string, o run.Options) run.Result {
		if strings.Contains(command, "simctl boot") {
			return runtest.Fail(1, "Unable to boot device")
		}
		return inner(command, argv, o)
	}
	if code := e.run("boot", "--all", "iPhone 16", "--json"); code != ExitFailure {
		t.Fatalf("code %d, stderr %q", code, e.errs.String())
	}
	var got struct {
		OK      bool      `json:"ok"`
		Results []outcome `json:"results"`
	}
	if err := json.Unmarshal(e.out.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v: %q", err, e.out.String())
	}
	if got.OK || len(got.Results) != 1 || got.Results[0].Status != outcomeFailed || got.Results[0].Error == nil {
		t.Errorf("got %+v", got)
	}
}

func TestBootSetUsesTheNamedDevices(t *testing.T) {
	e := newEnv(t)
	e.app.DeviceSets = func() (devicesets.Sets, error) {
		return devicesets.Sets{"matrix": {"iPhone 16"}}, nil
	}
	if code := e.run("boot", "--set", "matrix"); code != ExitOK || !e.fake.Called("simctl boot 00000000-0000-4000-8000-0000000000A1") {
		t.Errorf("code %d, ran %v, stderr %q", code, e.fake.Calls(), e.errs.String())
	}
	if code := e.run("boot", "--set", "nope"); code != ExitFailure || !strings.Contains(e.errs.String(), `No device set is named "nope"`) {
		t.Errorf("unknown set: code %d, stderr %q", code, e.errs.String())
	}
	if code := e.run("boot", "--set", "matrix", "iPhone 16 Pro"); code != ExitUsage {
		t.Errorf("--set with a name: code %d", code)
	}
}

func TestInstallSeveralGoesToRunningDevicesThatCanTakeTheBuild(t *testing.T) {
	e := newEnv(t)
	build := filepath.Join(t.TempDir(), "Sample.app")
	if err := os.Mkdir(build, 0o755); err != nil {
		t.Fatal(err)
	}
	if code := e.run("install", "--all", build); code != ExitOK {
		t.Fatalf("code %d, stderr %q", code, e.errs.String())
	}
	// Only the booted simulator takes it: the other is stopped, and nothing else is running.
	if !e.fake.Called("simctl install 00000000-0000-4000-8000-0000000000A2") || e.fake.Called("simctl install 00000000-0000-4000-8000-0000000000A1") {
		t.Errorf("ran %v", e.fake.Calls())
	}
	if !strings.Contains(e.out.String(), "ok       iPhone 16 Pro: installed") {
		t.Errorf("output %q", e.out.String())
	}

	apk := filepath.Join(t.TempDir(), "app.apk")
	if err := os.WriteFile(apk, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := e.run("install", "--all", apk); code != ExitFailure || !strings.Contains(e.errs.String(), "No running device can take") {
		t.Errorf("an apk with no Android device running: code %d, stderr %q", code, e.errs.String())
	}
	if code := e.run("install", "--phones", build, "iPhone 16 Pro"); code != ExitUsage {
		t.Errorf("--phones alone: code %d", code)
	}
}

func TestInstallSetReportsMembersItSkips(t *testing.T) {
	e := newEnv(t)
	e.app.DeviceSets = func() (devicesets.Sets, error) {
		return devicesets.Sets{"matrix": {"iPhone 16", "iPhone 16 Pro"}}, nil
	}
	build := filepath.Join(t.TempDir(), "Sample.app")
	if err := os.Mkdir(build, 0o755); err != nil {
		t.Fatal(err)
	}
	if code := e.run("install", "--set", "matrix", build); code != ExitOK {
		t.Fatalf("code %d, stderr %q", code, e.errs.String())
	}
	if !strings.Contains(e.out.String(), "skipped  iPhone 16: not running") || !strings.Contains(e.out.String(), "ok       iPhone 16 Pro: installed") {
		t.Errorf("output:\n%s", e.out.String())
	}
}

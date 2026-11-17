package doctor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/0xrnp/lazymobile/internal/android"
	"github.com/0xrnp/lazymobile/internal/run"
	"github.com/0xrnp/lazymobile/internal/run/runtest"
)

func bare(t *testing.T) android.Options {
	t.Helper()
	return android.Options{
		OS: "linux", Home: t.TempDir(), Getenv: func(string) string { return "" },
		LookPath:     func(string) (string, error) { return "", errors.New("not found") },
		SDKFallbacks: []string{}, JavaFallbacks: []string{},
	}
}

func codes(report Report) map[string]Severity {
	out := map[string]Severity{}
	for _, p := range report.Problems {
		out[p.Code] = p.Severity
	}
	return out
}

func TestIOSChecks(t *testing.T) {
	const dir = "/Applications/Xcode.app/Contents/Developer\n"
	tests := []struct {
		name    string
		answers map[string]run.Result
		want    map[string]Severity
		found   bool
	}{
		{"no Xcode", map[string]run.Result{"xcode-select": runtest.Fail(2, "no developer tools")},
			map[string]Severity{"xcode_missing": Unavailable}, false},
		{"only the command line tools", map[string]run.Result{"xcode-select": runtest.Out("/Library/Developer/CommandLineTools\n")},
			map[string]Severity{"xcode_clt_only": Unavailable}, false},
		{"complete", map[string]run.Result{
			"xcode-select": runtest.Out(dir), "--find simctl": {}, "--find devicectl": {}, "xcodebuild": runtest.Out("Xcode 27.2\nBuild version 1\n"),
		}, map[string]Severity{}, true},
		{"before Xcode 15", map[string]run.Result{
			"xcode-select": runtest.Out(dir), "--find simctl": {}, "--find devicectl": runtest.Fail(72, "not found"), "xcodebuild": runtest.Out("Xcode 14.3\n"),
		}, map[string]Severity{"devicectl_missing": Optional}, true},
		{"license not accepted", map[string]run.Result{
			"xcode-select": runtest.Out(dir), "--find simctl": {}, "--find devicectl": {},
			"xcodebuild": runtest.Fail(69, "You have not agreed to the Xcode license agreements."),
		}, map[string]Severity{"xcode_license": Unavailable}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var report Report
			xcode(context.Background(), runtest.Table(tt.answers), &report)
			got := codes(report)
			if len(got) != len(tt.want) {
				t.Errorf("problems = %v, want %v", got, tt.want)
			}
			for code, severity := range tt.want {
				if got[code] != severity {
					t.Errorf("%s = %q, want %q", code, got[code], severity)
				}
			}
			if (len(report.Found) > 0) != tt.found {
				t.Errorf("found = %v", report.Found)
			}
		})
	}
}

func TestAndroidChecks(t *testing.T) {
	t.Run("nothing installed", func(t *testing.T) {
		report := Check(context.Background(), &runtest.Fake{}, bare(t), "linux")
		if got := codes(report); got["android_sdk_missing"] != Unavailable || len(got) != 1 {
			t.Errorf("problems = %v", got)
		}
		if !report.Healthy() {
			t.Error("a machine without the SDK is not set up wrongly")
		}
	})

	t.Run("a setting that points nowhere", func(t *testing.T) {
		opts := bare(t)
		opts.Getenv = func(key string) string {
			if key == "ANDROID_HOME" {
				return "/no/such/sdk"
			}
			return ""
		}
		report := Check(context.Background(), &runtest.Fake{}, opts, "linux")
		if codes(report)["android_sdk_invalid"] != Wrong || report.Healthy() {
			t.Errorf("problems = %v", codes(report))
		}
	})

	t.Run("an SDK with only adb", func(t *testing.T) {
		opts := bare(t)
		opts.SDK = filepath.Join(t.TempDir(), "sdk")
		adb := filepath.Join(opts.SDK, "platform-tools", "adb")
		if err := os.MkdirAll(filepath.Dir(adb), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(adb, nil, 0o755); err != nil {
			t.Fatal(err)
		}
		report := Check(context.Background(), &runtest.Fake{}, opts, "linux")
		got := codes(report)
		if got["android_emulator_missing"] != Unavailable || got["android_avdmanager_missing"] != Optional || len(got) != 2 {
			t.Errorf("problems = %v", got)
		}
	})

	t.Run("iOS is not checked off macOS", func(t *testing.T) {
		fake := &runtest.Fake{}
		Check(context.Background(), fake, bare(t), "linux")
		if fake.Called("xcode-select") {
			t.Error("looked for Xcode on Linux")
		}
	})
}

type fakeEAS struct {
	configured []string
	available  bool
	loggedIn   bool
	err        error
	asked      int
}

func (f *fakeEAS) ConfiguredCommand() []string { return f.configured }
func (f *fakeEAS) Available() bool             { return f.available }
func (f *fakeEAS) LoginCommand() []string      { return []string{"eas", "login"} }
func (f *fakeEAS) LoggedIn(context.Context) (bool, error) {
	f.asked++
	return f.loggedIn, f.err
}

func has(programs ...string) func(string) (string, error) {
	return func(name string) (string, error) {
		for _, p := range programs {
			if p == name {
				return "/bin/" + name, nil
			}
		}
		return "", errors.New("not found")
	}
}

func TestExtraChecks(t *testing.T) {
	cases := []struct {
		name  string
		extra Extra
		eas   *fakeEAS
		want  map[string]Severity
		fix   map[string][]string
		asked int
	}{
		{"nothing connected, no project", Extra{}, nil, map[string]Severity{}, nil, 0},
		{"iPhone without idevicesyslog", Extra{IPhoneConnected: true, LookPath: has("brew")}, nil,
			map[string]Severity{"idevicesyslog_missing": Optional},
			map[string][]string{"idevicesyslog_missing": {"brew", "install", "libimobiledevice"}}, 0},
		{"iPhone without idevicesyslog or brew", Extra{IPhoneConnected: true, LookPath: has()}, nil,
			map[string]Severity{"idevicesyslog_missing": Optional}, map[string][]string{"idevicesyslog_missing": nil}, 0},
		{"iPhone with idevicesyslog", Extra{IPhoneConnected: true, LookPath: has("idevicesyslog")}, nil, map[string]Severity{}, nil, 0},
		{"no eas", Extra{LookPath: has("npm")}, &fakeEAS{},
			map[string]Severity{"eas_missing": Optional},
			map[string][]string{"eas_missing": {"npm", "install", "--global", "eas-cli"}}, 0},
		{"not logged in", Extra{}, &fakeEAS{available: true},
			map[string]Severity{"eas_not_logged_in": Optional},
			map[string][]string{"eas_not_logged_in": {"eas", "login"}}, 1},
		{"logged in", Extra{}, &fakeEAS{available: true, loggedIn: true}, map[string]Severity{}, nil, 1},
		{"could not check", Extra{}, &fakeEAS{available: true, err: errors.New("offline")},
			map[string]Severity{"eas_check_failed": Optional}, nil, 1},
		{"a command from .lazymobile.json is never run", Extra{}, &fakeEAS{available: true, configured: []string{"sh", "-c", "x"}},
			map[string]Severity{}, nil, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.eas != nil {
				c.extra.EAS = c.eas
			}
			report := CheckExtra(context.Background(), c.extra)
			got := codes(report)
			if len(got) != len(c.want) {
				t.Fatalf("problems = %v, want %v", got, c.want)
			}
			for code, severity := range c.want {
				if got[code] != severity {
					t.Errorf("%s = %q, want %q", code, got[code], severity)
				}
			}
			for _, p := range report.Problems {
				if want, listed := c.fix[p.Code]; listed && !reflect.DeepEqual(p.Fix, want) {
					t.Errorf("%s fix = %v, want %v", p.Code, p.Fix, want)
				}
			}
			if c.eas != nil && c.eas.asked != c.asked {
				t.Errorf("eas asked %d times, want %d", c.eas.asked, c.asked)
			}
		})
	}
}

func TestFixesAreOnlyOfferedWhenOneCommandDoesIt(t *testing.T) {
	old := xcodeApp
	defer func() { xcodeApp = old }()

	xcodeApp = t.TempDir()
	var report Report
	xcode(context.Background(), runtest.Table(map[string]run.Result{"xcode-select": runtest.Out("/Library/Developer/CommandLineTools\n")}), &report)
	want := []string{"sudo", "xcode-select", "--switch", xcodeApp + "/Contents/Developer"}
	if len(report.Problems) != 1 || !reflect.DeepEqual(report.Problems[0].Fix, want) {
		t.Errorf("with Xcode installed: %+v", report.Problems)
	}

	xcodeApp = filepath.Join(t.TempDir(), "missing")
	report = Report{}
	xcode(context.Background(), runtest.Table(map[string]run.Result{"xcode-select": runtest.Out("/Library/Developer/CommandLineTools\n")}), &report)
	if len(report.Problems) != 1 || report.Problems[0].Fix != nil {
		t.Errorf("without Xcode there is nothing to switch to: %+v", report.Problems)
	}

	report = Report{}
	xcode(context.Background(), runtest.Table(map[string]run.Result{
		"xcode-select": runtest.Out("/Applications/Xcode.app/Contents/Developer\n"), "--find simctl": {}, "--find devicectl": {},
		"xcodebuild": runtest.Fail(69, "You have not agreed to the Xcode license agreements."),
	}), &report)
	if len(report.Problems) != 1 || !reflect.DeepEqual(report.Problems[0].Fix, []string{"sudo", "xcodebuild", "-license", "accept"}) {
		t.Errorf("license: %+v", report.Problems)
	}
}

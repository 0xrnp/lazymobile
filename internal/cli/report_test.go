package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0xrnp/lazymobile/internal/run"
	"github.com/0xrnp/lazymobile/internal/run/runtest"
)

// fakeXcrun puts an xcrun on PATH that prints these log lines, since a log is read straight from
// the tool and not through the runner.
func fakeXcrun(t *testing.T, lines ...string) {
	t.Helper()
	dir := t.TempDir()
	body := "#!/bin/sh\ncat <<'EOF'\n" + strings.Join(lines, "\n") + "\nEOF\n"
	if err := os.WriteFile(filepath.Join(dir, "xcrun"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// reportEnv answers the log and the executable name that a report asks a simulator for, and
// points the crash reports at a folder with one report in it.
func reportEnv(t *testing.T) *env {
	t.Helper()
	fakeXcrun(t,
		"2026-11-19 12:00:00.000 Df Example[1:1] first line",
		"2026-11-19 12:00:01.000 Df Example[1:1] second line",
	)
	e := newEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Example-2026-11-17-183134.ips"), []byte(simulatorCrash), 0o600); err != nil {
		t.Fatal(err)
	}
	e.app.Manager.IOS.CrashDir = dir
	answer := e.fake.Answer
	e.fake.Answer = func(command string, argv []string, opts run.Options) run.Result {
		switch {
		case strings.HasPrefix(command, "plutil -extract CFBundleExecutable"):
			return runtest.Out("Example\n")
		}
		return answer(command, argv, opts)
	}
	return e
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestReportSavesTheScreenshotLogCrashAndVersion(t *testing.T) {
	e := reportEnv(t)
	out := filepath.Join(t.TempDir(), "bug")
	if code := e.run("report", "iPhone 16 Pro", "com.example.app", "--out", out); code != ExitOK {
		t.Fatalf("code %d, stderr %q", code, e.errs.String())
	}
	for _, name := range []string{"screenshot.png", "log.txt", "crash.txt", "summary.txt"} {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Errorf("%s is missing: %v", name, err)
		}
	}
	if log := read(t, filepath.Join(out, "log.txt")); !strings.Contains(log, "first line") || !strings.Contains(log, "second line") {
		t.Errorf("log.txt = %q", log)
	}
	if crash := read(t, filepath.Join(out, "crash.txt")); !strings.Contains(crash, "EXC_BAD_ACCESS (SIGSEGV)") {
		t.Errorf("crash.txt = %q", crash)
	}
	summary := read(t, filepath.Join(out, "summary.txt"))
	for _, want := range []string{"lazymobile 1.2.3", "iPhone 16 Pro", "com.example.app", "personal data"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary.txt lacks %q: %q", want, summary)
		}
	}
	// com.example.app is not installed on the fake simulator, so its version is not known.
	if !strings.Contains(summary, "Not included, app version") {
		t.Errorf("a missing version is not said: %q", summary)
	}
	if info, err := os.Stat(out); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("the folder is not private: %v %v", info, err)
	}
	if !strings.Contains(e.out.String(), "Saved "+out) {
		t.Errorf("stdout = %q", e.out.String())
	}
}

func TestReportKeepsGoingWhenOnePartFails(t *testing.T) {
	e := reportEnv(t)
	answer := e.fake.Answer
	e.fake.Answer = func(command string, argv []string, opts run.Options) run.Result {
		if strings.Contains(command, "simctl io") {
			return runtest.Fail(1, "cannot take a screenshot")
		}
		return answer(command, argv, opts)
	}
	out := filepath.Join(t.TempDir(), "bug")
	if code := e.run("report", "iPhone 16 Pro", "com.example.installed", "--out", out, "--json"); code != ExitOK {
		t.Fatalf("code %d, stderr %q", code, e.errs.String())
	}
	var got struct {
		OK       bool     `json:"ok"`
		Path     string   `json:"path"`
		Warnings []string `json:"warnings"`
		Report   struct {
			Included []string `json:"included"`
		} `json:"report"`
	}
	if err := json.Unmarshal(e.out.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v: %q", err, e.out.String())
	}
	if !got.OK || got.Path != out || len(got.Warnings) < 2 {
		t.Errorf("got %+v", got)
	}
	skipped := strings.Join(got.Warnings, "\n")
	// The screenshot failed, and this app has no crash report. The log and the summary were saved.
	if !strings.Contains(skipped, "screenshot") || !strings.Contains(skipped, "crash report") {
		t.Errorf("skipped = %q", skipped)
	}
	if strings.Join(got.Report.Included, ",") != "log.txt,summary.txt" {
		t.Errorf("included = %v", got.Report.Included)
	}
	// com.example.installed is installed, so the summary names it.
	if summary := read(t, filepath.Join(out, "summary.txt")); !strings.Contains(summary, "Installed") {
		t.Errorf("summary.txt = %q", summary)
	}
}

func TestReportNeverUsesAFolderThatExists(t *testing.T) {
	e := reportEnv(t)
	out := t.TempDir()
	keep := filepath.Join(out, "keep.txt")
	if err := os.WriteFile(keep, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := e.run("report", "iPhone 16 Pro", "com.example.app", "--out", out); code != ExitFailure ||
		!strings.Contains(e.errs.String(), "already exists") || read(t, keep) != "mine" {
		t.Errorf("code %d, stderr %q", code, e.errs.String())
	}
	if e.fake.Called("simctl io") {
		t.Error("a screenshot was taken for a folder that was refused")
	}
}

func TestReportRefusesWhatItCannotDo(t *testing.T) {
	e := reportEnv(t)
	for name, args := range map[string][]string{
		"not running":     {"report", "iPhone 16", "com.example.app", "--out", filepath.Join(t.TempDir(), "a")},
		"bad app id":      {"report", "iPhone 16 Pro", "not an id; rm", "--out", filepath.Join(t.TempDir(), "b")},
		"no app":          {"report", "iPhone 16 Pro"},
		"negative lines":  {"report", "iPhone 16 Pro", "com.example.app", "--lines", "-1"},
		"negative window": {"report", "iPhone 16 Pro", "com.example.app", "--since", "-1m"},
	} {
		code := e.run(args...)
		if code == ExitOK {
			t.Errorf("%s: it was accepted", name)
		}
	}
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
	out := filepath.Join(t.TempDir(), "phone")
	if code := e.run("report", "Someone's iPhone", "com.example.app", "--out", out); code != ExitFailure ||
		!strings.Contains(e.errs.String(), "iPhone") {
		t.Errorf("code %d, stderr %q", code, e.errs.String())
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("a folder was made for an iPhone")
	}
}

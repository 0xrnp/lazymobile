//go:build unix

package logs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/run"
)

var (
	emulator  = device.Device{ID: "Pixel_9", Name: "Pixel 9", Platform: device.Android, Kind: device.Emulator, State: device.Booted, Serial: "emulator-5554"}
	simulator = device.Device{ID: "SIM-UDID", Name: "iPhone 16", Platform: device.IOS, Kind: device.Simulator, State: device.Booted}
	iphone    = device.Device{ID: "PHONE-UDID", Name: "My iPhone", Platform: device.IOS, Kind: device.Physical, State: device.Connected}
)

// script writes an executable shell script and returns its path.
func script(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// collector gathers what a session reports.
type collector struct {
	mu     sync.Mutex
	lines  []Line
	status []string
}

func (c *collector) handlers() Handlers {
	return Handlers{
		Lines: func(lines []Line) {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.lines = append(c.lines, lines...)
		},
		Status: func(message string) {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.status = append(c.status, message)
		},
	}
}

func (c *collector) texts() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.lines))
	for i, line := range c.lines {
		out[i] = line.Text
	}
	return out
}

// waitFor polls until the collector has n lines.
func (c *collector) waitFor(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if len(c.texts()) >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("got %d lines after 10s, want %d: %v", len(c.texts()), n, c.texts())
}

func adbDeps(path string) Deps {
	return Deps{Runner: run.Exec{}, ADB: func() (string, error) { return path, nil }}
}

const fakeLogcat = `case "$*" in
  *pidof*) echo 100 ;;
  *logcat*)
    echo "$@" > "$(dirname "$0")/logcat-args"
    echo "11-13 20:49:44.298   100   100 I App: first"
    echo "11-13 20:49:44.299   200   200 I Other: noise"
    echo "11-13 20:49:45.000   500   600 I ActivityManager: Start proc 300:com.example.app/u0a1 for activity"
    echo "11-13 20:49:45.100   300   300 E App: after restart"
    echo "11-13 20:49:45.200   400   400 E AndroidRuntime: Process: com.example.app, PID: 300"
    sleep 30 ;;
esac`

func TestAndroidStreamFollowsOneApp(t *testing.T) {
	dir := t.TempDir()
	adb := script(t, dir, "adb", fakeLogcat)
	var c collector
	session := Start(context.Background(), adbDeps(adb), Spec{Device: emulator, Mode: Logcat, App: "com.example.app", Tail: 50}, c.handlers())
	c.waitFor(t, 4)
	session.Stop()
	if err := session.Wait(); err != nil {
		t.Errorf("a stopped stream should end without an error, got %v", err)
	}

	got := c.texts()
	if len(got) != 4 {
		t.Fatalf("got %d lines, want 4: %v", len(got), got)
	}
	for _, line := range got {
		if strings.Contains(line, "noise") {
			t.Errorf("another app's line was kept: %q", line)
		}
	}
	args, err := os.ReadFile(filepath.Join(dir, "logcat-args"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "-s emulator-5554 logcat -v threadtime -T 50"; strings.TrimSpace(string(args)) != want {
		t.Errorf("logcat arguments = %q, want %q", strings.TrimSpace(string(args)), want)
	}
}

func TestAndroidStreamWithoutAnAppKeepsEverything(t *testing.T) {
	adb := script(t, t.TempDir(), "adb", fakeLogcat)
	var c collector
	session := Start(context.Background(), adbDeps(adb), Spec{Device: emulator, Mode: Logcat}, c.handlers())
	c.waitFor(t, 5)
	session.Stop()
	if err := session.Wait(); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lines[3].Level != Error || c.lines[3].PID != "300" {
		t.Errorf("lines should be parsed: %+v", c.lines[3])
	}
}

// Stopping must end the tool's whole process group at once, or adb would keep running.
func TestStopEndsTheStreamQuickly(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "child-survived")
	adb := script(t, dir, "adb", `echo started; (sleep 2; touch "`+marker+`") & wait`)
	var c collector
	session := Start(context.Background(), adbDeps(adb), Spec{Device: emulator, Mode: Logcat}, c.handlers())
	c.waitFor(t, 1)
	start := time.Now()
	session.Stop()
	session.Stop()
	if err := session.Wait(); err != nil {
		t.Errorf("error = %v", err)
	}
	if elapsed := time.Since(start); elapsed > 1500*time.Millisecond {
		t.Errorf("stopping took %v", elapsed)
	}
	time.Sleep(2200 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Error("a child of the log tool outlived the session")
	}
}

func TestStreamThatEnds(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"with a failure", `echo "one"; echo "error: device offline" >&2; exit 1`, "device offline"},
		{"cleanly", `echo "one"`, "The log stream ended"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adb := script(t, t.TempDir(), "adb", tt.body)
			var c collector
			err := Start(context.Background(), adbDeps(adb), Spec{Device: emulator, Mode: Logcat}, c.handlers()).Wait()
			if apperr.CodeOf(err) != apperr.CommandFailed || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want command_failed mentioning %q", err, tt.want)
			}
			if got := c.texts(); len(got) == 0 || got[0] != "one" {
				t.Errorf("lines before the end should still arrive: %v", got)
			}
		})
	}
}

func TestStartRefusesBadRequestsBeforeRunningAnything(t *testing.T) {
	ran := filepath.Join(t.TempDir(), "ran")
	adb := script(t, t.TempDir(), "adb", `touch "`+ran+`"`)
	stopped := emulator
	stopped.State = device.Shutdown

	tests := []struct {
		name string
		spec Spec
		want apperr.Code
	}{
		{"a device that is not running", Spec{Device: stopped, Mode: Logcat}, apperr.InvalidState},
		{"a mode the device does not have", Spec{Device: emulator, Mode: Console, App: "com.example.app"}, apperr.Unsupported},
		{"an app id with shell characters", Spec{Device: emulator, Mode: Logcat, App: "com.example; reboot"}, apperr.InvalidState},
		{"launching without an app", Spec{Device: simulator, Mode: Console}, apperr.InvalidState},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Start(context.Background(), adbDeps(adb), tt.spec, Handlers{}).Wait()
			if apperr.CodeOf(err) != tt.want {
				t.Errorf("error = %v, want code %q", err, tt.want)
			}
		})
	}
	if _, err := os.Stat(ran); err == nil {
		t.Error("a refused request still ran the tool")
	}
}

func TestMissingADBIsReported(t *testing.T) {
	missing := apperr.New(apperr.ToolMissing, "adb was not found")
	deps := Deps{Runner: run.Exec{}, ADB: func() (string, error) { return "", missing }}
	err := Start(context.Background(), deps, Spec{Device: emulator, Mode: Logcat}, Handlers{}).Wait()
	if !errors.Is(err, error(missing)) {
		t.Errorf("error = %v", err)
	}
}

type fakeApps struct {
	name string
	err  error
}

func (f fakeApps) AppExecutable(context.Context, device.Device, string) (string, error) {
	return f.name, f.err
}

// fakeXcrun puts an xcrun on PATH that records its arguments, prints one line and waits.
func fakeXcrun(t *testing.T) (argsFile string) {
	t.Helper()
	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args")
	script(t, dir, "xcrun", `printf '%s\n' "$@" > "`+argsFile+`"; echo "2026-11-13 20:56:36.411 Df Runner[30348:6e9c9] hello"; sleep 30`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return argsFile
}

func readArgs(t *testing.T, path string) []string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(content)), "\n")
}

func TestSimulatorSystemLogForOneApp(t *testing.T) {
	argsFile := fakeXcrun(t)
	var c collector
	deps := Deps{Runner: run.Exec{}, IOS: fakeApps{name: "Runner"}}
	session := Start(context.Background(), deps, Spec{Device: simulator, Mode: System, App: "com.example.app"}, c.handlers())
	c.waitFor(t, 1)
	session.Stop()
	if err := session.Wait(); err != nil {
		t.Fatal(err)
	}
	want := []string{"simctl", "spawn", "SIM-UDID", "log", "stream", "--style", "compact", "--level", "debug", "--predicate", `process == "Runner"`}
	if got := readArgs(t, argsFile); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("arguments = %q, want %q", got, want)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lines[0].Level != Info || c.lines[0].PID != "30348" {
		t.Errorf("line = %+v", c.lines[0])
	}
	if len(c.status) != 1 || !strings.Contains(c.status[0], `"Runner"`) {
		t.Errorf("status = %v", c.status)
	}
}

func TestSimulatorSystemLogRefusesAnUnusualProcessName(t *testing.T) {
	deps := Deps{Runner: run.Exec{}, IOS: fakeApps{name: `Run" OR 1 == 1 OR "`}}
	err := Start(context.Background(), deps, Spec{Device: simulator, Mode: System, App: "com.example.app"}, Handlers{}).Wait()
	if apperr.CodeOf(err) != apperr.Unsupported {
		t.Errorf("error = %v, want unsupported", err)
	}
}

func TestSimulatorConsoleLaunchesTheApp(t *testing.T) {
	argsFile := fakeXcrun(t)
	var c collector
	session := Start(context.Background(), Deps{Runner: run.Exec{}}, Spec{Device: simulator, Mode: Console, App: "com.example.app"}, c.handlers())
	c.waitFor(t, 1)
	session.Stop()
	if err := session.Wait(); err != nil {
		t.Fatal(err)
	}
	want := "simctl|launch|--console-pty|--terminate-running-process|SIM-UDID|com.example.app"
	if got := strings.Join(readArgs(t, argsFile), "|"); got != want {
		t.Errorf("arguments = %q, want %q", got, want)
	}
}

func TestPhoneSystemLogNeedsIdevicesyslog(t *testing.T) {
	deps := Deps{Runner: run.Exec{}, LookPath: func(string) (string, error) { return "", errors.New("not found") }}
	err := Start(context.Background(), deps, Spec{Device: iphone, Mode: System}, Handlers{}).Wait()
	if apperr.CodeOf(err) != apperr.ToolMissing || !strings.Contains(err.Error(), "brew install libimobiledevice") {
		t.Errorf("error = %v", err)
	}
}

func TestVeryLongLinesAreCut(t *testing.T) {
	adb := script(t, t.TempDir(), "adb", `head -c 200000 /dev/zero | tr '\0' 'x'; echo; echo "after"`)
	var c collector
	_ = Start(context.Background(), adbDeps(adb), Spec{Device: emulator, Mode: Logcat}, c.handlers()).Wait()
	got := c.texts()
	if len(got) != 2 || len(got[0]) != maxLineBytes || got[1] != "after" {
		lengths := make([]int, len(got))
		for i, line := range got {
			lengths[i] = len(line)
		}
		t.Errorf("line lengths = %v, want [%d 5]", lengths, maxLineBytes)
	}
}

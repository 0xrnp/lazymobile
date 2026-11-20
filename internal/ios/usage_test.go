package ios

import (
	"context"
	"testing"
	"time"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/run"
	"github.com/0xrnp/lazymobile/internal/run/runtest"
)

func TestParsePSUsage(t *testing.T) {
	for out, want := range map[string]time.Duration{
		"  0:19.63 206768\n": 19630 * time.Millisecond,
		"12:01.50 1024":      12*time.Minute + 1500*time.Millisecond,
		"1:02:03.00 1024":    time.Hour + 2*time.Minute + 3*time.Second,
		"2-01:00:00.00 1024": 49 * time.Hour,
	} {
		got, ok := ParsePSUsage(out)
		if !ok || got.CPU.Round(time.Millisecond) != want {
			t.Errorf("%q: %v, %v; want %v", out, got.CPU, ok, want)
		}
	}
	if got, _ := ParsePSUsage("0:19.63 206768"); got.Memory != 206768*1024 {
		t.Errorf("memory %d", got.Memory)
	}
	for _, out := range []string{"", "0:19.63", "a:b 1", "0:19.63 x", "1:2:3:4 1"} {
		if _, ok := ParsePSUsage(out); ok {
			t.Errorf("%q was read", out)
		}
	}
}

func TestAppPIDFindsTheAppInLaunchctl(t *testing.T) {
	// From `simctl spawn <udid> launchctl list` on an iOS 27.2 simulator.
	list := "PID\tStatus\tLabel\n" +
		"47152\t0\tUIKitApplication:com.apple.Preferences[92cb][rb-legacy]\n" +
		"-\t0\tUIKitApplication:com.apple.mobilesafari[1a2b][rb-legacy]\n" +
		"812\t0\tcom.apple.Preferences.helper\n"
	if got := AppPID(list, "com.apple.Preferences"); got != 47152 {
		t.Errorf("Preferences: %d", got)
	}
	for _, id := range []string{"com.apple.mobilesafari", "com.apple", "com.example.app"} {
		if got := AppPID(list, id); got != 0 {
			t.Errorf("%s: %d", id, got)
		}
	}
}

const launchctlList = "PID\tStatus\tLabel\n47152\t0\tUIKitApplication:com.example.app[92cb][rb-legacy]\n"

func TestAppUsageOnASimulatorStartsFromTheLastPid(t *testing.T) {
	ctx := context.Background()
	fake := runtest.Table(map[string]run.Result{"ps -o time=,rss= -p 47152": runtest.Out("0:19.63 206768\n")})
	usage, err := New(fake).AppUsage(ctx, bootedSim, "com.example.app", 47152)
	if err != nil || usage.PID != 47152 || usage.Memory != 206768*1024 {
		t.Fatalf("%+v, %v", usage, err)
	}
	if fake.Called("launchctl") {
		t.Error("the pid was looked up again")
	}
}

func TestAppUsageOnASimulatorFindsTheAppAgainAfterARestart(t *testing.T) {
	ctx := context.Background()
	fake := runtest.Table(map[string]run.Result{
		"ps -o time=,rss= -p 812":   runtest.Fail(1, ""),
		"launchctl list":            runtest.Out(launchctlList),
		"ps -o time=,rss= -p 47152": runtest.Out("0:01.00 1024\n"),
	})
	usage, err := New(fake).AppUsage(ctx, bootedSim, "com.example.app", 812)
	if err != nil || usage.PID != 47152 || usage.CPU != time.Second {
		t.Errorf("%+v, %v", usage, err)
	}
}

func TestAppUsageOnASimulatorSaysWhenTheAppIsNotRunning(t *testing.T) {
	ctx := context.Background()
	fake := runtest.Table(map[string]run.Result{"launchctl list": runtest.Out("PID\tStatus\tLabel\n")})
	if _, err := New(fake).AppUsage(ctx, bootedSim, "com.example.app", 0); apperr.CodeOf(err) != apperr.InvalidState {
		t.Errorf("not running: %v", err)
	}
	// The app is listed, but gone before ps reads it.
	fake = runtest.Table(map[string]run.Result{"launchctl list": runtest.Out(launchctlList), "ps -o": runtest.Fail(1, "")})
	if _, err := New(fake).AppUsage(ctx, bootedSim, "com.example.app", 0); apperr.CodeOf(err) != apperr.InvalidState {
		t.Errorf("gone: %v", err)
	}
	if _, err := New(runtest.Table(nil)).AppUsage(ctx, iphone, "com.example.app", 0); apperr.CodeOf(err) != apperr.Unsupported {
		t.Errorf("an iPhone: %v", err)
	}
}

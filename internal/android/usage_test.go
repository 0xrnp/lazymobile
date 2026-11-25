package android

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/run"
	"github.com/0xrnp/lazymobile/internal/run/runtest"
)

func TestParseUsage(t *testing.T) {
	// What the script printed for the Settings app on an Android 16 emulator.
	got, ok := ParseUsage("11578\r\n144 194\r\nVmRSS:\t  148632 kB\r\n100\r\n")
	want := device.Usage{PID: 11578, CPU: 3380 * time.Millisecond, Memory: 148632 * 1024}
	if !ok || got != want {
		t.Errorf("%+v, %v; want %+v", got, ok, want)
	}
	for _, out := range []string{"", "11578\n144 194\n100", "x\n1 2\nVmRSS: 1 kB\n100", "1\n1 2\nVmRSS: 1 MB\n100", "1\n1 2\nVmRSS: 1 kB\n0"} {
		if _, ok := ParseUsage(out); ok {
			t.Errorf("%q was read", out)
		}
	}
}

func TestAppUsageAsksTheDeviceOnceAndSaysWhenTheAppIsNotRunning(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		result run.Result
		code   apperr.Code
	}{
		"running":                    {runtest.Out("11578\r\n144 194\r\nVmRSS:\t  148632 kB\r\n100\r\n"), ""},
		"not running":                {runtest.Fail(1, ""), apperr.InvalidState},
		"not running, old adb":       {runtest.Out(""), apperr.InvalidState},
		"stopped after its pid":      {run.Result{Code: 1, Stdout: "11578\n", Stderr: "cut: /proc/11578/stat: No such file or directory"}, apperr.InvalidState},
		"something the script broke": {runtest.Out("11578\nnonsense\n"), apperr.ParseFailed},
		"the device went away":       {runtest.Fail(1, "error: device 'emulator-5554' not found"), apperr.CommandFailed},
	} {
		fake := runtest.Table(map[string]run.Result{"pidof -s com.example.app": tc.result})
		usage, err := New(fake, sdk(t, "adb")).AppUsage(ctx, runningEmulator, "com.example.app")
		if apperr.CodeOf(err) != tc.code {
			t.Errorf("%s: %v, want %q", name, err, tc.code)
		}
		if tc.code == "" && usage.PID != 11578 {
			t.Errorf("%s: %+v", name, usage)
		}
		if calls := fake.Calls(); len(calls) != 1 || !strings.Contains(calls[0], "-s emulator-5554 shell") {
			t.Errorf("%s: calls %q", name, calls)
		}
	}
}

func TestAppUsageRefusesWhatIsNotAnAppIDBeforeAskingTheDevice(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{})
	if _, err := New(fake, sdk(t, "adb")).AppUsage(context.Background(), runningEmulator, "x; reboot"); apperr.CodeOf(err) != apperr.InvalidState || len(fake.Calls()) != 0 {
		t.Errorf("%v, calls %q", err, fake.Calls())
	}
}

func TestParseJank(t *testing.T) {
	// What `dumpsys gfxinfo com.android.settings` printed on an Android 16 emulator, trimmed.
	busy := "Applications Graphics Acceleration Info:\n\n** Graphics info for pid 27764 [com.android.settings] **\n\nStats since: 8780728177935ns\nTotal frames rendered: 4\nJanky frames: 3 (75.00%)\nJanky frames (legacy): 4 (100.00%)\n50th percentile: 117ms\n90th percentile: 118ms\n95th percentile: 119ms\n99th percentile: 120ms\nHISTOGRAM: 5ms=0\n50th gpu percentile: 4950ms\n"
	got, ok := ParseJank(busy)
	want := device.Jank{Frames: 4, Janky: 3, Percent: 75, P50: 117, P90: 118, P95: 119, P99: 120}
	if !ok || got != want {
		t.Errorf("%+v, %v; want %+v", got, ok, want)
	}
	// After a reset the percentiles are leftovers.
	idle := "Total frames rendered: 0\nJanky frames: 0 (0.00%)\nJanky frames (legacy): 0 (0.00%)\n50th percentile: 4950ms\n"
	if got, ok := ParseJank(idle); !ok || got != (device.Jank{}) {
		t.Errorf("%+v, %v", got, ok)
	}
	for _, out := range []string{"", "Total frames rendered: 4\n", "Janky frames: 3 (75.00%)\n"} {
		if _, ok := ParseJank(out); ok {
			t.Errorf("%q was read", out)
		}
	}
}

func TestAppJankResetsOnlyWhenAsked(t *testing.T) {
	ctx := context.Background()
	out := "Total frames rendered: 10\nJanky frames: 1 (10.00%)\n50th percentile: 8ms\n"
	for _, reset := range []bool{false, true} {
		fake := runtest.Table(map[string]run.Result{"dumpsys gfxinfo com.example.app": runtest.Out(out)})
		jank, err := New(fake, sdk(t, "adb")).AppJank(ctx, runningEmulator, "com.example.app", reset)
		calls := fake.Calls()
		if err != nil || jank.Janky != 1 || len(calls) != 1 || strings.HasSuffix(calls[0], " reset") != reset {
			t.Errorf("reset %v: %+v, %v, %q", reset, jank, err, calls)
		}
	}
	fake := runtest.Table(map[string]run.Result{"dumpsys gfxinfo com.example.app": runtest.Out("No process found for: com.example.app\n")})
	if _, err := New(fake, sdk(t, "adb")).AppJank(ctx, runningEmulator, "com.example.app", false); apperr.CodeOf(err) != apperr.InvalidState {
		t.Errorf("not running: %v", err)
	}
	fake = runtest.Table(map[string]run.Result{})
	if _, err := New(fake, sdk(t, "adb")).AppJank(ctx, runningEmulator, "x; reboot", false); apperr.CodeOf(err) != apperr.InvalidState || len(fake.Calls()) != 0 {
		t.Errorf("bad id: %v, calls %q", err, fake.Calls())
	}
}

package logs

import (
	"strings"
	"testing"

	"github.com/0xrnp/lazymobile/internal/device"
)

func TestParseAndroid(t *testing.T) {
	tests := []struct {
		text  string
		pid   string
		level Level
	}{
		{"11-13 20:49:44.298   917  1126 D Tag: msg", "917", Debug},
		{"11-13 20:49:44.298 12345 12345 E AndroidRuntime: FATAL EXCEPTION: main", "12345", Error},
		{"01-02 03:04:05.678     1     1 A libc: Fatal signal 11", "1", Fatal},
		{"	at com.example.Main.run(Main.java:10)", "", NoLevel},
		{"--------- beginning of main", "", NoLevel},
		{"", "", NoLevel},
	}
	for _, tt := range tests {
		got := ParseAndroid(tt.text)
		if got.PID != tt.pid || got.Level != tt.level || got.Text != tt.text {
			t.Errorf("ParseAndroid(%q) = %+v, want pid %q level %v", tt.text, got, tt.pid, tt.level)
		}
	}
}

func TestParseIOS(t *testing.T) {
	tests := []struct {
		text  string
		pid   string
		level Level
	}{
		{"2026-11-13 20:56:36.411 Df Runner[30348:6e9c9] message", "30348", Info},
		{"2026-11-13 20:56:36.411 E  My App[77:1a] [com.example:net] failed", "77", Error},
		{"2026-11-13 20:56:36.411 Db backboardd[5:2] debug", "5", Debug},
		{"2026-11-13 20:56:36.411 F  Runner[9:9] fault", "9", Fatal},
		{"Filtering the log data using \"process == \\\"Runner\\\"\"", "", NoLevel},
		{"Timestamp               Ty Process[PID:TID]", "", NoLevel},
	}
	for _, tt := range tests {
		got := ParseIOS(tt.text)
		if got.PID != tt.pid || got.Level != tt.level {
			t.Errorf("ParseIOS(%q) = %+v, want pid %q level %v", tt.text, got, tt.pid, tt.level)
		}
	}
}

func TestAppFilterFollowsAnAppAcrossRestarts(t *testing.T) {
	f := NewAppFilter("com.example.app")
	f.AddPIDs([]string{"100"})
	steps := []struct {
		text string
		want bool
	}{
		{"11-13 20:49:44.298   100   100 I App: from the running app", true},
		{"11-13 20:49:44.299   200   200 I Other: someone else", false},
		{"11-13 20:49:45.000   500   600 I ActivityManager: Start proc 300:com.example.app/u0a1 for activity", true},
		{"11-13 20:49:45.100   300   300 E App: after the restart", true},
		{"11-13 20:49:45.200   400   400 E AndroidRuntime: Process: com.example.app, PID: 300", true},
		// A package that only starts with the same text is a different app.
		{"11-13 20:49:46.000   500   600 I ActivityManager: Start proc 700:com.example.application/u0a2 for service", true},
		{"11-13 20:49:46.100   700   700 I Other: from the other app", false},
		{"11-13 20:49:46.200   500   600 I ActivityManager: Start proc 800:com.example.app:push/u0a1 for service", true},
		{"11-13 20:49:46.300   800   800 I Push: from the app's second process", true},
	}
	for _, step := range steps {
		if got := f.Accept(ParseAndroid(step.text)); got != step.want {
			t.Errorf("Accept(%q) = %v, want %v", step.text, got, step.want)
		}
	}
}

func TestModes(t *testing.T) {
	ids := func(d device.Device) []Mode {
		var out []Mode
		for _, m := range Modes(d) {
			out = append(out, m.ID)
		}
		return out
	}
	tests := []struct {
		name string
		d    device.Device
		want []Mode
	}{
		{"running emulator", device.Device{Platform: device.Android, Kind: device.Emulator, State: device.Booted}, []Mode{Logcat}},
		{"android phone", device.Device{Platform: device.Android, Kind: device.Physical, State: device.Connected}, []Mode{Logcat}},
		{"booted simulator", device.Device{Platform: device.IOS, Kind: device.Simulator, State: device.Booted}, []Mode{System, Console}},
		{"iphone", device.Device{Platform: device.IOS, Kind: device.Physical, State: device.Connected}, []Mode{Console, System}},
		{"stopped simulator", device.Device{Platform: device.IOS, Kind: device.Simulator, State: device.Shutdown}, nil},
		{"unauthorized phone", device.Device{Platform: device.Android, Kind: device.Physical, State: device.Unauthorized}, nil},
	}
	for _, tt := range tests {
		got := ids(tt.d)
		if len(got) != len(tt.want) {
			t.Errorf("%s: modes = %v, want %v", tt.name, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("%s: modes = %v, want %v", tt.name, got, tt.want)
			}
		}
	}
}

func TestValidAppID(t *testing.T) {
	for id, want := range map[string]bool{
		"com.example.app": true, "com.example.my-app_2": true, "Runner": true,
		"": false, "com.example.app; reboot": false, "$(reboot)": false, "-rf": false, "a b": false, "a`b`": false, "a|b": false,
	} {
		if got := ValidAppID(id); got != want {
			t.Errorf("ValidAppID(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestParseLevel(t *testing.T) {
	if level, ok := ParseLevel("Warning"); !ok || level != Warning {
		t.Errorf("ParseLevel(Warning) = %v, %v", level, ok)
	}
	if _, ok := ParseLevel("loud"); ok {
		t.Error("ParseLevel accepted an unknown name")
	}
	if Error.String() != "error" || NoLevel.String() != "all" {
		t.Errorf("names: %s, %s", Error, NoLevel)
	}
}

// A real crash on an Android 16 emulator, from `am crash` on the Settings app. Every line of the
// report has the error level and the AndroidRuntime tag, and the line before it is not an error.
const androidCrash = `11-16 07:50:32.344 11123 11123 D AndroidRuntime: Shutting down VM
11-16 07:50:32.344 11123 11123 E AndroidRuntime: FATAL EXCEPTION: main
11-16 07:50:32.344 11123 11123 E AndroidRuntime: Process: com.android.settings, PID: 11123
11-16 07:50:32.344 11123 11123 E AndroidRuntime: android.app.RemoteServiceException$CrashedByAdbException: shell-induced crash
11-16 07:50:32.344 11123 11123 E AndroidRuntime: 	at android.app.ActivityThread.throwRemoteServiceException(ActivityThread.java:2377)
11-16 07:50:32.344 11123 11123 E AndroidRuntime: 	at com.android.internal.os.ZygoteInit.main(ZygoteInit.java:932)`

func TestEveryLineOfAnAndroidCrashReportIsMarked(t *testing.T) {
	for i, text := range strings.Split(androidCrash, "\n") {
		line := ParseAndroid(text)
		if want := i > 0; line.Crash != want {
			t.Errorf("line %d: Crash = %v, want %v: %s", i, line.Crash, want, text)
		}
	}
}

func TestLinesThatAreNotCrashesAreNotMarked(t *testing.T) {
	for _, text := range []string{
		"11-13 20:49:44.298   917  1126 E Tag: a plain error",
		"11-13 20:49:44.298   917  1126 W AndroidRuntime: a warning with the tag",
		"11-13 20:49:44.298   917  1126 I ReactNativeJS: Running \"main\"",
		"11-13 20:49:44.298   917  1126 D Tag: msg",
		"",
	} {
		if ParseAndroid(text).Crash {
			t.Errorf("marked as a crash: %q", text)
		}
	}
	if ParseIOS("2026-11-13 20:56:36.411 E  Runner[30348:6e9c9] a plain error").Crash {
		t.Error("an iOS error is not a crash")
	}
}

func TestCrashesAreMarkedOnEveryStream(t *testing.T) {
	tests := []struct {
		name  string
		parse func(string) Line
		text  string
	}{
		{"an Android native crash", ParseAndroid, "01-02 03:04:05.678     1     1 F libc    : Fatal signal 11 (SIGSEGV), code 1"},
		{"a React Native error on Android", ParseAndroid, "01-02 03:04:05.678  4000  4100 E ReactNativeJS: Error: boom"},
		{"an iOS uncaught exception", ParseIOS, "2026-11-13 20:56:36.411 E  Runner[30348:6e9c9] *** Terminating app due to uncaught exception 'NSInvalidArgumentException'"},
		{"an iOS crash type", ParseIOS, "2026-11-13 20:56:36.411 Df ReportCrash[88:1] Exception Type: EXC_BAD_ACCESS (SIGSEGV)"},
		{"an app's console output", ParsePlain, "Unhandled JS Exception: undefined is not a function"},
		{"a React Native invariant", ParsePlain, "Invariant Violation: requireNativeComponent: \"RNSScreen\" was not found"},
	}
	for _, tt := range tests {
		if got := tt.parse(tt.text); !got.Crash || got.Text != tt.text {
			t.Errorf("%s: %+v", tt.name, got)
		}
	}
}

package android

import (
	"context"
	"reflect"
	"runtime"
	"testing"

	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/run"
	"github.com/0xrnp/lazymobile/internal/run/runtest"
)

func runtimeArch() string { return runtime.GOARCH }

func TestParseAVDNamesSkipsLogLines(t *testing.T) {
	text := "INFO    | Storing crashdata in: /tmp/android/emu-crash.db\r\nPixel_9\n\n  Medium.Phone-API_36  \nWARNING | something\n"
	want := []string{"Pixel_9", "Medium.Phone-API_36"}
	if got := ParseAVDNames(text); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseADBDevices(t *testing.T) {
	text := "* daemon not running; starting now at tcp:5037\n* daemon started successfully\nList of devices attached\n" +
		"emulator-5554          device product:sdk_gphone64_arm64 model:sdk_gphone64_arm64 device:emu64a transport_id:1\n" +
		"R58M12345\tunauthorized usb:1-1 transport_id:4\n" +
		"0123456789ABCDEF       no permissions (missing udev rules? user is in the plugdev group); see [http://developer.android.com/tools/device.html] usb:1-2\n" +
		"adb-R58M._adb-tls-connect._tcp device product:x model:Galaxy_S23\n" +
		"\n"
	got := ParseADBDevices(text)
	want := []struct{ serial, state, model string }{
		{"emulator-5554", "device", "sdk_gphone64_arm64"},
		{"R58M12345", "unauthorized", ""},
		{"0123456789ABCDEF", "no permissions", ""},
		{"adb-R58M._adb-tls-connect._tcp", "device", "Galaxy_S23"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Serial != w.serial || got[i].State != w.state || got[i].Props["model"] != w.model {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], w)
		}
	}
}

func TestSerialsAndAddresses(t *testing.T) {
	for serial, want := range map[string]bool{
		"192.168.1.5:5555": true, "adb-X._adb-tls-connect._tcp": true, "R58M12345": false, "emulator-5554": false,
	} {
		if got := IsWifiSerial(serial); got != want {
			t.Errorf("IsWifiSerial(%q) = %v", serial, got)
		}
	}
	for address, want := range map[string]bool{
		"192.168.1.5:37123": true, "[fe80::1]:5555": true, "phone.local:5555": true,
		"192.168.1.5": false, "192.168.1.5:port": false, "a b:1": false, "x:1; rm -rf": false, "": false,
	} {
		if got := ValidAddress(address); got != want {
			t.Errorf("ValidAddress(%q) = %v", address, got)
		}
	}
}

func TestReleaseForAPI(t *testing.T) {
	for api, want := range map[string]string{"36": "16", "32": "12L", "99": "API 99", "": "", "Baklava": ""} {
		if got := ReleaseForAPI(api); got != want {
			t.Errorf("ReleaseForAPI(%q) = %q, want %q", api, got, want)
		}
	}
}

func TestCombineShowsAnEmulatorWhoseAVDIsUnknown(t *testing.T) {
	entries := []ADBEntry{{Serial: "emulator-5556", State: "device", Props: map[string]string{}}}
	devices := Combine([]string{"Pixel_9"}, entries, map[string]Fact{"emulator-5556": {AVD: "Deleted_AVD", Release: "15"}},
		func(string) AVDInfo { return AVDInfo{API: "36"} })
	if len(devices) != 2 {
		t.Fatalf("got %+v", devices)
	}
	orphan := devices[0]
	if orphan.ID != "emulator-5556" || orphan.Name != "Deleted_AVD" || orphan.State != device.Booted || orphan.Source != "adb" {
		t.Errorf("orphan = %+v", orphan)
	}
	if devices[1].State != device.Shutdown || devices[1].OSVersion != "16" {
		t.Errorf("avd = %+v", devices[1])
	}
}

func TestParseJavaVersion(t *testing.T) {
	tests := []struct {
		output string
		major  int
		ok     bool
	}{
		{`openjdk version "21.0.4" 2024-07-16`, 21, true},
		{`java version "1.8.0_392"`, 8, true},
		{`openjdk version "17" 2021-09-14`, 17, true},
		{"The operation couldn't be completed. Unable to locate a Java Runtime.", 0, false},
	}
	for _, tt := range tests {
		_, major, ok := ParseJavaVersion(tt.output)
		if major != tt.major || ok != tt.ok {
			t.Errorf("ParseJavaVersion(%q) = %d, %v; want %d, %v", tt.output, major, ok, tt.major, tt.ok)
		}
	}
}

// macOS ships a `java` that only prints an error, so a candidate counts only when it runs.
func TestFindJavaSkipsAStub(t *testing.T) {
	opts := Options{
		OS: "linux", Getenv: func(string) string { return "" }, Home: "/nowhere",
		LookPath:      func(string) (string, error) { return "/usr/bin/java", nil },
		JavaFallbacks: []string{"/opt/studio/jbr"},
	}
	fake := runtest.Table(map[string]run.Result{
		"/usr/bin/java -version":            runtest.Fail(1, "Unable to locate a Java Runtime."),
		"/opt/studio/jbr/bin/java -version": {Stderr: `openjdk version "21.0.4"`},
	})
	java, ok := opts.FindJava(context.Background(), fake)
	if !ok || java.Home != "/opt/studio/jbr" || java.Major != 21 {
		t.Errorf("java = %+v, ok = %v", java, ok)
	}
}

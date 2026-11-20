package ios

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/run/runtest"
)

const crashSim = "2D63736C-EDEF-47CB-AF23-182FDE3721E2"

// ips is a crash report in the shape macOS 27 writes for a simulator's app, shortened.
func ips(bugType, bundleID, udid, when string) string {
	return `{"app_name":"MobileSafari","timestamp":"` + when + `","app_version":"27.2","build_version":"8625.2","bundleID":"` + bundleID + `","bug_type":"` + bugType + `","name":"MobileSafari"}
{
  "captureTime" : "` + when + `",
  "coalitionName" : "com.apple.CoreSimulator.SimDevice.` + udid + `",
  "exception" : {"codes": "0x0, 0x0", "type": "EXC_CRASH", "signal": "SIGABRT"},
  "termination" : {"code": 6, "namespace": "SIGNAL", "indicator": "Abort trap: 6"},
  "asi" : {"libsystem_c.dylib": ["abort() called"]},
  "faultingThread" : 1,
  "threads" : [
    {"frames": [{"imageIndex": 0, "imageOffset": 100, "symbol": "mach_msg2_trap", "symbolLocation": 8}]},
    {"triggered": true, "frames": [
      {"imageIndex": 0, "imageOffset": 5492, "symbol": "__pthread_kill", "symbolLocation": 8},
      {"imageIndex": 1, "imageOffset": 4096}
    ]}
  ],
  "usedImages" : [{"name": "libsystem_kernel.dylib"}, {"name": "MobileSafari"}]
}`
}

func writeReport(t *testing.T, dir, name, text string, age time.Duration) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

func TestACrashReportReadsAsText(t *testing.T) {
	report, ok := ParseCrashReport([]byte(ips("309", "com.apple.mobilesafari", crashSim, "2026-11-17 18:31:34.24 +0530")), "com.apple.mobilesafari", crashSim)
	if !ok {
		t.Fatal("not read")
	}
	want := `MobileSafari 27.2 (8625.2), com.apple.mobilesafari
Crashed 2026-11-17 18:31:34.24 +0530
EXC_CRASH (SIGABRT)
Abort trap: 6
libsystem_c.dylib: abort() called

Thread 1 crashed:
0   libsystem_kernel.dylib  __pthread_kill + 8
1   MobileSafari            0x1000`
	if report.Text != want {
		t.Errorf("text =\n%s\nwant\n%s", report.Text, want)
	}
	if report.When != "2026-11-17 18:31:34.24 +0530" || report.App != "com.apple.mobilesafari" {
		t.Errorf("report = %+v", report)
	}
}

// macOS 27.2 sometimes appends a plain-text system profile after the JSON body. Such a report was
// skipped, so the crash looked missing.
func TestAReportWithASystemProfileAfterItsBodyIsRead(t *testing.T) {
	text := ips("309", "com.apple.mobilesafari", crashSim, "now") + `

System Profile:
Network Service: Wi-Fi, AirPort, en0
Boot Volume File System Type: apfs
Model: Mac16,8, BootROM 20457.41.3, proc 12:0:8:4 processors, 24 GB, SMC
`
	report, ok := ParseCrashReport([]byte(text), "com.apple.mobilesafari", crashSim)
	if !ok || !strings.Contains(report.Text, "EXC_CRASH (SIGABRT)") {
		t.Errorf("read %v, report %+v", ok, report)
	}
}

func TestOnlyACrashOfThatAppOnThatSimulatorIsTaken(t *testing.T) {
	for name, text := range map[string]string{
		"another app":       ips("309", "com.example.other", crashSim, "now"),
		"another simulator": ips("309", "com.apple.mobilesafari", "11111111-2222-3333-4444-555555555555", "now"),
		"a hang":            ips("298", "com.apple.mobilesafari", crashSim, "now"),
		"no body":           `{"bug_type":"309","bundleID":"com.apple.mobilesafari"}`,
		"broken":            "{not json\n{}",
	} {
		if report, ok := ParseCrashReport([]byte(text), "com.apple.mobilesafari", crashSim); ok {
			t.Errorf("%s: read %+v", name, report)
		}
	}
}

func TestLatestCrashTakesTheNewestReportOfTheSimulator(t *testing.T) {
	dir := t.TempDir()
	writeReport(t, dir, "MobileSafari-old.ips", ips("309", "com.apple.mobilesafari", crashSim, "the older one"), 2*time.Hour)
	writeReport(t, dir, "MobileSafari-new.ips", ips("309", "com.apple.mobilesafari", crashSim, "the newer one"), time.Hour)
	writeReport(t, dir, "MobileSafari-other-sim.ips", ips("309", "com.apple.mobilesafari", "11111111-2222-3333-4444-555555555555", "another simulator"), time.Minute)
	writeReport(t, dir, "notes.txt", "not a report", 0)
	c := New(runtest.Table(nil))
	c.CrashDir = dir
	sim := device.Device{ID: crashSim, Name: "iPhone 17", Platform: device.IOS, Kind: device.Simulator, State: "Shutdown"}
	report, err := c.LatestCrash(context.Background(), sim, "com.apple.mobilesafari")
	if err != nil {
		t.Fatal(err)
	}
	if report.When != "the newer one" || report.Path != filepath.Join(dir, "MobileSafari-new.ips") {
		t.Errorf("report = %+v", report)
	}
}

func TestLatestCrashSaysWhenThereIsNoneAndNeedsAnIPhoneConnected(t *testing.T) {
	c := New(runtest.Table(nil))
	c.CrashDir = t.TempDir()
	sim := device.Device{ID: crashSim, Name: "iPhone 17", Platform: device.IOS, Kind: device.Simulator}
	_, err := c.LatestCrash(context.Background(), sim, "com.apple.mobilesafari")
	if apperr.CodeOf(err) != apperr.NotFound || !strings.Contains(err.Error(), "com.apple.mobilesafari") {
		t.Errorf("error = %v, want not_found", err)
	}
	phone := device.Device{ID: "00008150-0001", Name: "iPhone", Platform: device.IOS, Kind: device.Physical}
	if _, err := c.LatestCrash(context.Background(), phone, "com.example.app"); apperr.CodeOf(err) != apperr.InvalidState {
		t.Errorf("error = %v, want invalid_state", err)
	}
}

func TestAFrameWithoutALibraryIsShownAsUnknown(t *testing.T) {
	text := strings.Replace(ips("309", "com.apple.mobilesafari", crashSim, "now"), `{"imageIndex": 1, "imageOffset": 4096}`, `{"imageOffset": 4096}`, 1)
	report, ok := ParseCrashReport([]byte(text), "com.apple.mobilesafari", crashSim)
	if !ok || !strings.Contains(report.Text, "1   ???") {
		t.Errorf("text =\n%s", report.Text)
	}
}

func TestOnlyPlainFilesAreRead(t *testing.T) {
	dir := t.TempDir()
	elsewhere := filepath.Join(t.TempDir(), "real.ips")
	if err := os.WriteFile(elsewhere, []byte(ips("309", "com.apple.mobilesafari", crashSim, "through a link")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(dir, "link.ips")); err != nil {
		t.Fatal(err)
	}
	c := New(runtest.Table(nil))
	c.CrashDir = dir
	sim := device.Device{ID: crashSim, Name: "iPhone 17", Platform: device.IOS, Kind: device.Simulator}
	if report, err := c.LatestCrash(context.Background(), sim, "com.apple.mobilesafari"); apperr.CodeOf(err) != apperr.NotFound {
		t.Errorf("report = %+v, error = %v; want a link left alone", report, err)
	}
}

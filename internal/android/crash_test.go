package android

import (
	"context"
	"strings"
	"testing"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/run"
	"github.com/0xrnp/lazymobile/internal/run/runtest"
)

// crashBuffer is `logcat -b crash -d -v threadtime` from an Android 16 emulator, shortened: a native
// crash of the Bluetooth app, and two crashes of the Clock app with a line of another process
// between them.
const crashBuffer = `--------- beginning of crash
11-17 18:01:32.201 18459 18459 E AndroidRuntime: FATAL EXCEPTION: main
11-17 18:01:32.201 18459 18459 E AndroidRuntime: Process: com.google.android.deskclock, PID: 18459
11-17 18:01:32.201 18459 18459 E AndroidRuntime: java.lang.IllegalStateException: the older crash
11-17 18:01:32.201 18459 18459 E AndroidRuntime: 	at android.os.Looper.loop(Looper.java:338)
11-17 18:32:18.532 17223 17262 F libc    : Fatal signal 6 (SIGABRT), code -1 (SI_QUEUE) in tid 17262 (bt_stack_manage), pid 17223 (droid.bluetooth)
11-17 18:32:18.683 18740 18740 F DEBUG   : *** *** *** *** *** *** *** *** *** *** *** *** *** *** *** ***
11-17 18:32:18.683 18740 18740 F DEBUG   : Cmdline: com.google.android.bluetooth
11-17 18:32:18.683 18740 18740 F DEBUG   : pid: 17223, tid: 17262, name: bt_stack_manage  >>> com.google.android.bluetooth <<<
11-17 18:32:18.683 18740 18740 F DEBUG   : signal 6 (SIGABRT), code -1 (SI_QUEUE), fault addr --------
11-17 18:32:18.683 18740 18740 F DEBUG   : Abort message: 'system/gd/hci/hci_layer.cc:527 on_hardware_error: Hardware Error Event with code 0x42'
11-17 18:32:18.683 18740 18740 F DEBUG   : backtrace:
11-17 18:32:18.683 18740 18740 F DEBUG   :       #00 pc 00000000000707b0  /apex/com.android.runtime/lib64/bionic/libc.so (abort+156)
11-17 18:32:19.001 671 690 E ActivityManager: an unrelated line from another process
11-17 18:33:10.376 18551 18551 E AndroidRuntime: FATAL EXCEPTION: main
11-17 18:33:10.376 18551 18551 E AndroidRuntime: Process: com.google.android.deskclock, PID: 18551
11-17 18:33:10.376 18551 18551 E AndroidRuntime: android.app.RemoteServiceException$CrashedByAdbException: shell-induced crash
11-17 18:33:10.376 18551 18551 E AndroidRuntime: 	at android.app.ActivityThread.throwRemoteServiceException(ActivityThread.java:2377)
11-17 18:33:10.376 18551 18551 E AndroidRuntime: 	at com.android.internal.os.ZygoteInit.main(ZygoteInit.java:932)
`

// dropboxCrashes is `dumpsys dropbox --print system_app_crash`, shortened, with an entry of another
// app between two of the Clock app.
const dropboxCrashes = `Drop box contents: 42 entries
Max entries: 1000
Searching for: system_app_crash

========================================
2026-11-17 17:59:16 system_app_crash (text, 1143 bytes)
Process: com.google.android.deskclock
PID: 17680
Package: com.google.android.deskclock v76002074 (7.5 (563137996))

java.lang.IllegalStateException: the older crash
	at android.os.Looper.loop(Looper.java:338)

========================================
2026-11-17 17:59:40 system_app_crash (text, 900 bytes)
Process: com.android.settings
PID: 17700

java.lang.NullPointerException: not the Clock app
	at android.os.Looper.loop(Looper.java:338)

========================================
2026-11-17 17:59:48 system_app_crash (text, 1142 bytes)
Process: com.google.android.deskclock
PID: 17797
Package: com.google.android.deskclock v76002074 (7.5 (563137996))

android.app.RemoteServiceException$CrashedByAdbException: shell-induced crash
	at android.app.ActivityThread.throwRemoteServiceException(ActivityThread.java:2377)

`

func TestTheLastJavaCrashOfAnAppIsTakenFromTheCrashBuffer(t *testing.T) {
	report, ok := LatestCrashInLogcat(crashBuffer, "com.google.android.deskclock")
	if !ok {
		t.Fatal("no crash found")
	}
	want := "FATAL EXCEPTION: main\n" +
		"Process: com.google.android.deskclock, PID: 18551\n" +
		"android.app.RemoteServiceException$CrashedByAdbException: shell-induced crash\n" +
		"\tat android.app.ActivityThread.throwRemoteServiceException(ActivityThread.java:2377)\n" +
		"\tat com.android.internal.os.ZygoteInit.main(ZygoteInit.java:932)"
	if report.Text != want {
		t.Errorf("text =\n%s\nwant\n%s", report.Text, want)
	}
	if report.When != "11-17 18:33:10.376" || report.App != "com.google.android.deskclock" {
		t.Errorf("report = %+v", report)
	}
}

func TestANativeCrashIsTakenFromItsDebugReport(t *testing.T) {
	report, ok := LatestCrashInLogcat(crashBuffer, "com.google.android.bluetooth")
	if !ok {
		t.Fatal("no crash found")
	}
	if !strings.HasPrefix(report.Text, "*** *** ***") || !strings.Contains(report.Text, "Abort message:") || !strings.HasSuffix(report.Text, "(abort+156)") {
		t.Errorf("text =\n%s", report.Text)
	}
	if strings.Contains(report.Text, "unrelated") {
		t.Errorf("a line of another process was kept:\n%s", report.Text)
	}
}

func TestAnAppThatDidNotCrashHasNoReport(t *testing.T) {
	if report, ok := LatestCrashInLogcat(crashBuffer, "com.example.app"); ok {
		t.Errorf("found %+v", report)
	}
	// A package whose name starts like another's is not that one.
	if report, ok := LatestCrashInLogcat(crashBuffer, "com.google.android"); ok {
		t.Errorf("found %+v", report)
	}
}

func TestTheLastDropboxEntryOfAnAppIsTaken(t *testing.T) {
	report, ok := LatestCrashInDropbox(dropboxCrashes, "com.google.android.deskclock")
	if !ok {
		t.Fatal("no crash found")
	}
	if report.When != "2026-11-17 17:59:48" || !strings.Contains(report.Text, "shell-induced crash") || strings.Contains(report.Text, "older") {
		t.Errorf("report = %+v", report)
	}
	if !strings.HasPrefix(report.Text, "Process: com.google.android.deskclock\n") {
		t.Errorf("text does not start with the process:\n%s", report.Text)
	}
	if _, ok := LatestCrashInDropbox(dropboxCrashes, "com.example.app"); ok {
		t.Error("found a crash of an app that has none")
	}
}

func TestLatestCrashReadsTheCrashBufferFirst(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{
		"logcat -b crash": runtest.Out(crashBuffer),
		"dropbox":         runtest.Out(dropboxCrashes),
	})
	report, err := New(fake, sdk(t, "adb")).LatestCrash(context.Background(), runningEmulator, "com.google.android.deskclock")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report.Text, "PID: 18551") {
		t.Errorf("text =\n%s", report.Text)
	}
	if !fake.Called("adb -s emulator-5554 logcat -b crash -d -v threadtime") {
		t.Errorf("ran %v", fake.Calls())
	}
	if fake.Called("dropbox") {
		t.Errorf("read dropbox although the crash buffer had the crash: %v", fake.Calls())
	}
}

func TestLatestCrashFallsBackToDropbox(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{
		"logcat -b crash":                  runtest.Out("--------- beginning of crash\n"),
		"dropbox --print system_app_crash": runtest.Out(dropboxCrashes),
		"dropbox":                          runtest.Out("Drop box contents: 0 entries\n"),
	})
	report, err := New(fake, sdk(t, "adb")).LatestCrash(context.Background(), runningEmulator, "com.google.android.deskclock")
	if err != nil {
		t.Fatal(err)
	}
	if report.When != "2026-11-17 17:59:48" {
		t.Errorf("report = %+v", report)
	}
	for _, tag := range dropboxCrashTags {
		if !fake.Called("adb -s emulator-5554 shell dumpsys dropbox --print " + tag) {
			t.Errorf("did not read %s; ran %v", tag, fake.Calls())
		}
	}
}

func TestLatestCrashSaysWhenThereIsNone(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{"adb": runtest.Out("")})
	_, err := New(fake, sdk(t, "adb")).LatestCrash(context.Background(), runningEmulator, "com.example.app")
	if apperr.CodeOf(err) != apperr.NotFound {
		t.Errorf("error = %v, want not_found", err)
	}
}

func TestLatestCrashNeedsARunningDevice(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{})
	stopped := runningEmulator
	stopped.State = "shutdown"
	_, err := New(fake, sdk(t, "adb")).LatestCrash(context.Background(), stopped, "com.example.app")
	if apperr.CodeOf(err) != apperr.InvalidState {
		t.Errorf("error = %v, want invalid_state", err)
	}
}

func TestTwoCrashesWrittenAtOnceKeepAllTheirLines(t *testing.T) {
	text := `11-17 18:40:00.001 2001 2001 E AndroidRuntime: FATAL EXCEPTION: main
11-17 18:40:00.001 2001 2001 E AndroidRuntime: Process: com.example.app, PID: 2001
11-17 18:40:00.002 3002 3002 F DEBUG   : *** *** *** *** *** *** *** *** *** *** *** *** *** *** *** ***
11-17 18:40:00.002 3002 3002 F DEBUG   : pid: 1500, tid: 1500, name: other  >>> com.example.other <<<
11-17 18:40:00.003 2001 2001 E AndroidRuntime: java.lang.IllegalStateException: boom
11-17 18:40:00.003 2001 2001 E AndroidRuntime: 	at com.example.app.Main.run(Main.java:12)
11-17 18:40:00.004 3002 3002 F DEBUG   : backtrace:
`
	app, ok := LatestCrashInLogcat(text, "com.example.app")
	if !ok || !strings.HasSuffix(app.Text, "at com.example.app.Main.run(Main.java:12)") || strings.Contains(app.Text, "backtrace") {
		t.Errorf("app crash =\n%s", app.Text)
	}
	other, ok := LatestCrashInLogcat(text, "com.example.other")
	if !ok || !strings.HasSuffix(other.Text, "backtrace:") || strings.Contains(other.Text, "boom") {
		t.Errorf("native crash =\n%s", other.Text)
	}
}

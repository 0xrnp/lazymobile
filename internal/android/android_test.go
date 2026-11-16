package android

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/run"
	"github.com/0xrnp/lazymobile/internal/run/runtest"
)

func write(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

// sdk makes a fake SDK with the named tools, and options that see nothing else on the machine.
func sdk(t *testing.T, tools ...string) Options {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "sdk")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools {
		path := map[string]string{
			"emulator":   "emulator/emulator",
			"adb":        "platform-tools/adb",
			"avdmanager": "cmdline-tools/latest/bin/avdmanager",
		}[tool]
		write(t, filepath.Join(dir, path), "#!/bin/sh\nexit 0\n", 0o755)
	}
	return Options{
		SDK:           dir,
		OS:            "linux",
		Home:          filepath.Join(root, "home"),
		Getenv:        func(string) string { return "" },
		LookPath:      func(string) (string, error) { return "", errors.New("not found") },
		SDKFallbacks:  []string{},
		JavaFallbacks: []string{},
		LogDir:        filepath.Join(root, "logs"),
	}
}

func avd(t *testing.T, opts Options, name, config string) {
	t.Helper()
	dir := filepath.Join(opts.AVDDir(), name+".avd")
	write(t, filepath.Join(opts.AVDDir(), name+".ini"), "path="+dir+"\ntarget=android-36\n", 0o644)
	write(t, filepath.Join(dir, "config.ini"), config, 0o644)
}

func find(t *testing.T, devices []device.Device, id string) device.Device {
	t.Helper()
	for _, d := range devices {
		if d.ID == id {
			return d
		}
	}
	t.Fatalf("no device with id %q in %+v", id, devices)
	return device.Device{}
}

func TestListCombinesAVDsRunningEmulatorsAndPhones(t *testing.T) {
	opts := sdk(t, "emulator", "adb")
	avd(t, opts, "Pixel_9", "avd.ini.displayname=Pixel 9\nhw.device.name=pixel_9\n")
	avd(t, opts, "Tablet", "avd.ini.displayname=\n")

	fake := runtest.Table(map[string]run.Result{
		"-list-avds": runtest.Out("INFO    | Storing crashdata in: /tmp/x\nPixel_9\nTablet\n"),
		"devices -l": runtest.Out("List of devices attached\n" +
			"emulator-5554          device product:sdk model:sdk_gphone64 transport_id:1\n" +
			"R58M12345              device usb:1-1 product:x model:Galaxy_S23 transport_id:2\n" +
			"192.168.1.20:5555      unauthorized transport_id:3\n"),
		"-s emulator-5554 emu avd name":                           runtest.Out("Pixel_9\r\nOK\r\n"),
		"-s emulator-5554 shell getprop sys.boot_completed":       runtest.Out("1\n"),
		"-s emulator-5554 shell getprop ro.build.version.release": runtest.Out("16\n"),
		"-s R58M12345 shell getprop ro.build.version.release":     runtest.Out("15\n"),
	})
	devices, errs := New(fake, opts).List(context.Background())
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(devices) != 4 {
		t.Fatalf("got %d devices, want 4: %+v", len(devices), devices)
	}

	running := find(t, devices, "Pixel_9")
	if running.State != device.Booted || running.Serial != "emulator-5554" || running.Name != "Pixel 9" ||
		running.OSVersion != "16" || running.Model != "pixel_9" || running.Source != "emulator" {
		t.Errorf("running emulator = %+v", running)
	}
	stopped := find(t, devices, "Tablet")
	if stopped.State != device.Shutdown || stopped.Serial != "" || stopped.Name != "Tablet" {
		t.Errorf("stopped emulator = %+v (an empty display name should fall back to the AVD name)", stopped)
	}
	phone := find(t, devices, "R58M12345")
	if phone.Kind != device.Physical || phone.State != device.Connected || phone.Name != "Galaxy S23" ||
		phone.Transport != "usb" || phone.OSVersion != "15" {
		t.Errorf("phone = %+v", phone)
	}
	wifi := find(t, devices, "192.168.1.20:5555")
	if wifi.State != device.Unauthorized || wifi.Transport != "wifi" || wifi.Detail == "" {
		t.Errorf("wifi phone = %+v", wifi)
	}
	if fake.Called("-s 192.168.1.20:5555") {
		t.Error("a device that is not usable should not be asked anything")
	}
}

func TestListShowsABootingEmulator(t *testing.T) {
	opts := sdk(t, "emulator", "adb")
	avd(t, opts, "Pixel_9", "")
	fake := runtest.Table(map[string]run.Result{
		"-list-avds":                 runtest.Out("Pixel_9\n"),
		"devices -l":                 runtest.Out("emulator-5554 device\n"),
		"emu avd name":               runtest.Out("Pixel_9\nOK\n"),
		"getprop sys.boot_completed": runtest.Out("\n"),
		"getprop ro.build.version":   runtest.Out("16\n"),
	})
	devices, _ := New(fake, opts).List(context.Background())
	if got := find(t, devices, "Pixel_9").State; got != device.Booting {
		t.Errorf("state = %s, want booting", got)
	}
}

func TestListReportsMissingToolsAndKeepsTheRest(t *testing.T) {
	opts := sdk(t, "adb")
	fake := runtest.Table(map[string]run.Result{"devices -l": runtest.Out("R58M12345 offline\n")})
	devices, errs := New(fake, opts).List(context.Background())
	if len(devices) != 1 || devices[0].State != device.Offline {
		t.Errorf("devices = %+v", devices)
	}
	if len(errs) != 1 || apperr.CodeOf(errs[0]) != apperr.ToolMissing || !strings.Contains(errs[0].Error(), `sdkmanager "emulator"`) {
		t.Errorf("errors = %v, want one saying how to install the emulator", errs)
	}
}

func TestFindToolOrder(t *testing.T) {
	configured := sdk(t, "adb")
	onPath := func(name string) (string, error) { return "/usr/bin/" + name, nil }

	configured.LookPath = onPath
	if tool, _ := configured.FindTool("adb"); tool.Via != "sdk" {
		t.Errorf("a configured SDK should win over PATH, got %+v", tool)
	}

	fromEnv := sdk(t, "adb")
	envSDK := fromEnv.SDK
	fromEnv.SDK = ""
	fromEnv.Getenv = func(key string) string {
		if key == "ANDROID_HOME" {
			return envSDK
		}
		return ""
	}
	if tool, ok := fromEnv.FindTool("adb"); !ok || tool.Via != "sdk" {
		t.Errorf("ANDROID_HOME should be used when PATH has nothing, got %+v", tool)
	}
	fromEnv.LookPath = onPath
	if tool, _ := fromEnv.FindTool("adb"); tool.Via != "path" {
		t.Errorf("PATH should win over ANDROID_HOME, got %+v", tool)
	}

	if _, ok := sdk(t).FindTool("adb"); ok {
		t.Error("found adb in an empty SDK")
	}
}

func TestFindToolPrefersTheNewestCommandLineTools(t *testing.T) {
	opts := sdk(t)
	for _, version := range []string{"9.0", "11.0", "latest", "2.1"} {
		write(t, filepath.Join(opts.SDK, "cmdline-tools", version, "bin", "avdmanager"), "", 0o755)
	}
	tool, _ := opts.FindTool("avdmanager")
	if !strings.Contains(tool.Path, "/latest/") {
		t.Errorf("path = %s, want the latest directory", tool.Path)
	}
	os.RemoveAll(filepath.Join(opts.SDK, "cmdline-tools", "latest"))
	tool, _ = opts.FindTool("avdmanager")
	if !strings.Contains(tool.Path, "/11.0/") {
		t.Errorf("path = %s, want 11.0 (newer than 9.0 as a number)", tool.Path)
	}
}

func fakeEmulator(t *testing.T, opts Options, script string) {
	t.Helper()
	write(t, filepath.Join(opts.SDK, "emulator", "emulator"), "#!/bin/sh\n"+script+"\n", 0o755)
}

func TestBootStartsTheEmulatorWithTheRightArguments(t *testing.T) {
	stoppedAVD := device.Device{ID: "Pixel_9", Name: "Pixel 9", Platform: device.Android, Kind: device.Emulator, State: device.Shutdown, Source: "emulator"}
	tests := []struct {
		name string
		opts BootOptions
		set  func(*Options)
		want string
	}{
		{"plain", BootOptions{}, nil, "-avd Pixel_9"},
		{"cold", BootOptions{Cold: true}, nil, "-avd Pixel_9 -no-snapshot-load"},
		{"wipe", BootOptions{Wipe: true, Cold: true}, nil, "-avd Pixel_9 -wipe-data"},
		{"headless with extra arguments", BootOptions{}, func(o *Options) { o.Headless = true; o.EmulatorArgs = []string{"-gpu", "host"} },
			"-avd Pixel_9 -no-window -gpu host"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := sdk(t, "emulator")
			if tt.set != nil {
				tt.set(&opts)
			}
			args := filepath.Join(t.TempDir(), "args")
			fakeEmulator(t, opts, `echo "$@" > "`+args+`"; sleep 1`)
			c := New(&runtest.Fake{}, opts)
			c.Grace = 200 * time.Millisecond
			if err := c.Boot(context.Background(), stoppedAVD, tt.opts); err != nil {
				t.Fatal(err)
			}
			// A new script can take a moment to start, so its output is waited for.
			var got []byte
			var err error
			for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
				if got, err = os.ReadFile(args); err == nil && len(got) > 0 {
					break
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(string(got)) != tt.want {
				t.Errorf("emulator arguments = %q, want %q", strings.TrimSpace(string(got)), tt.want)
			}
		})
	}
}

func TestBootExplainsAnEmulatorThatStopsAtOnce(t *testing.T) {
	opts := sdk(t, "emulator")
	fakeEmulator(t, opts, `echo "INFO | starting"; echo "PANIC: Broken AVD system path"; echo "bye"; exit 1`)
	c := New(&runtest.Fake{}, opts)
	c.Grace = 2 * time.Second
	err := c.Boot(context.Background(), device.Device{ID: "Bad", Name: "Bad", Platform: device.Android, Kind: device.Emulator, Source: "emulator"}, BootOptions{})
	if apperr.CodeOf(err) != apperr.CommandFailed || !strings.Contains(err.Error(), "PANIC: Broken AVD system path") {
		t.Errorf("error = %v, want the emulator's own reason", err)
	}
	if !strings.Contains(err.Error(), "emulator-Bad.log") {
		t.Errorf("error should point at the full log: %v", err)
	}
}

func TestBootOfARunningEmulator(t *testing.T) {
	running := device.Device{ID: "Pixel_9", Name: "Pixel 9", Platform: device.Android, Kind: device.Emulator, State: device.Booted, Serial: "emulator-5554", Source: "emulator"}
	c := New(&runtest.Fake{}, sdk(t))
	if err := c.Boot(context.Background(), running, BootOptions{}); err != nil {
		t.Errorf("booting a running emulator should succeed, got %v", err)
	}
	for _, opts := range []BootOptions{{Cold: true}, {Wipe: true}} {
		if err := c.Boot(context.Background(), running, opts); apperr.CodeOf(err) != apperr.InvalidState {
			t.Errorf("%+v on a running emulator: error = %v, want invalid_state", opts, err)
		}
	}
}

func TestBootRefusesWhatIsNotAnAVD(t *testing.T) {
	c := New(&runtest.Fake{}, sdk(t, "emulator"))
	for _, d := range []device.Device{
		{ID: "R58M", Name: "Phone", Platform: device.Android, Kind: device.Physical, Source: "adb"},
		{ID: "emulator-5554", Name: "Unknown", Platform: device.Android, Kind: device.Emulator, Source: "adb"},
		{ID: "UDID", Name: "iPhone", Platform: device.IOS, Kind: device.Simulator, Source: "simctl"},
	} {
		if err := c.Boot(context.Background(), d, BootOptions{}); apperr.CodeOf(err) != apperr.Unsupported {
			t.Errorf("%s: error = %v, want unsupported", d.Name, err)
		}
	}
}

func TestWaitBootedPollsUntilAndroidIsUp(t *testing.T) {
	opts := sdk(t, "emulator", "adb")
	avd(t, opts, "Pixel_9", "")
	asked := 0
	fake := &runtest.Fake{Answer: func(command string, _ []string, _ run.Options) run.Result {
		switch {
		case strings.Contains(command, "-list-avds"):
			return runtest.Out("Pixel_9\n")
		case strings.Contains(command, "devices -l"):
			return runtest.Out("emulator-5554 device\n")
		case strings.Contains(command, "emu avd name"):
			return runtest.Out("Pixel_9\nOK\n")
		case strings.Contains(command, "sys.boot_completed"):
			asked++
			if asked < 3 {
				return runtest.Out("\n")
			}
			return runtest.Out("1\n")
		}
		return runtest.Out("16\n")
	}}
	c := New(fake, opts)
	c.Poll = 10 * time.Millisecond
	if err := c.WaitBooted(context.Background(), device.Device{ID: "Pixel_9", Name: "Pixel 9"}); err != nil {
		t.Fatal(err)
	}
	if asked != 3 {
		t.Errorf("asked %d times, want 3", asked)
	}
}

func TestWaitBootedGivesUp(t *testing.T) {
	opts := sdk(t, "emulator", "adb")
	fake := runtest.Table(map[string]run.Result{"-list-avds": runtest.Out(""), "devices -l": runtest.Out("")})
	c := New(fake, opts)
	c.Poll = 5 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	err := c.WaitBooted(ctx, device.Device{ID: "Pixel_9", Name: "Pixel 9"})
	if apperr.CodeOf(err) != apperr.Timeout {
		t.Errorf("error = %v, want timeout", err)
	}
}

func TestShutdown(t *testing.T) {
	running := device.Device{ID: "Pixel_9", Name: "Pixel 9", Platform: device.Android, Kind: device.Emulator, State: device.Booted, Serial: "emulator-5554", Source: "emulator"}
	opts := sdk(t, "adb")

	fake := runtest.Table(map[string]run.Result{"emu kill": runtest.Out("OK: killing emulator, bye bye\n")})
	if err := New(fake, opts).Shutdown(context.Background(), running); err != nil {
		t.Fatal(err)
	}
	if !fake.Called("-s emulator-5554 emu kill") {
		t.Errorf("ran %v", fake.Calls())
	}

	gone := runtest.Table(map[string]run.Result{"emu kill": runtest.Fail(1, "adb: device 'emulator-5554' not found")})
	if err := New(gone, opts).Shutdown(context.Background(), running); err != nil {
		t.Errorf("an emulator that already stopped should count as shut down, got %v", err)
	}

	idle := &runtest.Fake{}
	stopped := running
	stopped.State, stopped.Serial = device.Shutdown, ""
	if err := New(idle, opts).Shutdown(context.Background(), stopped); err != nil || len(idle.Calls()) != 0 {
		t.Errorf("a stopped emulator needs no command: err = %v, ran %v", err, idle.Calls())
	}
}

func withJava(answers map[string]run.Result) map[string]run.Result {
	answers["java -version"] = run.Result{Stderr: `openjdk version "21.0.4" 2024-07-16`}
	return answers
}

func TestCreateAnswersThePromptAndPassesJava(t *testing.T) {
	opts := sdk(t, "avdmanager")
	opts.JavaHome = "/fake/jdk"
	var seen run.Options
	fake := &runtest.Fake{Answer: func(command string, _ []string, o run.Options) run.Result {
		if strings.Contains(command, "java -version") {
			return run.Result{Stderr: `openjdk version "21.0.4"`}
		}
		seen = o
		return run.Result{}
	}}
	if err := New(fake, opts).Create(context.Background(), "Mine", "system-images;android-36;google_apis;arm64-v8a", "pixel_9"); err != nil {
		t.Fatal(err)
	}
	if !fake.Called("create avd -n Mine -k system-images;android-36;google_apis;arm64-v8a -d pixel_9") {
		t.Errorf("ran %v", fake.Calls())
	}
	if seen.Stdin != "no\n" || !reflect.DeepEqual(seen.Env, []string{"JAVA_HOME=/fake/jdk"}) {
		t.Errorf("options = %+v", seen)
	}
}

func TestCreateFailures(t *testing.T) {
	tests := []struct {
		name    string
		avdName string
		tools   []string
		answers map[string]run.Result
		want    apperr.Code
	}{
		{"bad name", "has space", []string{"avdmanager"}, withJava(map[string]run.Result{}), apperr.InvalidState},
		{"no avdmanager", "Mine", nil, withJava(map[string]run.Result{}), apperr.ToolMissing},
		{"no java", "Mine", []string{"avdmanager"}, map[string]run.Result{}, apperr.ToolMissing},
		{"an error printed with exit code 0", "Mine", []string{"avdmanager"},
			withJava(map[string]run.Result{"create avd": {Stderr: "Error: Package path is not valid.\nmore"}}), apperr.CommandFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := sdk(t, tt.tools...)
			opts.JavaHome = "/fake/jdk"
			err := New(runtest.Table(tt.answers), opts).Create(context.Background(), tt.avdName, "image", "")
			if apperr.CodeOf(err) != tt.want {
				t.Errorf("error = %v, want code %q", err, tt.want)
			}
		})
	}
}

func TestDeleteRefusesARunningEmulator(t *testing.T) {
	running := device.Device{ID: "Pixel_9", Name: "Pixel 9", Platform: device.Android, Kind: device.Emulator, State: device.Booted, Serial: "emulator-5554", Source: "emulator"}
	fake := &runtest.Fake{}
	err := New(fake, sdk(t, "avdmanager")).Delete(context.Background(), running)
	if apperr.CodeOf(err) != apperr.InvalidState || len(fake.Calls()) != 0 {
		t.Errorf("error = %v, ran %v", err, fake.Calls())
	}
}

func TestWirelessDevices(t *testing.T) {
	opts := sdk(t, "adb")
	ctx := context.Background()

	paired := runtest.Table(map[string]run.Result{"pair": runtest.Out("Successfully paired to 192.168.1.5:37123 [guid=adb-X]\n")})
	if err := New(paired, opts).Pair(ctx, "192.168.1.5:37123", "123456"); err != nil {
		t.Errorf("pair: %v", err)
	}
	wrongCode := runtest.Table(map[string]run.Result{"pair": runtest.Out("Failed: Wrong password or connection was dropped.\n")})
	if err := New(wrongCode, opts).Pair(ctx, "192.168.1.5:37123", "123456"); apperr.CodeOf(err) != apperr.CommandFailed {
		t.Errorf("pair with a wrong code: %v", err)
	}
	never := &runtest.Fake{}
	if err := New(never, opts).Pair(ctx, "192.168.1.5:37123", "12345"); apperr.CodeOf(err) != apperr.InvalidState {
		t.Errorf("pair with a short code: %v", err)
	}
	if err := New(never, opts).Pair(ctx, "192.168.1.5; reboot", "123456"); apperr.CodeOf(err) != apperr.InvalidState {
		t.Errorf("pair with a bad address: %v", err)
	}
	if len(never.Calls()) != 0 {
		t.Errorf("invalid input reached adb: %v", never.Calls())
	}

	// adb exits 0 whether or not the connection worked.
	connected := runtest.Table(map[string]run.Result{"connect": runtest.Out("connected to 192.168.1.5:5555\n")})
	if err := New(connected, opts).Connect(ctx, "192.168.1.5:5555"); err != nil {
		t.Errorf("connect: %v", err)
	}
	refused := runtest.Table(map[string]run.Result{"connect": runtest.Out("failed to connect to '192.168.1.5:5555': Connection refused\n")})
	if err := New(refused, opts).Connect(ctx, "192.168.1.5:5555"); apperr.CodeOf(err) != apperr.CommandFailed {
		t.Errorf("refused connect: %v", err)
	}

	usb := device.Device{ID: "R58M", Platform: device.Android, Kind: device.Physical, Transport: "usb"}
	if err := New(never, opts).Disconnect(ctx, usb); apperr.CodeOf(err) != apperr.Unsupported {
		t.Errorf("disconnecting a USB device: %v", err)
	}
}

func TestInstallNoticesAFailurePrintedWithExitCode0(t *testing.T) {
	phone := device.Device{ID: "R58M", Name: "Phone", Platform: device.Android, Kind: device.Physical, State: device.Connected}
	failed := runtest.Table(map[string]run.Result{"install -r": runtest.Out("Performing Streamed Install\nFailure [INSTALL_FAILED_UPDATE_INCOMPATIBLE]\n")})
	err := New(failed, sdk(t, "adb")).Install(context.Background(), phone, "/tmp/app.apk")
	if apperr.CodeOf(err) != apperr.CommandFailed || !strings.Contains(err.Error(), "INSTALL_FAILED_UPDATE_INCOMPATIBLE") {
		t.Errorf("error = %v", err)
	}
}

func TestSystemImagesListsOnlyWhatRunsHere(t *testing.T) {
	opts := sdk(t)
	native := "x86_64"
	if normalizeArch(runtimeArch()) == "arm64" {
		native = "arm64-v8a"
	}
	foreign := map[string]string{"x86_64": "arm64-v8a", "arm64-v8a": "x86_64"}[native]
	for _, image := range []string{
		"android-34/google_apis/" + native,
		"android-36/google_apis_playstore/" + native,
		"android-36/google_apis/" + foreign,
	} {
		write(t, filepath.Join(opts.SDK, "system-images", image, "system.img"), "", 0o644)
	}
	// A folder without an image is an interrupted download.
	if err := os.MkdirAll(filepath.Join(opts.SDK, "system-images", "android-35", "default", native), 0o755); err != nil {
		t.Fatal(err)
	}

	images := New(&runtest.Fake{}, opts).SystemImages()
	if len(images) != 2 {
		t.Fatalf("got %d images, want 2: %+v", len(images), images)
	}
	if images[0].API != "36" || images[1].API != "34" {
		t.Errorf("order = %s, %s; want newest first", images[0].API, images[1].API)
	}
	if want := "system-images;android-36;google_apis_playstore;" + native; images[0].ID != want {
		t.Errorf("id = %s, want %s", images[0].ID, want)
	}
	if !strings.HasPrefix(images[0].Label, "Android 16 (API 36)") {
		t.Errorf("label = %s", images[0].Label)
	}
}

func TestInstalledAppsAreThePackagesAUserAdded(t *testing.T) {
	phone := device.Device{ID: "R58M", Name: "Phone", Platform: device.Android, Kind: device.Physical, State: device.Connected}
	fake := runtest.Table(map[string]run.Result{"pm list packages -3": runtest.Out("package:com.zeta.app\r\npackage:com.example.app\r\n\r\n")})
	apps, err := New(fake, sdk(t, "adb")).InstalledApps(context.Background(), phone)
	if err != nil {
		t.Fatal(err)
	}
	want := []device.App{{ID: "com.example.app"}, {ID: "com.zeta.app"}}
	if !slices.Equal(apps, want) {
		t.Errorf("apps = %v", apps)
	}
	if !fake.Called("-s R58M shell pm list packages -3") {
		t.Errorf("ran %v", fake.Calls())
	}

	stopped := phone
	stopped.State = device.Offline
	if _, err := New(fake, sdk(t, "adb")).InstalledApps(context.Background(), stopped); apperr.CodeOf(err) != apperr.InvalidState {
		t.Errorf("an offline phone: %v", err)
	}
}

var runningEmulator = device.Device{ID: "Pixel_9a", Name: "Pixel 9a", Platform: device.Android, Kind: device.Emulator, State: device.Booted, Serial: "emulator-5554"}

func TestAppInfoReadsTheVersionFromDumpsys(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{"shell dumpsys package com.example.app": runtest.Out(dumpsysPackage)})
	app, err := New(fake, sdk(t, "adb")).AppInfo(context.Background(), runningEmulator, "com.example.app")
	if err != nil {
		t.Fatal(err)
	}
	if want := (device.App{ID: "com.example.app", Version: "0.0.47", Build: "37"}); app != want {
		t.Errorf("app = %+v, want %+v", app, want)
	}
	if !fake.Called("-s emulator-5554 shell dumpsys package com.example.app") {
		t.Errorf("ran %v", fake.Calls())
	}
}

func TestAppInfoRefusesWhatIsNotAnAppIDBeforeAskingTheDevice(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{})
	for _, id := range []string{"", "com.example.app; reboot", "$(id)", "a b"} {
		if _, err := New(fake, sdk(t, "adb")).AppInfo(context.Background(), runningEmulator, id); apperr.CodeOf(err) != apperr.InvalidState {
			t.Errorf("AppInfo(%q) error = %v, want invalid_state", id, err)
		}
	}
	if calls := fake.Calls(); len(calls) != 0 {
		t.Errorf("the device was asked: %v", calls)
	}
}

func TestAppInfoOfAnAppThatIsNotInstalled(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{"dumpsys package": runtest.Out("Unable to find package: com.example.gone\n")})
	_, err := New(fake, sdk(t, "adb")).AppInfo(context.Background(), runningEmulator, "com.example.gone")
	if apperr.CodeOf(err) != apperr.NotFound {
		t.Errorf("error = %v, want not_found", err)
	}
}

func TestAppInfoNeedsARunningDevice(t *testing.T) {
	offline := runningEmulator
	offline.State = device.Offline
	fake := runtest.Table(map[string]run.Result{})
	if _, err := New(fake, sdk(t, "adb")).AppInfo(context.Background(), offline, "com.example.app"); apperr.CodeOf(err) != apperr.InvalidState {
		t.Errorf("an offline device: %v", err)
	}
	if fake.Called("adb") {
		t.Errorf("an offline device was asked: %v", fake.Calls())
	}
}

func TestUninstallRunsAdbUninstall(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{"uninstall com.example.app": runtest.Out("Success\n")})
	if err := New(fake, sdk(t, "adb")).Uninstall(context.Background(), runningEmulator, "com.example.app"); err != nil {
		t.Fatal(err)
	}
	if !fake.Called("-s emulator-5554 uninstall com.example.app") {
		t.Errorf("ran %v", fake.Calls())
	}
}

func TestUninstallNoticesAFailurePrintedWithExitCode0(t *testing.T) {
	failed := runtest.Table(map[string]run.Result{"uninstall": runtest.Out("Failure [DELETE_FAILED_DEVICE_POLICY_MANAGER]\n")})
	err := New(failed, sdk(t, "adb")).Uninstall(context.Background(), runningEmulator, "com.example.app")
	if apperr.CodeOf(err) != apperr.CommandFailed || !strings.Contains(err.Error(), "DELETE_FAILED_DEVICE_POLICY_MANAGER") {
		t.Errorf("error = %v", err)
	}
}

func TestUninstallRefusesBeforeAskingTheDevice(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{})
	offline := runningEmulator
	offline.State = device.Offline
	for name, call := range map[string]func() error{
		"an offline device": func() error {
			return New(fake, sdk(t, "adb")).Uninstall(context.Background(), offline, "com.example.app")
		},
		"an injected id": func() error {
			return New(fake, sdk(t, "adb")).Uninstall(context.Background(), runningEmulator, "a; reboot")
		},
		"an option as an id": func() error { return New(fake, sdk(t, "adb")).Uninstall(context.Background(), runningEmulator, "-k") },
	} {
		if apperr.CodeOf(call()) != apperr.InvalidState {
			t.Errorf("%s: want invalid_state", name)
		}
	}
	if calls := fake.Calls(); len(calls) != 0 {
		t.Errorf("the device was asked: %v", calls)
	}
}

// What monkey printed on a real Android 16 emulator, for an app it opened and for a package that
// is not installed (exit code 252).
const (
	monkeyLaunched = `  bash arg: -p
  bash arg: com.example.app
  bash arg: -c
  bash arg: android.intent.category.LAUNCHER
  bash arg: 1
args: [-p, com.example.app, -c, android.intent.category.LAUNCHER, 1]
 arg: "-p"
 arg: "com.example.app"
 arg: "-c"
 arg: "android.intent.category.LAUNCHER"
 arg: "1"
data="com.example.app"
data="android.intent.category.LAUNCHER"
Events injected: 1
## Network stats: elapsed time=35ms (0ms mobile, 0ms wifi, 35ms not connected)
`
	monkeyNothingToRun = `args: [-p, com.example.nope, -c, android.intent.category.LAUNCHER, 1]
 arg: "-p"
 arg: "com.example.nope"
data="com.example.nope"
data="android.intent.category.LAUNCHER"
** No activities found to run, monkey aborted.
`
)

func TestLaunchOpensTheAppAndRestartStopsItFirst(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{"shell monkey": runtest.Out(monkeyLaunched), "shell am force-stop": {}})
	c := New(fake, sdk(t, "adb"))
	if err := c.Launch(context.Background(), runningEmulator, "com.example.app", false); err != nil {
		t.Fatal(err)
	}
	if !fake.Called("-s emulator-5554 shell monkey -p com.example.app -c android.intent.category.LAUNCHER 1") {
		t.Errorf("ran %v", fake.Calls())
	}
	if fake.Called("force-stop") {
		t.Errorf("a plain launch stopped the app: %v", fake.Calls())
	}

	if err := c.Launch(context.Background(), runningEmulator, "com.example.app", true); err != nil {
		t.Fatal(err)
	}
	calls := fake.Calls()
	if n := len(calls); n != 3 || !strings.Contains(calls[1], "shell am force-stop com.example.app") || !strings.Contains(calls[2], "shell monkey") {
		t.Errorf("a restart ran %v", calls)
	}
}

func TestLaunchOfAPackageWithNothingToOpenSaysSo(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{"shell monkey": {Code: 252, Stdout: monkeyNothingToRun}})
	err := New(fake, sdk(t, "adb")).Launch(context.Background(), runningEmulator, "com.example.nope", false)
	if apperr.CodeOf(err) != apperr.NotFound || !strings.Contains(err.Error(), "com.example.nope has nothing to launch on Pixel 9a") {
		t.Errorf("error = %v", err)
	}
}

func TestStopForceStopsOnlyAnInstalledApp(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{
		"shell dumpsys package com.example.app": runtest.Out(dumpsysPackage),
		"shell dumpsys package com.example.ap":  runtest.Out(""),
		"shell am force-stop":                   {},
	})
	c := New(fake, sdk(t, "adb"))
	// `am force-stop` succeeds for any name, so a typo must not reach it.
	if err := c.Stop(context.Background(), runningEmulator, "com.example.ap"); apperr.CodeOf(err) != apperr.NotFound {
		t.Errorf("a typo: %v", err)
	}
	if fake.Called("force-stop") {
		t.Errorf("a typo was force-stopped: %v", fake.Calls())
	}
	if err := c.Stop(context.Background(), runningEmulator, "com.example.app"); err != nil {
		t.Fatal(err)
	}
	if !fake.Called("-s emulator-5554 shell am force-stop com.example.app") {
		t.Errorf("ran %v", fake.Calls())
	}
}

func TestLaunchAndStopRefuseBeforeAskingTheDevice(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{})
	c := New(fake, sdk(t, "adb"))
	offline := runningEmulator
	offline.State = device.Offline
	ctx := context.Background()
	for name, err := range map[string]error{
		"launch on an offline device": c.Launch(ctx, offline, "com.example.app", false),
		"stop on an offline device":   c.Stop(ctx, offline, "com.example.app"),
		"launch of an injected id":    c.Launch(ctx, runningEmulator, "a; reboot", true),
		"stop of an option as an id":  c.Stop(ctx, runningEmulator, "-k"),
	} {
		if apperr.CodeOf(err) != apperr.InvalidState {
			t.Errorf("%s: %v, want invalid_state", name, err)
		}
	}
	if calls := fake.Calls(); len(calls) != 0 {
		t.Errorf("the device was asked: %v", calls)
	}
}

// What am printed on a real Android 16 emulator for a link an app opened, and for one that no app
// handles (exit code 1).
const (
	amStarted          = "Starting: Intent { act=android.intent.action.VIEW dat=https://example.com/... }\n"
	amUnableToResolve  = "Starting: Intent { act=android.intent.action.VIEW dat=nosuchscheme://x/... }\nError: Activity not started, unable to resolve Intent { act=android.intent.action.VIEW dat=nosuchscheme://x/... flg=0x10000000 xflg=0x4 }\n"
	pngBytesForTesting = "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\r\n\n\r\n"
)

func TestOpenURLQuotesTheLinkForTheDevicesShell(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{"shell am start": runtest.Out(amStarted)})
	c := New(fake, sdk(t, "adb"))
	for link, want := range map[string]string{
		"https://example.com/a?b=1&c=2": `-d 'https://example.com/a?b=1&c=2'`,
		"myapp://it's":                  `-d 'myapp://it'\''s'`,
		"myapp://x;reboot":              `-d 'myapp://x;reboot'`,
	} {
		if err := c.OpenURL(context.Background(), runningEmulator, link); err != nil {
			t.Fatal(err)
		}
		if got := fake.Calls()[len(fake.Calls())-1]; !strings.Contains(got, "-s emulator-5554 shell am start -a android.intent.action.VIEW "+want) {
			t.Errorf("ran %q, want it to contain %q", got, want)
		}
	}
}

func TestOpenURLNoAppHandlesSaysSo(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{"shell am start": {Code: 1, Stdout: amUnableToResolve}})
	err := New(fake, sdk(t, "adb")).OpenURL(context.Background(), runningEmulator, "nosuchscheme://x")
	if apperr.CodeOf(err) != apperr.NotFound || !strings.Contains(err.Error(), "No app on Pixel 9a opens that link") {
		t.Errorf("error = %v", err)
	}
}

func TestScreenshotReturnsThePNGAsItIs(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{"exec-out screencap -p": runtest.Out(pngBytesForTesting)})
	png, err := New(fake, sdk(t, "adb")).Screenshot(context.Background(), runningEmulator)
	if err != nil || string(png) != pngBytesForTesting {
		t.Errorf("png = %q, error = %v", png, err)
	}
	if !fake.Called("adb -s emulator-5554 exec-out screencap -p") {
		t.Errorf("ran %v", fake.Calls())
	}

	notPNG := runtest.Table(map[string]run.Result{"exec-out screencap -p": runtest.Out("error: closed")})
	if _, err := New(notPNG, sdk(t, "adb")).Screenshot(context.Background(), runningEmulator); apperr.CodeOf(err) != apperr.ParseFailed {
		t.Errorf("something that is not a PNG: %v", err)
	}
}

func TestOpenURLAndScreenshotRefuseADeviceThatIsNotRunning(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{})
	offline := runningEmulator
	offline.State = device.Offline
	c := New(fake, sdk(t, "adb"))
	_, shotErr := c.Screenshot(context.Background(), offline)
	for name, err := range map[string]error{"link": c.OpenURL(context.Background(), offline, "https://example.com"), "screenshot": shotErr} {
		if apperr.CodeOf(err) != apperr.InvalidState {
			t.Errorf("%s: %v", name, err)
		}
	}
	if len(fake.Calls()) != 0 {
		t.Errorf("the device was asked: %v", fake.Calls())
	}
}

func TestAppearanceReadsAndSetsTheNightMode(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{
		"shell cmd uimode night yes": runtest.Out("Night mode: yes\n"),
		"shell cmd uimode night no":  runtest.Out("Night mode: no\n"),
		"shell cmd uimode night":     runtest.Out("Night mode: auto\n"),
	})
	c := New(fake, sdk(t, "adb"))
	ctx := context.Background()
	if mode, err := c.Appearance(ctx, runningEmulator); err != nil || mode != "auto" {
		t.Errorf("a device that follows the time of day: %q, %v", mode, err)
	}
	for mode, want := range map[string]string{"dark": "night yes", "light": "night no"} {
		if err := c.SetAppearance(ctx, runningEmulator, mode); err != nil {
			t.Fatal(err)
		}
		if got := fake.Calls()[len(fake.Calls())-1]; !strings.HasSuffix(got, "-s emulator-5554 shell cmd uimode "+want) {
			t.Errorf("%s ran %q", mode, got)
		}
	}
	for text, want := range map[string]string{"Night mode: yes": "dark", "Night mode: no": "light"} {
		one := runtest.Table(map[string]run.Result{"uimode night": runtest.Out(text + "\n")})
		if got, _ := New(one, sdk(t, "adb")).Appearance(ctx, runningEmulator); got != want {
			t.Errorf("%q read as %q, want %q", text, got, want)
		}
	}
}

func TestAppearanceOfAnAndroidWithoutTheCommandSaysWhatItNeeds(t *testing.T) {
	old := runtest.Table(map[string]run.Result{"uimode": runtest.Out("cmd: Can't find service: uimode\n")})
	c := New(old, sdk(t, "adb"))
	if _, err := c.Appearance(context.Background(), runningEmulator); apperr.CodeOf(err) != apperr.ParseFailed || !strings.Contains(err.Error(), "Android 10") {
		t.Errorf("read: %v", err)
	}
	if err := c.SetAppearance(context.Background(), runningEmulator, "dark"); apperr.CodeOf(err) != apperr.CommandFailed || !strings.Contains(err.Error(), "Android 10") {
		t.Errorf("set: %v", err)
	}
	offline := runningEmulator
	offline.State = device.Offline
	none := runtest.Table(map[string]run.Result{})
	if err := New(none, sdk(t, "adb")).SetAppearance(context.Background(), offline, "dark"); apperr.CodeOf(err) != apperr.InvalidState || len(none.Calls()) != 0 {
		t.Errorf("an offline device: %v, calls %v", err, none.Calls())
	}
}

func TestLocationOfAnEmulatorTakesTheLongitudeFirstAndNoticesRefusal(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{"emu geo fix": runtest.Out("OK\n")})
	c := New(fake, sdk(t, "adb"))
	if err := c.SetLocation(context.Background(), runningEmulator, 37.7749, -122.4194); err != nil {
		t.Fatal(err)
	}
	if !fake.Called("-s emulator-5554 emu geo fix -122.4194 37.7749") {
		t.Errorf("ran %v", fake.Calls())
	}
	// The console says KO and adb still exits 0.
	refused := runtest.Table(map[string]run.Result{"emu geo fix": runtest.Out("KO: invalid longitude value. Should be in [-180,+180] range\n")})
	err := New(refused, sdk(t, "adb")).SetLocation(context.Background(), runningEmulator, 1, 999)
	if apperr.CodeOf(err) != apperr.CommandFailed || !strings.Contains(err.Error(), "invalid longitude") {
		t.Errorf("a refusal: %v", err)
	}
}

func TestLocationAndStatusBarAreOnlyForEmulators(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{})
	phone := runningEmulator
	phone.Kind = device.Physical
	c := New(fake, sdk(t, "adb"))
	for name, err := range map[string]error{
		"location":  c.SetLocation(context.Background(), phone, 1, 2),
		"clean":     c.CleanStatusBar(context.Background(), phone),
		"reset":     c.ResetStatusBar(context.Background(), phone),
		"emulator?": c.CleanStatusBar(context.Background(), device.Device{Platform: device.Android, Kind: device.Emulator, State: device.Offline, Name: "x"}),
	} {
		if code := apperr.CodeOf(err); code != apperr.Unsupported && code != apperr.InvalidState {
			t.Errorf("%s: %v", name, err)
		}
	}
	if len(fake.Calls()) != 0 {
		t.Errorf("the device was asked: %v", fake.Calls())
	}
}

func TestStatusBarGoesThroughDemoMode(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{
		"settings put global sysui_demo_allowed 1":  {},
		"am broadcast -a com.android.systemui.demo": runtest.Out("Broadcast completed: result=0\n"),
	})
	c := New(fake, sdk(t, "adb"))
	if err := c.CleanStatusBar(context.Background(), runningEmulator); err != nil {
		t.Fatal(err)
	}
	calls := strings.Join(fake.Calls(), "\n")
	for _, want := range []string{
		"settings put global sysui_demo_allowed 1",
		"-e command enter",
		"-e command clock -e hhmm 1200",
		"-e command battery -e level 100 -e plugged false",
		"-e command network -e wifi show -e level 4 -e fully true",
		"-e command notifications -e visible false",
	} {
		if !strings.Contains(calls, want) {
			t.Errorf("did not run %q:\n%s", want, calls)
		}
	}
	if err := c.ResetStatusBar(context.Background(), runningEmulator); err != nil || !fake.Called("-e command exit") {
		t.Errorf("reset: %v, calls %v", err, fake.Calls())
	}
}

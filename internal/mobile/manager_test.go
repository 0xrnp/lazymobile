package mobile

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/0xrnp/lazymobile/internal/android"
	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/run"
	"github.com/0xrnp/lazymobile/internal/run/runtest"
)

func manager(t *testing.T, goos string) (*Manager, *runtest.Fake) {
	t.Helper()
	fake := &runtest.Fake{}
	opts := android.Options{
		OS: goos, Home: t.TempDir(), Getenv: func(string) string { return "" },
		LookPath:     func(string) (string, error) { return "", errors.New("not found") },
		SDKFallbacks: []string{}, JavaFallbacks: []string{},
	}
	return New(fake, opts, goos), fake
}

func TestRealDevicesAreLeftAlone(t *testing.T) {
	m, fake := manager(t, "darwin")
	ctx := context.Background()
	for _, phone := range []device.Device{
		{ID: "UDID", Name: "My iPhone", Platform: device.IOS, Kind: device.Physical, State: device.Connected},
		{ID: "R58M", Name: "My Galaxy", Platform: device.Android, Kind: device.Physical, State: device.Connected},
	} {
		for name, err := range map[string]error{
			"boot":     m.Boot(ctx, phone, BootOptions{}),
			"shutdown": m.Shutdown(ctx, phone),
			"erase":    m.Erase(ctx, phone),
			"delete":   m.Delete(ctx, phone),
		} {
			if apperr.CodeOf(err) != apperr.Unsupported {
				t.Errorf("%s %s: error = %v, want unsupported", name, phone.Name, err)
			}
		}
	}
	if len(fake.Calls()) != 0 {
		t.Errorf("tools ran for a real device: %v", fake.Calls())
	}
}

func TestLinuxDoesNotLookForXcode(t *testing.T) {
	m, fake := manager(t, "linux")
	_, errs := m.List(context.Background())
	if fake.Called("xcrun") {
		t.Error("ran xcrun on Linux")
	}
	for _, err := range errs {
		if apperr.CodeOf(err) != apperr.ToolMissing {
			t.Errorf("unexpected error: %v", err)
		}
	}
}

func TestWipingASimulatorPointsAtErase(t *testing.T) {
	m, _ := manager(t, "darwin")
	sim := device.Device{ID: "UDID", Name: "iPhone 16", Platform: device.IOS, Kind: device.Simulator}
	err := m.Boot(context.Background(), sim, BootOptions{Wipe: true})
	if apperr.CodeOf(err) != apperr.Unsupported {
		t.Errorf("error = %v", err)
	}
}

func TestAppInfoAsksTheToolOfTheDevicesPlatform(t *testing.T) {
	fake := &runtest.Fake{Answer: func(command string, argv []string, _ run.Options) run.Result {
		switch {
		case strings.Contains(command, "dumpsys package com.example.app"):
			return runtest.Out("Packages:\n  Package [com.example.app] (aa):\n    versionCode=37 minSdk=24\n    versionName=0.0.47\n")
		case strings.Contains(command, "devicectl device info apps --device UDID"):
			apps := `{"result": {"apps": [{"bundleIdentifier": "com.example.app", "name": "Example", "version": "1.4.2", "bundleVersion": "87"}]}}`
			if err := os.WriteFile(argv[len(argv)-1], []byte(apps), 0o600); err != nil {
				t.Error(err)
			}
			return run.Result{}
		}
		return run.Result{Missing: true}
	}}
	opts := android.Options{
		OS: "darwin", Home: t.TempDir(), Getenv: func(string) string { return "" },
		LookPath:     func(string) (string, error) { return "/usr/bin/adb", nil },
		SDKFallbacks: []string{}, JavaFallbacks: []string{},
	}
	m := New(fake, opts, "darwin")
	ctx := context.Background()

	emulator := device.Device{ID: "Pixel_9a", Name: "Pixel 9a", Platform: device.Android, Kind: device.Emulator, State: device.Booted, Serial: "emulator-5554"}
	if app, err := m.AppInfo(ctx, emulator, "com.example.app"); err != nil || app != (device.App{ID: "com.example.app", Version: "0.0.47", Build: "37"}) {
		t.Errorf("an emulator: %+v, %v", app, err)
	}

	iphone := device.Device{ID: "UDID", Name: "My iPhone", Platform: device.IOS, Kind: device.Physical, State: device.Connected}
	if app, err := m.AppInfo(ctx, iphone, "com.example.app"); err != nil || app != (device.App{ID: "com.example.app", Name: "Example", Version: "1.4.2", Build: "87"}) {
		t.Errorf("an iPhone: %+v, %v", app, err)
	}
	if _, err := m.AppInfo(ctx, iphone, "com.example.other"); apperr.CodeOf(err) != apperr.NotFound {
		t.Errorf("an app the iPhone does not list: %v", err)
	}
}

// uninstallManager answers the lists and the uninstall calls of both platforms. Both iOS tools
// report success for an app that is not there, as the real ones do.
func uninstallManager(t *testing.T) (*Manager, *runtest.Fake) {
	t.Helper()
	fake := &runtest.Fake{Answer: func(command string, argv []string, _ run.Options) run.Result {
		switch {
		case strings.Contains(command, "pm list packages -3"):
			return runtest.Out("package:com.example.app\n")
		case strings.Contains(command, "devicectl device info apps"), strings.Contains(command, "devicectl device uninstall app"):
			apps := `{"result": {"apps": [{"bundleIdentifier": "com.example.app", "name": "Example"}]}}`
			if err := os.WriteFile(argv[len(argv)-1], []byte(apps), 0o600); err != nil {
				t.Error(err)
			}
			return run.Result{}
		case strings.Contains(command, "adb -s emulator-5554 uninstall"):
			return runtest.Out("Success\n")
		}
		return run.Result{}
	}}
	opts := android.Options{
		OS: "darwin", Home: t.TempDir(), Getenv: func(string) string { return "" },
		LookPath:     func(string) (string, error) { return "/usr/bin/adb", nil },
		SDKFallbacks: []string{}, JavaFallbacks: []string{},
	}
	return New(fake, opts, "darwin"), fake
}

func TestUninstallRemovesAnInstalledApp(t *testing.T) {
	m, fake := uninstallManager(t)
	ctx := context.Background()
	emulator := device.Device{ID: "Pixel_9a", Name: "Pixel 9a", Platform: device.Android, Kind: device.Emulator, State: device.Booted, Serial: "emulator-5554"}
	iphone := device.Device{ID: "UDID", Name: "My iPhone", Platform: device.IOS, Kind: device.Physical, State: device.Connected}

	if err := m.Uninstall(ctx, emulator, "com.example.app"); err != nil || !fake.Called("adb -s emulator-5554 uninstall com.example.app") {
		t.Errorf("an emulator: %v, ran %v", err, fake.Calls())
	}
	if err := m.Uninstall(ctx, iphone, "com.example.app"); err != nil || !fake.Called("devicectl device uninstall app --device UDID com.example.app") {
		t.Errorf("an iPhone: %v, ran %v", err, fake.Calls())
	}
}

func TestUninstallOfWhatIsNotAUserAppRunsNothing(t *testing.T) {
	m, fake := uninstallManager(t)
	ctx := context.Background()
	emulator := device.Device{ID: "Pixel_9a", Name: "Pixel 9a", Platform: device.Android, Kind: device.Emulator, State: device.Booted, Serial: "emulator-5554"}
	iphone := device.Device{ID: "UDID", Name: "My iPhone", Platform: device.IOS, Kind: device.Physical, State: device.Connected}
	stopped := emulator
	stopped.State = device.Shutdown

	// A typo, and an app that comes with the system, are the same case: it is not in the user's list.
	for name, err := range map[string]error{
		"a typo on an emulator": m.Uninstall(ctx, emulator, "com.example.ap"),
		"a typo on an iPhone":   m.Uninstall(ctx, iphone, "com.example.ap"),
		"a system app":          m.Uninstall(ctx, emulator, "com.android.chrome"),
	} {
		if apperr.CodeOf(err) != apperr.NotFound {
			t.Errorf("%s: error = %v, want not_found", name, err)
		}
	}
	if err := m.Uninstall(ctx, stopped, "com.example.app"); apperr.CodeOf(err) != apperr.InvalidState {
		t.Errorf("a stopped emulator: %v", err)
	}
	if err := m.Uninstall(ctx, emulator, "a; reboot"); apperr.CodeOf(err) != apperr.InvalidState {
		t.Errorf("an injected id: %v", err)
	}
	if fake.Called("uninstall") {
		t.Errorf("an uninstall ran: %v", fake.Calls())
	}
}

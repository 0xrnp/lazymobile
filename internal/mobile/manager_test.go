package mobile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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

func TestLaunchAndStopGoToTheDevicesPlatform(t *testing.T) {
	m, fake := uninstallManager(t)
	ctx := context.Background()
	emulator := device.Device{ID: "Pixel_9a", Name: "Pixel 9a", Platform: device.Android, Kind: device.Emulator, State: device.Booted, Serial: "emulator-5554"}
	simulator := device.Device{ID: "SIM", Name: "iPhone 17", Platform: device.IOS, Kind: device.Simulator, State: device.Booted}
	iphone := device.Device{ID: "UDID", Name: "My iPhone", Platform: device.IOS, Kind: device.Physical, State: device.Connected}

	for name, err := range map[string]error{
		"launch on an emulator":  m.Launch(ctx, emulator, "com.example.app", LaunchOptions{}),
		"restart on a simulator": m.Launch(ctx, simulator, "com.example.app", LaunchOptions{Restart: true}),
		"stop on a simulator":    m.Stop(ctx, simulator, "com.example.app"),
	} {
		if err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for _, want := range []string{
		"adb -s emulator-5554 shell monkey -p com.example.app",
		"xcrun simctl launch --terminate-running-process SIM com.example.app",
		"xcrun simctl terminate SIM com.example.app",
	} {
		if !fake.Called(want) {
			t.Errorf("did not run %q: %v", want, fake.Calls())
		}
	}
	if err := m.Stop(ctx, iphone, "com.example.app"); apperr.CodeOf(err) != apperr.Unsupported {
		t.Errorf("stop on an iPhone: %v", err)
	}
}

func TestLaunchAndStopRefuseAStoppedDeviceOrABadID(t *testing.T) {
	m, fake := uninstallManager(t)
	ctx := context.Background()
	stopped := device.Device{ID: "SIM", Name: "iPhone 17", Platform: device.IOS, Kind: device.Simulator, State: device.Shutdown}
	running := stopped
	running.State = device.Booted
	for name, err := range map[string]error{
		"launch on a stopped device": m.Launch(ctx, stopped, "com.example.app", LaunchOptions{}),
		"stop on a stopped device":   m.Stop(ctx, stopped, "com.example.app"),
		"launch of an injected id":   m.Launch(ctx, running, "a; reboot", LaunchOptions{}),
		"stop of an option as an id": m.Stop(ctx, running, "--all"),
	} {
		if apperr.CodeOf(err) != apperr.InvalidState {
			t.Errorf("%s: %v, want invalid_state", name, err)
		}
	}
	if calls := fake.Calls(); len(calls) != 0 {
		t.Errorf("a tool ran: %v", calls)
	}
}

// twoSimulators answers `simctl list` with a booted and a shut-down simulator, and nothing else.
func twoSimulators(t *testing.T) (*Manager, *runtest.Fake) {
	t.Helper()
	m, _ := manager(t, "darwin")
	fake := &runtest.Fake{Answer: func(command string, argv []string, _ run.Options) run.Result {
		switch {
		case strings.Contains(command, "simctl list devices"):
			return runtest.Out(`{"devices": {"com.apple.CoreSimulator.SimRuntime.iOS-18-2": [
  {"udid": "00000000-0000-4000-8000-0000000000A1", "name": "iPhone 16", "state": "Shutdown", "isAvailable": true},
  {"udid": "00000000-0000-4000-8000-0000000000A2", "name": "iPhone 16 Pro", "state": "Booted", "isAvailable": true}
]}}`)
		case strings.Contains(command, "devicectl list devices"):
			if err := os.WriteFile(argv[len(argv)-1], []byte(`{"result": {"devices": []}}`), 0o600); err != nil {
				t.Error(err)
			}
		}
		return run.Result{}
	}}
	m.Runner, m.IOS.Runner = fake, fake
	return m, fake
}

func TestFindMeansOneDeviceOrExplainsWhy(t *testing.T) {
	m, _ := twoSimulators(t)
	ctx := context.Background()

	if d, err := m.Find(ctx, "iphone 16 pro"); err != nil || d.ID != "00000000-0000-4000-8000-0000000000A2" {
		t.Errorf("an exact name: %+v, %v", d, err)
	}
	if d, err := m.Find(ctx, "00000000-0000-4000-8000-0000000000a1"); err != nil || d.Name != "iPhone 16" {
		t.Errorf("an id in another case: %+v, %v", d, err)
	}

	if d, err := m.Find(ctx, "pro"); err != nil || d.Name != "iPhone 16 Pro" {
		t.Errorf("a part of a name that fits one device: %+v, %v", d, err)
	}

	// There is no Android SDK here, so a miss blames the tool that failed, which is the likelier
	// reason a device is not in the list.
	_, err := m.Find(ctx, "pixel")
	var e *apperr.Error
	if !errors.As(err, &e) || e.Code != apperr.NotFound || !strings.Contains(e.Hint, "Some tools failed") {
		t.Errorf("no match: %v", err)
	}
	_, err = m.Find(ctx, "iphone")
	if !errors.As(err, &e) || e.Code != apperr.InvalidState || !strings.Contains(e.Hint, "00000000-0000-4000-8000-0000000000A1") ||
		!strings.Contains(e.Hint, "00000000-0000-4000-8000-0000000000A2") {
		t.Errorf("a part that matches both must list their ids: %v", err)
	}
}

func TestBootOfASimulatorRefusesAWipe(t *testing.T) {
	m, fake := twoSimulators(t)
	sim := device.Device{ID: "SIM", Name: "iPhone 16", Platform: device.IOS, Kind: device.Simulator, State: device.Shutdown}
	err := m.Boot(context.Background(), sim, BootOptions{Wipe: true})
	if apperr.CodeOf(err) != apperr.Unsupported || fake.Called("simctl boot") {
		t.Errorf("error = %v, calls = %v", err, fake.Calls())
	}
}

func TestInstallChecksTheDeviceAndTheKindOfFileBeforeAnyTool(t *testing.T) {
	m, fake := manager(t, "darwin")
	ctx := context.Background()
	emulator := device.Device{ID: "Pixel_9a", Name: "Pixel 9a", Platform: device.Android, Kind: device.Emulator, State: device.Booted, Serial: "emulator-5554"}
	simulator := device.Device{ID: "SIM", Name: "iPhone 16", Platform: device.IOS, Kind: device.Simulator, State: device.Booted}
	stopped := simulator
	stopped.State = device.Shutdown

	for name, err := range map[string]error{
		"a device that is not running": m.Install(ctx, stopped, "/tmp/App.app"),
		"an .app on Android":           m.Install(ctx, emulator, "/tmp/App.app"),
		"an .apk on a simulator":       m.Install(ctx, simulator, "/tmp/app.apk"),
		"a file with no extension":     m.Install(ctx, emulator, "/tmp/app"),
	} {
		if apperr.CodeOf(err) != apperr.InvalidState {
			t.Errorf("%s: %v, want invalid_state", name, err)
		}
	}
	if len(fake.Calls()) != 0 {
		t.Errorf("a tool ran: %v", fake.Calls())
	}

	// The extension is matched whatever its case, and a trailing slash on an .app folder is fine.
	m, fake = twoSimulators(t)
	app := filepath.Join(t.TempDir(), "App.APP")
	if err := os.Mkdir(app, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := m.Install(ctx, simulator, app+"/"); err != nil || !fake.Called("simctl install SIM "+app+"/") {
		t.Errorf("an .app folder: %v, calls = %v", err, fake.Calls())
	}
	if err := m.Install(ctx, simulator, "/nowhere/App.app"); apperr.CodeOf(err) != apperr.NotFound {
		t.Errorf("a path with nothing there: %v", err)
	}
}

func TestOpenURLOnlyOpensWebAddressesWithTheSystemsOpener(t *testing.T) {
	for goos, opener := range map[string]string{"darwin": "open", "linux": "xdg-open"} {
		m, fake := manager(t, goos)
		fake.Answer = func(string, []string, run.Options) run.Result { return run.Result{} }
		if err := m.OpenURL(context.Background(), "https://expo.dev/builds/1"); err != nil {
			t.Errorf("%s: %v", goos, err)
		}
		if !fake.Called(opener + " https://expo.dev/builds/1") {
			t.Errorf("%s: ran %v, want %s", goos, fake.Calls(), opener)
		}
	}
	m, fake := manager(t, "darwin")
	for _, url := range []string{"http://expo.dev", "file:///etc/passwd", "-a Calculator", ""} {
		if err := m.OpenURL(context.Background(), url); apperr.CodeOf(err) != apperr.InvalidState {
			t.Errorf("%q: %v, want invalid_state", url, err)
		}
	}
	if len(fake.Calls()) != 0 {
		t.Errorf("a tool ran: %v", fake.Calls())
	}
}

func TestOnlyDarwinHasIOS(t *testing.T) {
	if m, _ := manager(t, "darwin"); !m.HasIOS() {
		t.Error("macOS has iOS")
	}
	if m, _ := manager(t, "linux"); m.HasIOS() {
		t.Error("Linux has no iOS")
	}
}

func TestValidLinkNeedsASchemeAndNothingThatWouldBreakAShell(t *testing.T) {
	for _, link := range []string{"https://example.com", "myapp://home?id=1&x=2#top", "exp+myapp://x", "tel:+1555", "mailto:a@b.c"} {
		if !ValidLink(link) {
			t.Errorf("%q should be a link", link)
		}
	}
	for _, link := range []string{"", "example.com", "://x", "-a Calculator", "myapp://a b", "myapp://a\nb", "myapp://a\x00b", "1app://x", "https://" + strings.Repeat("a", 3000)} {
		if ValidLink(link) {
			t.Errorf("%q should not be a link", link)
		}
	}
}

func TestOpenLinkChecksTheDeviceAndTheLinkBeforeAnyTool(t *testing.T) {
	m, fake := uninstallManager(t)
	ctx := context.Background()
	emulator := device.Device{ID: "Pixel_9a", Name: "Pixel 9a", Platform: device.Android, Kind: device.Emulator, State: device.Booted, Serial: "emulator-5554"}
	simulator := device.Device{ID: "SIM", Name: "iPhone 17", Platform: device.IOS, Kind: device.Simulator, State: device.Booted}
	stopped := simulator
	stopped.State = device.Shutdown
	for name, err := range map[string]error{
		"a stopped device":      m.OpenLink(ctx, stopped, "https://example.com"),
		"a link with a space":   m.OpenLink(ctx, emulator, "myapp://a b"),
		"an option as a link":   m.OpenLink(ctx, simulator, "-a Calculator"),
		"a link with a newline": m.OpenLink(ctx, emulator, "myapp://a\nreboot"),
	} {
		if apperr.CodeOf(err) != apperr.InvalidState {
			t.Errorf("%s: %v, want invalid_state", name, err)
		}
	}
	if len(fake.Calls()) != 0 {
		t.Errorf("a tool ran: %v", fake.Calls())
	}
	if err := m.OpenLink(ctx, emulator, "myapp://home?a=1&b=2"); err != nil || !fake.Called("adb -s emulator-5554 shell am start") {
		t.Errorf("an emulator: %v, calls %v", err, fake.Calls())
	}
	if err := m.OpenLink(ctx, simulator, "myapp://home"); err != nil || !fake.Called("xcrun simctl openurl SIM myapp://home") {
		t.Errorf("a simulator: %v, calls %v", err, fake.Calls())
	}
}

func TestScreenshotNameIsSafeForAFileSystem(t *testing.T) {
	d := device.Device{Name: "iPad Pro 13-inch (M5)"}
	got := ScreenshotName(d, time.Date(2026, 11, 16, 7, 50, 32, 0, time.UTC))
	if got != "lazymobile-iPad-Pro-13-inch--M5-20261116-075032.png" {
		t.Errorf("name = %q", got)
	}
	if got := ScreenshotName(device.Device{Name: "../etc/x"}, time.Time{}); strings.ContainsAny(got, "/\\") {
		t.Errorf("name = %q", got)
	}
}

// screenshotManager answers screencap with a PNG and simctl with a file, as the real tools do.
func screenshotManager(t *testing.T) (*Manager, *runtest.Fake) {
	t.Helper()
	m, _ := manager(t, "darwin")
	fake := &runtest.Fake{Answer: func(command string, argv []string, _ run.Options) run.Result {
		switch {
		case strings.Contains(command, "exec-out screencap -p"):
			return runtest.Out("\x89PNG\r\n\x1a\nbytes")
		case strings.Contains(command, "simctl io"):
			if err := os.WriteFile(argv[len(argv)-1], []byte("\x89PNG\r\n\x1a\n"), 0o644); err != nil {
				t.Error(err)
			}
		}
		return run.Result{}
	}}
	m.Runner, m.IOS.Runner = fake, fake
	opts := android.Options{
		OS: "darwin", Home: t.TempDir(), Getenv: func(string) string { return "" },
		LookPath:     func(string) (string, error) { return "/usr/bin/adb", nil },
		SDKFallbacks: []string{}, JavaFallbacks: []string{},
	}
	m.Android = android.New(fake, opts)
	return m, fake
}

func TestScreenshotIsSavedForTheUserAlone(t *testing.T) {
	m, _ := screenshotManager(t)
	dir := t.TempDir()
	emulator := device.Device{ID: "Pixel_9a", Name: "Pixel 9a", Platform: device.Android, Kind: device.Emulator, State: device.Booted, Serial: "emulator-5554"}
	simulator := device.Device{ID: "SIM", Name: "iPhone 17", Platform: device.IOS, Kind: device.Simulator, State: device.Booted}
	for _, d := range []device.Device{emulator, simulator} {
		path, err := m.Screenshot(context.Background(), d, filepath.Join(dir, string(d.Platform)+".png"))
		if err != nil {
			t.Fatalf("%s: %v", d.Name, err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 || info.Size() == 0 {
			t.Errorf("%s: file %v, error %v", d.Name, info, err)
		}
	}
}

func TestScreenshotNeverReplacesAFileOrWritesAnywhereUnexpected(t *testing.T) {
	m, fake := screenshotManager(t)
	dir := t.TempDir()
	emulator := device.Device{ID: "Pixel_9a", Name: "Pixel 9a", Platform: device.Android, Kind: device.Emulator, State: device.Booted, Serial: "emulator-5554"}
	stopped := emulator
	stopped.State = device.Shutdown
	existing := filepath.Join(dir, "mine.png")
	if err := os.WriteFile(existing, []byte("precious"), 0o600); err != nil {
		t.Fatal(err)
	}

	for name, err := range map[string]error{
		"a file that exists": second(m.Screenshot(context.Background(), emulator, existing)),
		"a missing folder":   second(m.Screenshot(context.Background(), emulator, filepath.Join(dir, "no", "x.png"))),
		"a stopped device":   second(m.Screenshot(context.Background(), stopped, filepath.Join(dir, "a.png"))),
	} {
		code := apperr.CodeOf(err)
		if code != apperr.InvalidState && code != apperr.Unsupported {
			t.Errorf("%s: %v", name, err)
		}
	}
	if got, _ := os.ReadFile(existing); string(got) != "precious" {
		t.Errorf("the file was replaced: %q", got)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("files left behind: %v", entries)
	}
	if fake.Called("simctl io") {
		t.Errorf("a tool ran for a refused screenshot: %v", fake.Calls())
	}
}

func second[T any](_ T, err error) error { return err }

func TestAppearanceChecksTheDeviceAndTheModeBeforeAnyTool(t *testing.T) {
	m, fake := uninstallManager(t)
	ctx := context.Background()
	simulator := device.Device{ID: "SIM", Name: "iPhone 17", Platform: device.IOS, Kind: device.Simulator, State: device.Booted}
	stopped := simulator
	stopped.State = device.Shutdown
	_, readErr := m.Appearance(ctx, stopped)
	for name, err := range map[string]error{
		"reading a stopped device": readErr,
		"setting a stopped device": m.SetAppearance(ctx, stopped, "dark"),
		"a mode that is not one":   m.SetAppearance(ctx, simulator, "sepia"),
		"an option as a mode":      m.SetAppearance(ctx, simulator, "-h"),
	} {
		if apperr.CodeOf(err) != apperr.InvalidState {
			t.Errorf("%s: %v, want invalid_state", name, err)
		}
	}
	if len(fake.Calls()) != 0 {
		t.Errorf("a tool ran: %v", fake.Calls())
	}
	if err := m.SetAppearance(ctx, simulator, "light"); err != nil || !fake.Called("xcrun simctl ui SIM appearance light") {
		t.Errorf("a simulator: %v, calls %v", err, fake.Calls())
	}
}

func TestParseLocationAcceptsOnlyPlacesOnTheGlobe(t *testing.T) {
	for text, want := range map[string][2]float64{
		"37.7749,-122.4194":  {37.7749, -122.4194},
		" 37.7749 , -122.4 ": {37.7749, -122.4},
		"-90,180":            {-90, 180},
		"0,0":                {0, 0},
	} {
		if lat, lon, err := ParseLocation(text); err != nil || lat != want[0] || lon != want[1] {
			t.Errorf("%q = %v, %v, %v", text, lat, lon, err)
		}
	}
	for _, text := range []string{"", "37.7", "a,b", "1,2,3", "91,0", "0,181", "-91,0", "NaN,0", "0,Inf", "37.7749 -122.4194"} {
		if _, _, err := ParseLocation(text); apperr.CodeOf(err) != apperr.InvalidState {
			t.Errorf("%q: %v, want invalid_state", text, err)
		}
	}
}

func TestLocationAndStatusBarLeaveRealPhonesAloneAndCheckBeforeAnyTool(t *testing.T) {
	m, fake := uninstallManager(t)
	ctx := context.Background()
	simulator := device.Device{ID: "SIM", Name: "iPhone 17", Platform: device.IOS, Kind: device.Simulator, State: device.Booted}
	emulator := device.Device{ID: "Pixel_9a", Name: "Pixel 9a", Platform: device.Android, Kind: device.Emulator, State: device.Booted, Serial: "emulator-5554"}
	iphone := device.Device{ID: "UDID", Name: "My iPhone", Platform: device.IOS, Kind: device.Physical, State: device.Connected}
	phone := device.Device{ID: "R58M", Name: "My Galaxy", Platform: device.Android, Kind: device.Physical, State: device.Connected, Serial: "R58M"}
	stopped := simulator
	stopped.State = device.Shutdown

	for name, err := range map[string]error{
		"location of an iPhone":     m.SetLocation(ctx, iphone, 1, 2),
		"location of a phone":       m.SetLocation(ctx, phone, 1, 2),
		"clear of a phone":          m.ClearLocation(ctx, phone),
		"status bar of an iPhone":   m.SetStatusBar(ctx, iphone, true),
		"status bar of a phone":     m.SetStatusBar(ctx, phone, false),
		"location of a stopped sim": m.SetLocation(ctx, stopped, 1, 2),
		"a latitude off the globe":  m.SetLocation(ctx, simulator, 999, 2),
		"clearing an emulator":      m.ClearLocation(ctx, emulator),
		"a push to an emulator":     m.SendPush(ctx, emulator, "com.example.app", []byte(`{"aps": {}}`)),
		"a push to an iPhone":       m.SendPush(ctx, iphone, "com.example.app", []byte(`{"aps": {}}`)),
		"a push to a stopped sim":   m.SendPush(ctx, stopped, "com.example.app", []byte(`{"aps": {}}`)),
	} {
		if code := apperr.CodeOf(err); code != apperr.Unsupported && code != apperr.InvalidState {
			t.Errorf("%s: %v", name, err)
		}
	}
	if len(fake.Calls()) != 0 {
		t.Errorf("a tool ran: %v", fake.Calls())
	}
	if err := m.SetLocation(ctx, simulator, 37.7, -122.4); err != nil || !fake.Called("simctl location SIM set 37.7,-122.4") {
		t.Errorf("a simulator: %v, calls %v", err, fake.Calls())
	}
	if err := m.SetLocation(ctx, emulator, 37.7, -122.4); err != nil || !fake.Called("emu geo fix -122.4 37.7") {
		t.Errorf("an emulator: %v, calls %v", err, fake.Calls())
	}
	if err := m.SetStatusBar(ctx, simulator, false); err != nil || !fake.Called("simctl status_bar SIM clear") {
		t.Errorf("status bar: %v, calls %v", err, fake.Calls())
	}
}

func TestPermissionsAndClearDataLeaveRealPhonesAloneAndCheckBeforeAnyTool(t *testing.T) {
	m, fake := uninstallManager(t)
	ctx := context.Background()
	emulator := device.Device{ID: "Pixel_9a", Name: "Pixel 9a", Platform: device.Android, Kind: device.Emulator, State: device.Booted, Serial: "emulator-5554"}
	iphone := device.Device{ID: "UDID", Name: "My iPhone", Platform: device.IOS, Kind: device.Physical, State: device.Connected}
	phone := device.Device{ID: "R58M", Name: "My Galaxy", Platform: device.Android, Kind: device.Physical, State: device.Connected, Serial: "R58M"}
	stopped := emulator
	stopped.State = device.Shutdown

	for name, err := range map[string]error{
		"a permission of an iPhone app":   m.SetPermission(ctx, iphone, "com.example.app", "photos", true),
		"a permission of a phone's app":   m.SetPermission(ctx, phone, "com.example.app", "camera", true),
		"data of a phone's app":           m.ClearData(ctx, phone, "com.example.app"),
		"a permission on a stopped one":   m.SetPermission(ctx, stopped, "com.example.app", "camera", true),
		"data on a stopped one":           m.ClearData(ctx, stopped, "com.example.app"),
		"an injected app id":              m.SetPermission(ctx, emulator, "a; reboot", "camera", true),
		"an option as an app id for data": m.ClearData(ctx, emulator, "-a"),
	} {
		if code := apperr.CodeOf(err); code != apperr.Unsupported && code != apperr.InvalidState {
			t.Errorf("%s: %v", name, err)
		}
	}
	if len(fake.Calls()) != 0 {
		t.Errorf("a tool ran: %v", fake.Calls())
	}
	if err := m.SetPermission(ctx, emulator, "com.example.app", "camera", true); err != nil || !fake.Called("pm grant com.example.app android.permission.CAMERA") {
		t.Errorf("an emulator: %v, calls %v", err, fake.Calls())
	}
}

func TestPermissionNamesDifferByPlatform(t *testing.T) {
	ios, android := PermissionNames(device.IOS), PermissionNames(device.Android)
	if !slices.Contains(ios, "photos") || slices.Contains(ios, "camera") {
		t.Errorf("iOS: %v (a simulator has no camera permission)", ios)
	}
	if !slices.Contains(android, "camera") || !slices.Contains(android, "notifications") {
		t.Errorf("Android: %v", android)
	}
}

func TestRecordingNamesFollowThePlatformsVideoFormat(t *testing.T) {
	at := time.Date(2026, 11, 16, 7, 50, 32, 0, time.UTC)
	if got := RecordingName(device.Device{Name: "iPhone 17", Platform: device.IOS}, at); got != "lazymobile-iPhone-17-20261116-075032.mov" {
		t.Errorf("iOS: %q", got)
	}
	if got := RecordingName(device.Device{Name: "Pixel 9a", Platform: device.Android}, at); got != "lazymobile-Pixel-9a-20261116-075032.mp4" {
		t.Errorf("Android: %q", got)
	}
}

func TestRecordingIsSavedForTheUserAloneAndNeverReplacesAFile(t *testing.T) {
	m, fake := screenshotManager(t)
	dir := t.TempDir()
	simulator := device.Device{ID: "SIM", Name: "iPhone 17", Platform: device.IOS, Kind: device.Simulator, State: device.Booted}
	iphone := device.Device{ID: "UDID", Name: "My iPhone", Platform: device.IOS, Kind: device.Physical, State: device.Connected}
	stopped := simulator
	stopped.State = device.Shutdown
	ctx := context.Background()

	path, err := m.Record(ctx, simulator, filepath.Join(dir, "a.mov"), time.Second, nil)
	if err != nil || !fake.Called("simctl io SIM recordVideo "+path) {
		t.Fatalf("record: %v, calls %v", err, fake.Calls())
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("file %v, error %v", info, err)
	}

	before := len(fake.Calls())
	for name, err := range map[string]error{
		"a file that is there": func() error { _, err := m.Record(ctx, simulator, path, 0, nil); return err }(),
		"a folder that is not there": func() error {
			_, err := m.Record(ctx, simulator, filepath.Join(dir, "no", "a.mov"), 0, nil)
			return err
		}(),
		"a stopped device": func() error { _, err := m.Record(ctx, stopped, filepath.Join(dir, "b.mov"), 0, nil); return err }(),
		"a negative duration": func() error {
			_, err := m.Record(ctx, simulator, filepath.Join(dir, "c.mov"), -time.Second, nil)
			return err
		}(),
		"an iPhone": func() error { _, err := m.Record(ctx, iphone, filepath.Join(dir, "d.mov"), 0, nil); return err }(),
	} {
		if code := apperr.CodeOf(err); code != apperr.InvalidState && code != apperr.Unsupported {
			t.Errorf("%s: %v", name, err)
		}
	}
	if len(fake.Calls()) != before {
		t.Errorf("a tool ran: %v", fake.Calls()[before:])
	}
}

func TestRecordingAnnouncesItselfOnlyOnceTheFileNameIsAccepted(t *testing.T) {
	m, _ := screenshotManager(t)
	dir := t.TempDir()
	simulator := device.Device{ID: "SIM", Name: "iPhone 17", Platform: device.IOS, Kind: device.Simulator, State: device.Booted}
	started := 0
	if _, err := m.Record(context.Background(), simulator, filepath.Join(dir, "no", "a.mov"), 0, func() { started++ }); err == nil || started != 0 {
		t.Errorf("a bad folder: %v, announced %d times", err, started)
	}
	if _, err := m.Record(context.Background(), simulator, filepath.Join(dir, "a.mov"), time.Second, func() { started++ }); err != nil || started != 1 {
		t.Errorf("a good file: %v, announced %d times", err, started)
	}
}

func TestDevHelpersRefuseWhatTheyCannotDo(t *testing.T) {
	m, fake := manager(t, "darwin")
	ctx := context.Background()
	simulator := device.Device{ID: "SIM", Name: "iPhone 16", Platform: device.IOS, Kind: device.Simulator, State: device.Booted}
	stopped := device.Device{ID: "emulator-5554", Name: "Pixel", Platform: device.Android, Kind: device.Emulator, State: device.Shutdown}
	running := device.Device{ID: "emulator-5554", Name: "Pixel", Platform: device.Android, Kind: device.Emulator, State: device.Booted}
	for name, tc := range map[string]struct {
		d      device.Device
		action string
		port   int
		code   apperr.Code
	}{
		"a simulator":       {simulator, "reload", 8081, apperr.Unsupported},
		"a stopped device":  {stopped, "reload", 8081, apperr.InvalidState},
		"an unknown action": {running, "explode", 8081, apperr.Usage},
		"port zero":         {running, "reverse", 0, apperr.Usage},
		"port too big":      {running, "reverse", 70000, apperr.Usage},
	} {
		if err := m.Dev(ctx, tc.d, tc.action, tc.port); apperr.CodeOf(err) != tc.code {
			t.Errorf("%s: error = %v, want %s", name, err, tc.code)
		}
	}
	if len(fake.Calls()) != 0 {
		t.Errorf("a tool ran: %v", fake.Calls())
	}
}

func TestCrashRefusesWhatItCannotRead(t *testing.T) {
	m, fake := manager(t, "darwin")
	ctx := context.Background()
	phone := device.Device{ID: "00008150-0001", Name: "iPhone", Platform: device.IOS, Kind: device.Physical, State: device.Disconnected}
	stopped := device.Device{ID: "emulator-5554", Name: "Pixel", Platform: device.Android, Kind: device.Emulator, State: device.Shutdown}
	simulator := device.Device{ID: "SIM", Name: "iPhone 16", Platform: device.IOS, Kind: device.Simulator, State: device.Shutdown}
	for name, tc := range map[string]struct {
		d    device.Device
		app  string
		code apperr.Code
	}{
		"an iPhone not connected":   {phone, "com.example.app", apperr.InvalidState},
		"a stopped Android device":  {stopped, "com.example.app", apperr.InvalidState},
		"a simulator with a bad id": {simulator, "not an id; rm -rf", apperr.InvalidState},
	} {
		if _, err := m.Crash(ctx, tc.d, tc.app); apperr.CodeOf(err) != tc.code {
			t.Errorf("%s: error = %v, want %s", name, err, tc.code)
		}
	}
	if len(fake.Calls()) != 0 {
		t.Errorf("a tool ran: %v", fake.Calls())
	}
}

func TestSaveTextNeverReplacesAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crash.txt")
	saved, err := SaveText(path, "FATAL EXCEPTION: main")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(saved)
	if err != nil || string(data) != "FATAL EXCEPTION: main\n" {
		t.Errorf("file = %q, %v", data, err)
	}
	if info, err := os.Stat(saved); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, %v; want 0600", info.Mode().Perm(), err)
	}
	if _, err := SaveText(path, "again"); apperr.CodeOf(err) != apperr.InvalidState {
		t.Errorf("error = %v, want invalid_state for a file that exists", err)
	}
}

func TestAppUsageRefusesAnIPhoneAndABadIDBeforeAnyTool(t *testing.T) {
	m, fake := manager(t, "darwin")
	ctx := context.Background()
	phone := device.Device{ID: "00008150-0001", Name: "iPhone", Platform: device.IOS, Kind: device.Physical, State: device.Connected}
	simulator := device.Device{ID: "SIM", Name: "iPhone 16", Platform: device.IOS, Kind: device.Simulator, State: device.Shutdown}
	for name, tc := range map[string]struct {
		d    device.Device
		app  string
		code apperr.Code
	}{
		"an iPhone":             {phone, "com.example.app", apperr.Unsupported},
		"a stopped simulator":   {simulator, "com.example.app", apperr.InvalidState},
		"an id that is not one": {simulator, "x; rm -rf /", apperr.InvalidState},
	} {
		if _, err := m.AppUsage(ctx, tc.d, tc.app, 0); apperr.CodeOf(err) != tc.code {
			t.Errorf("%s: error = %v, want %s", name, err, tc.code)
		}
	}
	if len(fake.Calls()) != 0 {
		t.Errorf("a tool ran: %v", fake.Calls())
	}
}

func TestLinkNoteWarnsOnlyAboutADeepLinkOnASimulator(t *testing.T) {
	simulator := device.Device{Platform: device.IOS, Kind: device.Simulator}
	emulator := device.Device{Platform: device.Android, Kind: device.Emulator}
	for _, tc := range []struct {
		d    device.Device
		link string
		note bool
	}{
		{simulator, "myapp://home", true},
		{simulator, "https://example.com", false},
		{simulator, "HTTP://example.com", false},
		{emulator, "myapp://home", false},
	} {
		if got := LinkNote(tc.d, tc.link) != ""; got != tc.note {
			t.Errorf("%s on %s: note %v, want %v", tc.link, tc.d.Platform, got, tc.note)
		}
	}
}

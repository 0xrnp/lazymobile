package mobile

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
)

func touch(t *testing.T, path string, at time.Time, dir bool) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if dir {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	} else if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func TestBuildInPicksTheNewestBuildForTheDevice(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	emulator := device.Device{Name: "Pixel 9a", Platform: device.Android, Kind: device.Emulator}
	simulator := device.Device{Name: "iPhone 17", Platform: device.IOS, Kind: device.Simulator}
	apks := filepath.Join(root, "app", "build", "outputs", "apk")
	touch(t, filepath.Join(apks, "dev", "debug", "app-dev-debug.apk"), now.Add(-time.Hour), false)
	touch(t, filepath.Join(apks, "prod", "debug", "app-prod-debug.apk"), now.Add(-time.Minute), false)
	// A test APK is never what is installed, even when it is the newest.
	touch(t, filepath.Join(apks, "androidTest", "debug", "app-debug-androidTest.apk"), now, false)
	if got, err := BuildIn(apks, emulator); err != nil || got != filepath.Join(apks, "prod", "debug", "app-prod-debug.apk") {
		t.Errorf("android: %q, %v", got, err)
	}
	products := filepath.Join(root, "Products")
	touch(t, filepath.Join(products, "Debug-iphonesimulator", "Shop.app"), now.Add(-time.Hour), true)
	// An extension inside the app, and a build for devices, are newer but not for the simulator.
	touch(t, filepath.Join(products, "Debug-iphonesimulator", "Shop.app", "PlugIns", "Widget.appex", "Inner.app"), now, true)
	touch(t, filepath.Join(products, "Debug-iphoneos", "Shop.app"), now, true)
	if got, err := BuildIn(products, simulator); err != nil || got != filepath.Join(products, "Debug-iphonesimulator", "Shop.app") {
		t.Errorf("ios: %q, %v", got, err)
	}
	// A path to a build is used as it is, and Install checks it.
	if got, err := BuildIn("/nowhere/app.apk", emulator); err != nil || got != "/nowhere/app.apk" {
		t.Errorf("a build path: %q, %v", got, err)
	}
	// A path that is not there is left to Install, which says so.
	if got, err := BuildIn(filepath.Join(root, "missing"), emulator); err != nil || got != filepath.Join(root, "missing") {
		t.Errorf("a path that is not there: %q, %v", got, err)
	}
	if _, err := BuildIn(products, emulator); apperr.CodeOf(err) != apperr.NotFound {
		t.Errorf("a folder without an APK: %v", err)
	}
}

func TestBuildInFollowsALinkToTheFolder(t *testing.T) {
	root := t.TempDir()
	touch(t, filepath.Join(root, "builds", "debug", "app-debug.apk"), time.Now(), false)
	link := filepath.Join(root, "link")
	if err := os.Symlink(filepath.Join(root, "builds"), link); err != nil {
		t.Fatal(err)
	}
	emulator := device.Device{Name: "Pixel 9a", Platform: device.Android, Kind: device.Emulator}
	if got, err := BuildIn(link, emulator); err != nil || filepath.Base(got) != "app-debug.apk" {
		t.Errorf("%q, %v", got, err)
	}
}

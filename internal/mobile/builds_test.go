package mobile

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/0xrnp/lazymobile/internal/device"
)

func TestFindBuildsLooksInTheUsualPlacesAndSortsNewestFirst(t *testing.T) {
	root := t.TempDir()
	touch := func(path string, age time.Duration) {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-age)
		_ = os.Chtimes(full, at, at)
	}
	touch("android/app/build/outputs/apk/debug/app-debug.apk", time.Hour)
	touch("android/app/build/outputs/apk/debug/app-debug-androidTest.apk", 0)
	touch("build/app/outputs/flutter-apk/app-release.apk", time.Minute)
	touch("notes.txt", 0)
	for _, dir := range []string{"ios/build/Build/Products/Debug-iphonesimulator/App.app", "build/ios/iphoneos/Runner.app"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	phone := device.Device{Platform: device.Android}
	got := FindBuilds(root, phone)
	want := []string{filepath.Join(root, "build/app/outputs/flutter-apk/app-release.apk"), filepath.Join(root, "android/app/build/outputs/apk/debug/app-debug.apk")}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("android builds = %v, want %v", got, want)
	}
	if got := FindBuilds(root, device.Device{Platform: device.IOS, Kind: device.Simulator}); len(got) != 1 || filepath.Base(got[0]) != "App.app" {
		t.Errorf("simulator builds = %v", got)
	}
	if got := FindBuilds(root, device.Device{Platform: device.IOS, Kind: device.Physical}); len(got) != 1 || filepath.Base(got[0]) != "Runner.app" {
		t.Errorf("iPhone builds = %v", got)
	}
	if got := FindBuilds("", phone); got != nil {
		t.Errorf("no folder = %v", got)
	}
}

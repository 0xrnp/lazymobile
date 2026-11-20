package mobile

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/0xrnp/lazymobile/internal/android"
	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
)

// buildSearchDepth is how deep BuildIn looks in a folder: Gradle puts an APK in
// build/outputs/apk/<flavor>/<build type>.
const buildSearchDepth = 6

// BuildIn is the build to install on d from path: path itself when it is an .apk or an .app, else
// the newest one in the folder path, such as Gradle's app/build/outputs/apk.
func BuildIn(path string, d device.Device) (string, error) {
	// A build is left to Install, which checks the kind of file and that it is there.
	lower := strings.ToLower(strings.TrimRight(path, "/"))
	if strings.HasSuffix(lower, ".apk") || strings.HasSuffix(lower, ".app") {
		return path, nil
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return path, nil
	}
	// WalkDir does not follow a root that is a link to the folder, so the folder it links to is
	// walked, and the build is named under the path that was given.
	root, err := filepath.EvalSymlinks(path)
	if err != nil {
		root = filepath.Clean(path)
	}
	var newest string
	var newestAt time.Time
	_ = filepath.WalkDir(root, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() && p != root {
			if strings.Count(strings.TrimPrefix(p, root), string(filepath.Separator)) > buildSearchDepth || skipBuildFolder(entry.Name(), d) {
				return filepath.SkipDir
			}
		}
		name := strings.ToLower(entry.Name())
		app := d.Platform == device.IOS && entry.IsDir() && strings.HasSuffix(name, ".app")
		apk := d.Platform == device.Android && !entry.IsDir() && strings.HasSuffix(name, ".apk")
		if !app && !apk {
			return nil
		}
		if info, err := entry.Info(); err == nil && info.ModTime().After(newestAt) {
			newest, newestAt = p, info.ModTime()
		}
		if app {
			// An app holds its extensions, which are not what is installed.
			return filepath.SkipDir
		}
		return nil
	})
	if newest == "" {
		kind := "an .apk"
		if d.Platform == device.IOS {
			kind = "an .app"
		}
		return "", apperr.New(apperr.NotFound, "There is no build for %s in %s", d.Name, path).
			WithHint("Build it first, or give the path of %s.", kind)
	}
	if rel, err := filepath.Rel(root, newest); err == nil {
		return filepath.Join(path, rel), nil
	}
	return newest, nil
}

// skipBuildFolder leaves out the builds that are not for d: Android's test APKs, and an iOS build
// for the other kind of device.
func skipBuildFolder(name string, d device.Device) bool {
	switch {
	case d.Platform == device.Android:
		return name == "androidTest"
	case d.Kind == device.Physical:
		return strings.HasSuffix(name, "-iphonesimulator")
	}
	return strings.HasSuffix(name, "-iphoneos")
}

// BuiltAppID is the id of the app a build holds: the application id Gradle wrote beside an APK, or
// the bundle id in an .app's Info.plist.
func (m *Manager) BuiltAppID(ctx context.Context, d device.Device, build string) (string, error) {
	if d.Platform == device.Android {
		return android.BuiltAppID(build)
	}
	return m.IOS.BundleID(ctx, build)
}

// XcodeSchemes lists the schemes of an Xcode workspace or project. container is ["-workspace",
// path] or ["-project", path].
func (m *Manager) XcodeSchemes(ctx context.Context, container []string) ([]string, error) {
	if m.GOOS != "darwin" || m.IOS == nil {
		return nil, apperr.New(apperr.Unsupported, "Building an Xcode project needs a Mac")
	}
	return m.IOS.XcodeSchemes(ctx, container)
}

// XcodeBuiltApp is where xcodebuild puts the app that scheme builds for the simulator udid.
func (m *Manager) XcodeBuiltApp(ctx context.Context, container []string, scheme, udid string) (string, error) {
	if m.GOOS != "darwin" || m.IOS == nil {
		return "", apperr.New(apperr.Unsupported, "Building an Xcode project needs a Mac")
	}
	return m.IOS.XcodeBuiltApp(ctx, container, scheme, udid)
}

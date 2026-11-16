package mobile

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/0xrnp/lazymobile/internal/device"
)

// FindBuilds lists the builds under root that d can install, newest first: the places Android
// Studio, Gradle, Flutter, Xcode and React Native leave them, and root itself. It looks only in
// those places, so it is quick in any folder.
func FindBuilds(root string, d device.Device) []string {
	if root == "" {
		return nil
	}
	var patterns []string
	switch {
	case d.Platform == device.Android:
		patterns = []string{
			"*.apk",
			"app/build/outputs/apk/*/*.apk", "android/app/build/outputs/apk/*/*.apk",
			"build/app/outputs/flutter-apk/*.apk",
		}
	case d.Kind == device.Physical:
		patterns = []string{"*.app", "build/ios/iphoneos/*.app", "ios/build/Build/Products/*-iphoneos/*.app"}
	default:
		patterns = []string{"*.app", "build/ios/iphonesimulator/*.app", "ios/build/Build/Products/*-iphonesimulator/*.app"}
	}
	type found struct {
		path string
		time int64
	}
	var builds []found
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(filepath.Join(root, pattern))
		for _, path := range matches {
			// Test builds of an Android project are not the app.
			if strings.Contains(filepath.Base(path), "androidTest") {
				continue
			}
			if info, err := os.Stat(path); err == nil {
				builds = append(builds, found{path, info.ModTime().UnixNano()})
			}
		}
	}
	sort.SliceStable(builds, func(i, j int) bool { return builds[i].time > builds[j].time })
	paths := make([]string, 0, len(builds))
	for _, b := range builds {
		paths = append(paths, b.path)
	}
	return paths
}

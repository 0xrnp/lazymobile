package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
)

var (
	nativeEmulator  = device.Device{ID: "Pixel_9a", Name: "Pixel 9a", Serial: "emulator-5554", Platform: device.Android, Kind: device.Emulator, State: device.Booted}
	nativeSimulator = device.Device{ID: runningSim, Name: "iPhone 16 Pro", Platform: device.IOS, Kind: device.Simulator, State: device.Booted}
	nativeIPhone    = device.Device{ID: "00008000-000000000000001E", Name: "Someone's iPhone", Platform: device.IOS, Kind: device.Physical, State: device.Connected}
)

// gradleProject writes a Gradle project whose root build file declares the Android plugin for its
// modules, as Android Studio's template does, and whose app module applies it.
func gradleProject(t *testing.T, e *runEnv, appBuild string) {
	t.Helper()
	e.writeFiles(t, map[string]string{
		"gradlew":              "#!/bin/sh\n",
		"settings.gradle.kts":  `include(":app")`,
		"build.gradle.kts":     "plugins {\n    alias(libs.plugins.android.application) apply false\n}\n",
		"app/build.gradle.kts": appBuild,
		"lib/build.gradle.kts": "plugins {\n    alias(libs.plugins.android.library)\n}\n",
	})
}

const kotlinApp = "plugins {\n    alias(libs.plugins.android.application)\n    alias(libs.plugins.kotlin.android)\n}\n"

func (e *runEnv) writeFiles(t *testing.T, files map[string]string) {
	t.Helper()
	for name, text := range files {
		writeFile(t, filepath.Join(e.dir, name), text)
	}
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func shellScript(t *testing.T, argv []string, err error) string {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if len(argv) != 3 || argv[0] != "sh" || argv[1] != "-c" {
		t.Fatalf("argv %q", argv)
	}
	return argv[2]
}

func TestANativeAndroidAppIsBuiltWithGradleThenInstalledAndLaunched(t *testing.T) {
	e := newRunEnv(t)
	e.app.Executable = "/bin/lazymobile"
	gradleProject(t, e, kotlinApp)
	plans, err := e.app.runPlans(context.Background(), e.dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The app module only: the root declares the plugin, and lib is a library.
	if len(plans) != 1 || plans[0].name != "gradlew :app" || plans[0].platform != device.Android {
		t.Fatalf("plans %+v", plans)
	}
	argv, err := plans[0].argv(nativeEmulator)
	want := "./gradlew :app:assembleDebug && /bin/lazymobile install emulator-5554 app/build/outputs/apk/debug --launch"
	if got := shellScript(t, argv, err); got != want {
		t.Errorf("%s\nwant %s", got, want)
	}
	if _, err := plans[0].argv(nativeSimulator); apperr.CodeOf(err) != apperr.Unsupported {
		t.Errorf("on a simulator: %v", err)
	}
	// A task after -- is built instead, and its APK installed.
	plans, _ = e.app.runPlans(context.Background(), e.dir, []string{"assembleDevFreeDebug", "--offline"})
	argv, err = plans[0].argv(nativeEmulator)
	want = "./gradlew assembleDevFreeDebug --offline && /bin/lazymobile install emulator-5554 app/build/outputs/apk/devFree/debug --launch"
	if got := shellScript(t, argv, err); got != want {
		t.Errorf("%s\nwant %s", got, want)
	}
}

func TestAnAndroidAppWithFlavorsNeedsOneNamed(t *testing.T) {
	e := newRunEnv(t)
	gradleProject(t, e, kotlinApp+"android {\n    productFlavors {\n        create(\"staging\") { dimension = \"env\" }\n    }\n}\n")
	plans, _ := e.app.runPlans(context.Background(), e.dir, nil)
	if _, err := plans[0].argv(nativeEmulator); err == nil || !strings.Contains(err.Error(), "lazymobile run -- assembleStagingDebug") {
		t.Errorf("error %v", err)
	}
	// Groovy declares a flavor as a block.
	if got := productFlavor.FindStringSubmatch("productFlavors {\n        dev {\n"); got == nil || got[2] != "dev" {
		t.Errorf("groovy: %q", got)
	}
}

func TestANativeIOSAppIsBuiltWithXcodebuildThenInstalledAndLaunched(t *testing.T) {
	e := newRunEnv(t)
	e.app.Executable = "/bin/lazymobile"
	e.writeFiles(t, map[string]string{
		"Shop.xcodeproj/project.pbxproj":                                        "",
		"Shop.xcodeproj/xcshareddata/xcschemes/Shop.xcscheme":                   "",
		"Shop.xcodeproj/xcshareddata/xcschemes/ShopTests.xcscheme":              "",
		"Shop.xcodeproj/xcuserdata/me.xcuserdatad/xcschemes/Pods-Shop.xcscheme": "",
	})
	plans, err := e.app.runPlans(context.Background(), e.dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || plans[0].name != "xcodebuild Shop" || plans[0].platform != device.IOS {
		t.Fatalf("plans %+v", plans)
	}
	// The scheme files are enough: xcodebuild is not asked for the list.
	if e.fake.Called("xcodebuild -list") {
		t.Error("the schemes were listed with xcodebuild")
	}
	argv, err := plans[0].argv(nativeSimulator)
	want := "xcodebuild -project Shop.xcodeproj -scheme Shop -configuration Debug -destination id=" + runningSim +
		" build && /bin/lazymobile install " + runningSim + " /DD/Debug-iphonesimulator/Shop.app --launch"
	if got := shellScript(t, argv, err); got != want {
		t.Errorf("%s\nwant %s", got, want)
	}
	// The path of the build is asked once per device, also when run starts the device first.
	if _, err := plans[0].argv(nativeSimulator); err != nil || strings.Count(strings.Join(e.fake.Calls(), "\n"), "-showBuildSettings") != 1 {
		t.Errorf("asked again: %v, %q", err, e.fake.Calls())
	}
	if _, err := plans[0].argv(nativeIPhone); apperr.CodeOf(err) != apperr.Unsupported {
		t.Errorf("an iPhone: %v", err)
	}
}

func TestAnXcodeProjectWithoutSchemeFilesAsksXcodebuild(t *testing.T) {
	e := newRunEnv(t)
	e.writeFiles(t, map[string]string{"Shop.xcodeproj/project.pbxproj": ""})
	plans, err := e.app.runPlans(context.Background(), e.dir, nil)
	if err != nil || len(plans) != 1 || plans[0].name != "xcodebuild Shop" || !e.fake.Called("xcodebuild -list -json -project "+filepath.Join(e.dir, "Shop.xcodeproj")) {
		t.Errorf("plans %+v, %v, calls %q", plans, err, e.fake.Calls())
	}
}

func TestRunShowsTheNativeBuildAsItsOwnCommand(t *testing.T) {
	e := newRunEnv(t)
	e.app.Executable = "/bin/lazymobile"
	e.writeFiles(t, map[string]string{"Shop.xcodeproj/xcshareddata/xcschemes/Shop.xcscheme": ""})
	if code := e.run("run", "iPhone 16 Pro"); code != ExitOK {
		t.Fatalf("code %d, stderr %q", code, e.errs.String())
	}
	if len(e.ran) != 1 || e.ran[0][0] != "sh" || !strings.Contains(e.errs.String(), "Running `xcodebuild -project Shop.xcodeproj -scheme Shop") {
		t.Errorf("ran %q, stderr %q", e.ran, e.errs.String())
	}
}

func TestAFolderWithAnAndroidAndAnIOSAppRunsEachOnItsPlatform(t *testing.T) {
	e := newRunEnv(t)
	gradleProject(t, e, kotlinApp)
	e.writeFiles(t, map[string]string{"iosApp.xcodeproj/xcshareddata/xcschemes/iosApp.xcscheme": ""})
	plans, err := e.app.runPlans(context.Background(), e.dir, nil)
	if err != nil || len(plans) != 2 {
		t.Fatalf("plans %+v, %v", plans, err)
	}
	if got := plansFor(plans, device.IOS); len(got) != 1 || got[0].name != "xcodebuild iosApp" {
		t.Errorf("for iOS: %+v", got)
	}
	if got := plansFor(plans, device.Android); len(got) != 1 || got[0].name != "gradlew :app" {
		t.Errorf("for Android: %+v", got)
	}
}

func TestApkVariantFolder(t *testing.T) {
	for task, want := range map[string]string{
		"assembleDebug":             "debug",
		":app:assembleRelease":      "release",
		"assembleDevDebug":          "dev/debug",
		":app:assembleDevFreeDebug": "devFree/debug",
		"installDebug":              "",
		"assemble":                  "",
	} {
		if got := apkVariantFolder(task); got != filepath.FromSlash(want) {
			t.Errorf("%s: %q, want %q", task, got, want)
		}
	}
}

func TestShellJoinQuotesWhatTheShellWouldSplit(t *testing.T) {
	got := shellJoin([]string{"/Apps/lazymobile", "install", "emulator-5554", "/My Builds/it's.app", "id=X", "$HOME"})
	want := `/Apps/lazymobile install emulator-5554 '/My Builds/it'\''s.app' id=X '$HOME'`
	if got != want {
		t.Errorf("%s\nwant %s", got, want)
	}
}

func TestAKotlinMultiplatformProjectRunsOnBothPlatforms(t *testing.T) {
	e := newRunEnv(t)
	e.app.Executable = "/bin/lazymobile"
	// The layout of the Kotlin Multiplatform wizard's project.
	e.writeFiles(t, map[string]string{
		"gradlew":                     "#!/bin/sh\n",
		"build.gradle.kts":            "plugins {\n    alias(libs.plugins.androidApplication) apply false\n    alias(libs.plugins.kotlinMultiplatform) apply false\n}\n",
		"composeApp/build.gradle.kts": "plugins {\n    alias(libs.plugins.kotlinMultiplatform)\n    alias(libs.plugins.androidApplication)\n}\n",
		"shared/build.gradle.kts":     "plugins {\n    alias(libs.plugins.androidLibrary)\n}\n",
		"iosApp/iosApp.xcodeproj/xcshareddata/xcschemes/iosApp.xcscheme": "",
	})
	plans, err := e.app.runPlans(context.Background(), e.dir, nil)
	if err != nil || len(plans) != 2 || plans[0].name != "gradlew :composeApp" || plans[1].name != "xcodebuild iosApp" {
		t.Fatalf("plans %+v, %v", plans, err)
	}
	argv, err := plans[1].argv(nativeSimulator)
	if got := shellScript(t, argv, err); !strings.HasPrefix(got, "xcodebuild -project iosApp/iosApp.xcodeproj -scheme iosApp ") {
		t.Errorf("script %s", got)
	}
}

func TestAnXcodeProjectThatCannotBeReadLeavesTheAndroidApp(t *testing.T) {
	e := newRunEnv(t)
	gradleProject(t, e, kotlinApp)
	e.writeFiles(t, map[string]string{"Shop.xcodeproj/project.pbxproj": ""})
	e.app.Manager.GOOS = "linux"
	plans, err := e.app.runPlans(context.Background(), e.dir, nil)
	if err != nil || len(plans) != 1 || plans[0].name != "gradlew :app" {
		t.Errorf("plans %+v, %v", plans, err)
	}
}

func TestProductFlavorsAreFoundInEachWayOfDeclaringThem(t *testing.T) {
	for text, want := range map[string]string{
		"productFlavors {\n    create(\"staging\") {":                                      "staging",
		"productFlavors {\n    register(\"qa\") {":                                         "qa",
		"productFlavors {\n    // the servers\n    flavorDimensions += \"env\"\n    dev {": "dev",
	} {
		m := productFlavor.FindStringSubmatch(text)
		if m == nil || m[1]+m[2] != want {
			t.Errorf("%q: %q, want %s", text, m, want)
		}
	}
}

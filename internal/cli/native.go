package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/state"
)

// nativePlans runs a native Android app with the project's Gradle wrapper and a native iOS app with
// xcodebuild, then installs the build and launches it with lazymobile, in one shell command that is
// shown before it runs. A folder with both, such as a Kotlin Multiplatform project, offers each on
// its own platform.
func (a *App) nativePlans(ctx context.Context, folder string, extra []string) ([]runPlan, error) {
	plans := a.gradlePlans(folder, extra)
	xcode, err := a.xcodePlans(ctx, folder, extra)
	if err != nil {
		// An Xcode project that cannot be read, or a computer that is not a Mac, still leaves the
		// Android app to run.
		if len(plans) > 0 {
			return plans, nil
		}
		return nil, err
	}
	return append(plans, xcode...), nil
}

// gradlePlans offers each Android app module of a Gradle project: the folder itself, or a folder in
// it, whose build file applies the Android application plugin.
func (a *App) gradlePlans(folder string, extra []string) []runPlan {
	if info, err := os.Stat(filepath.Join(folder, "gradlew")); err != nil || info.IsDir() {
		return nil
	}
	var modules []string
	if androidAppModule(folder) {
		modules = append(modules, "")
	}
	entries, _ := os.ReadDir(folder)
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && e.Name() != "build" && androidAppModule(filepath.Join(folder, e.Name())) {
			modules = append(modules, e.Name())
		}
	}
	var plans []runPlan
	for _, module := range modules {
		name, task := "gradlew", "assembleDebug"
		if module != "" {
			name, task = "gradlew :"+module, ":"+module+":assembleDebug"
		}
		plans = append(plans, runPlan{name: name, dir: folder, from: "gradlew", platform: device.Android,
			argv: func(d device.Device) ([]string, error) {
				if d.Platform != device.Android {
					return nil, apperr.New(apperr.Unsupported, "Gradle builds the Android app, and %s is %s", d.Name, d.Platform)
				}
				task, rest := task, extra
				// A task given after --, such as assembleDevDebug for a flavor, is built instead.
				if len(extra) > 0 && !strings.HasPrefix(extra[0], "-") {
					task, rest = extra[0], extra[1:]
				} else if flavor := firstFlavor(filepath.Join(folder, module)); flavor != "" {
					// assembleDebug builds every flavor, and which one to launch is the user's choice.
					return nil, usageError{fmt.Sprintf("The app has product flavors, so name the one to build after --, such as: lazymobile run -- assemble%sDebug", strings.ToUpper(flavor[:1])+flavor[1:])}
				}
				apks := filepath.Join(module, "build", "outputs", "apk", apkVariantFolder(task))
				build := shellJoin(append([]string{"./gradlew", task}, rest...))
				install := shellJoin([]string{a.executable(), "install", d.TargetID(), apks, "--launch"})
				return []string{"sh", "-c", build + " && " + install}, nil
			}})
	}
	return plans
}

// androidAppModule reports whether the build file in dir applies the Android application plugin:
// by its id, a version catalog alias (android.application, or androidApplication in the Kotlin
// Multiplatform template), or a convention plugin named after it. A root build file that only
// declares it (`apply false`) for its modules does not count.
func androidAppModule(dir string) bool {
	for _, name := range []string{"build.gradle.kts", "build.gradle"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		for line := range strings.Lines(string(data)) {
			if (strings.Contains(line, "android.application") || strings.Contains(line, "androidApplication")) &&
				!strings.Contains(line, "apply false") && !strings.HasPrefix(strings.TrimSpace(line), "//") {
				return true
			}
		}
	}
	return false
}

// productFlavor is the first flavor declared in a build file: create("dev") or register("dev") in
// Kotlin, or a block such as dev { ... } in Groovy, after any comment or flavorDimensions line.
var productFlavor = regexp.MustCompile(`productFlavors\s*\{(?:\s*(?://[^\n]*|flavorDimensions[^\n]*))*\s*(?:(?:create|register|maybeCreate)\("(\w+)"\)|(\w+)\s*\{)`)

// firstFlavor is the first product flavor of the module in dir, or "" when it has none.
func firstFlavor(dir string) string {
	for _, name := range []string{"build.gradle.kts", "build.gradle"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		if m := productFlavor.FindSubmatch(data); m != nil {
			return string(m[1]) + string(m[2])
		}
	}
	return ""
}

// apkVariantFolder is where the Android Gradle plugin puts the APK of an assemble task, inside
// build/outputs/apk: debug for assembleDebug, dev/debug for assembleDevDebug, devFree/debug for
// assembleDevFreeDebug. Another task gives "", the whole folder, whose newest APK is installed.
func apkVariantFolder(task string) string {
	name := task[strings.LastIndex(task, ":")+1:]
	variant, ok := strings.CutPrefix(name, "assemble")
	if !ok || variant == "" {
		return ""
	}
	var words []string
	for i, r := range variant {
		if i == 0 || unicode.IsUpper(r) {
			words = append(words, "")
		}
		words[len(words)-1] += string(r)
	}
	buildType := strings.ToLower(words[len(words)-1])
	if len(words) == 1 {
		return buildType
	}
	flavor := strings.Join(words[:len(words)-1], "")
	return filepath.Join(strings.ToLower(flavor[:1])+flavor[1:], buildType)
}

// xcodePlans offers each scheme of the folder's Xcode workspace, or else its project. The schemes
// are read from the scheme files, which is instant; only a project without any asks xcodebuild,
// which takes a few seconds.
func (a *App) xcodePlans(ctx context.Context, folder string, extra []string) ([]runPlan, error) {
	flag, container := xcodeContainer(folder)
	if container == "" {
		return nil, nil
	}
	absolute := []string{flag, filepath.Join(folder, container)}
	schemes := schemeFiles(folder, container)
	if len(schemes) == 0 {
		var err error
		if schemes, err = a.Manager.XcodeSchemes(ctx, absolute); err != nil {
			return nil, err
		}
	}
	schemes = slices.DeleteFunc(schemes, func(s string) bool {
		// CocoaPods adds a scheme for each pod, and test schemes build no app.
		return strings.HasPrefix(s, "Pods-") || strings.HasSuffix(s, "Tests")
	})
	if len(schemes) == 0 {
		return nil, apperr.New(apperr.NotFound, "%s has no scheme that builds an app", container).
			WithHint("Open it in Xcode, which makes one, or mark the app's scheme as shared.")
	}
	var plans []runPlan
	for _, scheme := range schemes {
		builtApps := map[string]string{}
		plans = append(plans, runPlan{name: "xcodebuild " + scheme, dir: folder, from: container, platform: device.IOS,
			argv: func(d device.Device) ([]string, error) {
				switch {
				case d.Platform != device.IOS:
					return nil, apperr.New(apperr.Unsupported, "Xcode builds the iOS app, and %s is %s", d.Name, d.Platform)
				case d.Kind == device.Physical:
					return nil, apperr.New(apperr.Unsupported, "lazymobile builds for simulators only: an iPhone needs code signing").
						WithHint("Run it on %s from Xcode, or pick a simulator.", d.Name)
				}
				app, ok := builtApps[d.ID]
				if !ok {
					fmt.Fprintf(a.Stderr, "Asking Xcode where it builds %s...\n", scheme)
					var err error
					if app, err = a.Manager.XcodeBuiltApp(ctx, absolute, scheme, d.ID); err != nil {
						return nil, err
					}
					builtApps[d.ID] = app
				}
				argv := []string{"xcodebuild", flag, container, "-scheme", scheme, "-configuration", "Debug", "-destination", "id=" + d.ID}
				build := shellJoin(append(append(argv, extra...), "build"))
				install := shellJoin([]string{a.executable(), "install", d.ID, app, "--launch"})
				return []string{"sh", "-c", build + " && " + install}, nil
			}})
	}
	return plans, nil
}

// xcodeContainer is the folder's Xcode workspace, which CocoaPods makes and which holds the project,
// else its project, as the xcodebuild flag and the path from folder. One that is not in folder
// itself is looked for a level down, where a Kotlin Multiplatform project keeps iosApp.
func xcodeContainer(folder string) (flag, path string) {
	places := []string{"."}
	entries, _ := os.ReadDir(folder)
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && !strings.Contains(e.Name(), ".xc") &&
			!slices.Contains([]string{"node_modules", "Pods", "build"}, e.Name()) {
			places = append(places, e.Name())
		}
	}
	for _, place := range places {
		for _, kind := range [][2]string{{"-workspace", "*.xcworkspace"}, {"-project", "*.xcodeproj"}} {
			if found, _ := filepath.Glob(filepath.Join(folder, place, kind[1])); len(found) > 0 {
				slices.Sort(found)
				return kind[0], filepath.Join(place, filepath.Base(found[0]))
			}
		}
	}
	return "", ""
}

// schemeFiles are the schemes saved in the workspace or project, shared or the user's own, and for
// a workspace also those of the projects beside it.
func schemeFiles(folder, container string) []string {
	places := []string{container}
	if strings.HasSuffix(container, ".xcworkspace") {
		projects, _ := filepath.Glob(filepath.Join(folder, filepath.Dir(container), "*.xcodeproj"))
		for _, p := range projects {
			places = append(places, filepath.Join(filepath.Dir(container), filepath.Base(p)))
		}
	}
	var schemes []string
	for _, place := range places {
		for _, pattern := range []string{"xcshareddata/xcschemes/*.xcscheme", "xcuserdata/*.xcuserdatad/xcschemes/*.xcscheme"} {
			found, _ := filepath.Glob(filepath.Join(folder, place, pattern))
			for _, f := range found {
				schemes = append(schemes, strings.TrimSuffix(filepath.Base(f), ".xcscheme"))
			}
		}
	}
	slices.Sort(schemes)
	return slices.Compact(schemes)
}

// safeWord is an argument the shell reads as it is.
var safeWord = regexp.MustCompile(`^[A-Za-z0-9_./:=@%+,-]+$`)

// shellJoin is argv as one shell command line, each argument quoted when it needs to be.
func shellJoin(argv []string) string {
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		if safeWord.MatchString(arg) {
			quoted[i] = arg
		} else {
			quoted[i] = "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
		}
	}
	return strings.Join(quoted, " ")
}

// followAgainWithin is how recent a run in this folder must be for an installed build to be
// followed again. A first native build can take many minutes.
const followAgainWithin = time.Hour

// rememberLaunchedBuild records the app `install --launch` started, as `launch` does, and as this
// folder's app. A native run learns its app only once it is built, after a dashboard following the
// run has looked, so a recent run on the device is marked again for the dashboard to open its log.
func (a *App) rememberLaunchedBuild(d device.Device, id string) {
	folder, _ := a.getwd()
	_ = a.State.Update(func(f *state.File) {
		f.RememberApp(d.Platform, id)
		if folder == "" {
			return
		}
		f.RememberFolderApp(folder, d.Platform, id)
		if run := f.Runs[folder]; run.Last == d.Key() && time.Since(run.Started) < followAgainWithin {
			f.RememberRunStarted(folder, d.Key(), time.Now())
		}
	})
}

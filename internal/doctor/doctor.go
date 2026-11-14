// Package doctor finds the tools each platform needs and says what is missing and how to fix
// it. Nothing here fails: a missing or broken tool becomes a Problem in the report.
package doctor

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/0xrnp/lazymobile/internal/android"
	"github.com/0xrnp/lazymobile/internal/run"
)

const toolTimeout = 10 * time.Second

// Severity ranks a problem.
type Severity string

const (
	// Wrong means something is set up wrongly.
	Wrong Severity = "error"
	// Unavailable means a platform cannot be used because something is not installed.
	Unavailable Severity = "warning"
	// Optional means a feature is missing, and the rest works.
	Optional Severity = "info"
)

// Problem is one thing to fix.
type Problem struct {
	Code     string   `json:"code"`
	Platform string   `json:"platform"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
	Hint     string   `json:"hint,omitempty"`
}

// Found is a tool that is present.
type Found struct {
	Name   string `json:"name"`
	Detail string `json:"detail"`
}

// Report is everything doctor learned.
type Report struct {
	OS       string    `json:"os"`
	Found    []Found   `json:"found"`
	Problems []Problem `json:"problems"`
}

// Healthy reports whether nothing is set up wrongly. Missing platforms do not count.
func (r Report) Healthy() bool {
	for _, p := range r.Problems {
		if p.Severity == Wrong {
			return false
		}
	}
	return true
}

const switchHint = "sudo xcode-select --switch /Applications/Xcode.app/Contents/Developer"

var xcodeVersion = regexp.MustCompile(`Xcode\s+(\S+)`)

func succeeded(res run.Result) bool {
	return res.Code == 0 && !res.Missing && res.Err == nil && !res.TimedOut
}

func xcode(ctx context.Context, r run.Runner, report *Report) {
	problem := func(code string, severity Severity, message, hint string) {
		report.Problems = append(report.Problems, Problem{Code: code, Platform: "ios", Severity: severity, Message: message, Hint: hint})
	}
	selected := r.Run(ctx, []string{"xcode-select", "-p"}, run.Options{Timeout: toolTimeout})
	dir := strings.TrimSpace(selected.Stdout)
	if !succeeded(selected) || dir == "" {
		problem("xcode_missing", Unavailable, "Xcode is not installed",
			"Install Xcode from the App Store, open it once, then run: "+switchHint)
		return
	}
	if strings.Contains(dir, "CommandLineTools") {
		problem("xcode_clt_only", Unavailable,
			"Only the Command Line Tools are installed, and simulators need the full Xcode app",
			"Install Xcode from the App Store, then run: "+switchHint)
		return
	}

	commands := [][]string{
		{"xcrun", "--find", "simctl"},
		{"xcrun", "--find", "devicectl"},
		{"xcodebuild", "-version"},
	}
	results := make([]run.Result, len(commands))
	var wg sync.WaitGroup
	for i, argv := range commands {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = r.Run(ctx, argv, run.Options{Timeout: toolTimeout})
		}()
	}
	wg.Wait()
	simctl, devicectl, version := results[0], results[1], results[2]

	if succeeded(version) {
		detail := dir
		if m := xcodeVersion.FindStringSubmatch(version.Stdout); m != nil {
			detail = m[1] + " (" + dir + ")"
		}
		report.Found = append(report.Found, Found{"Xcode", detail})
	} else if strings.Contains(strings.ToLower(version.Stderr+version.Stdout), "license") {
		problem("xcode_license", Unavailable, "The Xcode license has not been accepted",
			"Accept it once with: sudo xcodebuild -license accept")
	}
	if !succeeded(simctl) {
		problem("simctl_missing", Unavailable, "simctl was not found in the selected Xcode",
			"Open Xcode once to finish installing its components, then run: "+switchHint)
	}
	if !succeeded(devicectl) {
		problem("devicectl_missing", Optional, "Physical iOS devices are unavailable", "They need Xcode 15 or newer.")
	}
}

func androidSDK(ctx context.Context, r run.Runner, opts android.Options, report *Report) {
	problem := func(code string, severity Severity, message, hint string) {
		report.Problems = append(report.Problems, Problem{Code: code, Platform: "android", Severity: severity, Message: message, Hint: hint})
	}
	sdk, candidates := opts.SDKDir()

	// A location the user set explicitly that does not exist is a setup mistake, not a gap.
	var looked []string
	for _, c := range candidates {
		looked = append(looked, c.Path)
		if c.Source != "default" && c.Path != sdk {
			if !android.IsDir(c.Path) {
				problem("android_sdk_invalid", Wrong, c.Source+" points to "+c.Path+", which does not exist",
					"Fix it or unset it. The SDK is usually in Android Studio > Settings > Android SDK.")
			}
		}
	}

	tools := map[string]android.Tool{}
	for _, name := range []string{"emulator", "adb", "avdmanager"} {
		if tool, ok := opts.FindTool(name); ok {
			tools[name] = tool
			report.Found = append(report.Found, Found{name, tool.Path})
		}
	}
	if sdk == "" && len(tools) == 0 {
		where := "nowhere (no usual location on this OS)"
		if len(looked) > 0 {
			where = strings.Join(looked, ", ")
		}
		problem("android_sdk_missing", Unavailable, "The Android SDK was not found",
			"Install Android Studio or the command-line tools, or set ANDROID_HOME. Looked in: "+where)
		return
	}
	if sdk != "" {
		report.Found = append(report.Found, Found{"Android SDK", sdk})
	}
	if _, ok := tools["emulator"]; !ok {
		problem("android_emulator_missing", Unavailable, "The Android emulator is not installed", `Install it with: sdkmanager "emulator"`)
	}
	if _, ok := tools["adb"]; !ok {
		problem("android_adb_missing", Unavailable, "adb is not installed, so physical Android devices are unavailable",
			`Install it with: sdkmanager "platform-tools"`)
	}
	if _, ok := tools["avdmanager"]; !ok {
		problem("android_avdmanager_missing", Optional, "avdmanager is not installed, so new Android devices cannot be created",
			`Install the command-line tools with: sdkmanager "cmdline-tools;latest"`)
		return
	}
	if java, ok := opts.FindJava(ctx, r); ok {
		report.Found = append(report.Found, Found{"Java", java.Version + " (" + java.Path + ")"})
	} else {
		problem("java_missing", Optional, "No working Java was found, and avdmanager needs one",
			"Install a JDK, or install Android Studio, which bundles one.")
	}
}

// Check looks for the tools of both platforms. On an OS other than macOS, iOS is left out.
func Check(ctx context.Context, r run.Runner, opts android.Options, goos string) Report {
	report := Report{OS: goos}
	var ios, droid Report
	var wg sync.WaitGroup
	if goos == "darwin" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			xcode(ctx, r, &ios)
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		androidSDK(ctx, r, opts, &droid)
	}()
	wg.Wait()
	report.Found = append(ios.Found, droid.Found...)
	report.Problems = append(ios.Problems, droid.Problems...)
	return report
}

// Package android drives Android emulators (emulator, avdmanager) and devices (adb), on macOS
// and Linux.
package android

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/run"
)

// Options say where to find the Android tools. The zero value uses the environment and the usual
// install locations.
type Options struct {
	// SDK is the Android SDK location. It wins over PATH and the environment.
	SDK string
	// JavaHome is a JDK for avdmanager.
	JavaHome string
	// AVDHome is where AVDs live. Default: the emulator's own lookup order.
	AVDHome string
	// EmulatorArgs are added to every emulator start.
	EmulatorArgs []string
	// Headless starts emulators without a window.
	Headless bool

	// The fields below exist for tests.

	// OS is "darwin" or "linux". Default: this machine.
	OS string
	// Getenv reads the environment. Default: os.Getenv.
	Getenv func(string) string
	// Home is the user's home directory. Default: os.UserHomeDir.
	Home string
	// LookPath finds a program on PATH. Default: exec.LookPath.
	LookPath func(string) (string, error)
	// SDKFallbacks replaces the usual install locations.
	SDKFallbacks []string
	// JavaFallbacks replaces the JDK homes tried after PATH.
	JavaFallbacks []string
	// LogDir is where emulator output is kept. Default: the user cache directory.
	LogDir string
}

func (o Options) os() string {
	if o.OS != "" {
		return o.OS
	}
	return runtime.GOOS
}

func (o Options) getenv(key string) string {
	if o.Getenv != nil {
		return o.Getenv(key)
	}
	return os.Getenv(key)
}

func (o Options) home() string {
	if o.Home != "" {
		return o.Home
	}
	home, _ := os.UserHomeDir()
	return home
}

func (o Options) lookPath(name string) string {
	look := o.LookPath
	if look == nil {
		look = exec.LookPath
	}
	path, err := look(name)
	if err != nil {
		return ""
	}
	return path
}

// SDKCandidate is a place an SDK was looked for, and why.
type SDKCandidate struct {
	Path string
	// Source is "config", "ANDROID_HOME", "ANDROID_SDK_ROOT" or "default".
	Source string
}

func (o Options) sdkCandidates() []SDKCandidate {
	var list []SDKCandidate
	seen := map[string]bool{}
	add := func(path, source string) {
		if path == "" {
			return
		}
		path = filepath.Clean(path)
		if !seen[path] {
			seen[path] = true
			list = append(list, SDKCandidate{path, source})
		}
	}
	add(o.SDK, "config")
	add(o.getenv("ANDROID_HOME"), "ANDROID_HOME")
	add(o.getenv("ANDROID_SDK_ROOT"), "ANDROID_SDK_ROOT")

	if o.SDKFallbacks != nil {
		for _, path := range o.SDKFallbacks {
			add(path, "default")
		}
		return list
	}
	home := o.home()
	switch o.os() {
	case "darwin":
		add(filepath.Join(home, "Library/Android/sdk"), "default")
		add("/opt/homebrew/share/android-commandlinetools", "default")
		add("/usr/local/share/android-commandlinetools", "default")
	case "linux":
		add(filepath.Join(home, "Android/Sdk"), "default")
		add("/usr/lib/android-sdk", "default")
	}
	return list
}

// IsDir reports whether path is an existing directory.
func IsDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}

// SDKDir returns the first existing SDK directory, and every location that was considered.
func (o Options) SDKDir() (string, []SDKCandidate) {
	candidates := o.sdkCandidates()
	for _, c := range candidates {
		if IsDir(c.Path) {
			return c.Path, candidates
		}
	}
	return "", candidates
}

var cmdlineVersion = regexp.MustCompile(`^(\d+)\.?(\d*)`)

// cmdlineRank orders command-line tools directories: "latest" first, then by version.
func cmdlineRank(dir string) float64 {
	name := filepath.Base(dir)
	if name == "latest" {
		return 1e9
	}
	m := cmdlineVersion.FindStringSubmatch(name)
	if m == nil {
		return 0
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return float64(major) + float64(minor)/1000
}

func sdkToolPaths(sdk, name string) []string {
	switch name {
	case "emulator":
		return []string{filepath.Join(sdk, "emulator", "emulator")}
	case "adb":
		return []string{filepath.Join(sdk, "platform-tools", "adb")}
	}
	// avdmanager and sdkmanager live in the command-line tools, or in the legacy tools folder.
	dirs, _ := filepath.Glob(filepath.Join(sdk, "cmdline-tools", "*"))
	sort.SliceStable(dirs, func(i, j int) bool { return cmdlineRank(dirs[i]) > cmdlineRank(dirs[j]) })
	var paths []string
	for _, dir := range dirs {
		paths = append(paths, filepath.Join(dir, "bin", name))
	}
	return append(paths, filepath.Join(sdk, "tools", "bin", name))
}

// Tool is where an SDK tool was found.
type Tool struct {
	Path string
	// Via is "sdk" or "path".
	Via string
}

// FindTool finds "emulator", "adb", "avdmanager" or "sdkmanager". An SDK given in the options
// wins, then PATH, then the SDK from the environment and the usual locations.
func (o Options) FindTool(name string) (Tool, bool) {
	fromSDKs := func(configured bool) (Tool, bool) {
		for _, c := range o.sdkCandidates() {
			if (c.Source == "config") != configured || !IsDir(c.Path) {
				continue
			}
			for _, path := range sdkToolPaths(c.Path, name) {
				if isExecutable(path) {
					return Tool{path, "sdk"}, true
				}
			}
		}
		return Tool{}, false
	}
	if tool, ok := fromSDKs(true); ok {
		return tool, true
	}
	if path := o.lookPath(name); path != "" {
		return Tool{path, "path"}, true
	}
	return fromSDKs(false)
}

var installHint = map[string]string{
	"emulator":   `Install it with: sdkmanager "emulator", or in Android Studio > SDK Manager > SDK Tools.`,
	"adb":        `Install it with: sdkmanager "platform-tools", or in Android Studio > SDK Manager > SDK Tools.`,
	"avdmanager": `Install the command-line tools: sdkmanager "cmdline-tools;latest", or in Android Studio > SDK Manager > SDK Tools.`,
}

// tool is the path of an SDK tool, or an error saying how to install it.
func (o Options) tool(name string) (string, error) {
	if found, ok := o.FindTool(name); ok {
		return found.Path, nil
	}
	return "", apperr.New(apperr.ToolMissing, "%s was not found", name).WithHint("%s", installHint[name])
}

// Java is a working JDK.
type Java struct {
	Path string
	// Home is set when it came from a known JDK home, so tools can be pointed at it.
	Home    string
	Version string
	Major   int
}

var javaVersion = regexp.MustCompile(`version "([^"]+)"`)
var javaMajor = regexp.MustCompile(`^(\d+)\.?(\d*)`)

// ParseJavaVersion reads the output of `java -version`.
func ParseJavaVersion(output string) (version string, major int, ok bool) {
	m := javaVersion.FindStringSubmatch(output)
	if m == nil {
		return "", 0, false
	}
	parts := javaMajor.FindStringSubmatch(m[1])
	if parts == nil {
		return "", 0, false
	}
	major, _ = strconv.Atoi(parts[1])
	// Java 8 and older report "1.8.0".
	if major == 1 {
		major, _ = strconv.Atoi(parts[2])
	}
	return m[1], major, major > 0
}

// FindJava returns the first working JDK. A bare `java` on PATH may be a stub (macOS ships one
// that only prints an error), so every candidate is run.
func (o Options) FindJava(ctx context.Context, r run.Runner) (Java, bool) {
	type candidate struct{ java, home string }
	var list []candidate
	seen := map[string]bool{}
	add := func(java, home string) {
		if java != "" && !seen[java] {
			seen[java] = true
			list = append(list, candidate{java, home})
		}
	}
	addHome := func(home string) {
		if home != "" {
			home = filepath.Clean(home)
			add(filepath.Join(home, "bin", "java"), home)
		}
	}
	addHome(o.JavaHome)
	addHome(o.getenv("JAVA_HOME"))
	add(o.lookPath("java"), "")
	if o.JavaFallbacks != nil {
		for _, home := range o.JavaFallbacks {
			addHome(home)
		}
	} else {
		switch o.os() {
		case "darwin":
			addHome("/Applications/Android Studio.app/Contents/jbr/Contents/Home")
			addHome("/Applications/Android Studio Preview.app/Contents/jbr/Contents/Home")
		case "linux":
			addHome(filepath.Join(o.home(), "android-studio/jbr"))
			addHome("/opt/android-studio/jbr")
			addHome("/usr/local/android-studio/jbr")
		}
	}

	for _, c := range list {
		res := r.Run(ctx, []string{c.java, "-version"}, run.Options{Timeout: 10 * time.Second})
		// `java -version` prints to stderr.
		version, major, ok := ParseJavaVersion(res.Stderr + "\n" + res.Stdout)
		if res.Code == 0 && !res.Missing && res.Err == nil && !res.TimedOut && ok {
			return Java{Path: c.java, Home: c.home, Version: version, Major: major}, true
		}
	}
	return Java{}, false
}

// AVDDir is where AVDs live, in the emulator's own lookup order.
func (o Options) AVDDir() string {
	switch {
	case o.AVDHome != "":
		return o.AVDHome
	case o.getenv("ANDROID_AVD_HOME") != "":
		return o.getenv("ANDROID_AVD_HOME")
	case o.getenv("ANDROID_USER_HOME") != "":
		return filepath.Join(o.getenv("ANDROID_USER_HOME"), "avd")
	case o.getenv("ANDROID_SDK_HOME") != "":
		return filepath.Join(o.getenv("ANDROID_SDK_HOME"), ".android", "avd")
	}
	return filepath.Join(o.home(), ".android", "avd")
}

// readINI reads a key=value file, as AVDs use. An empty value counts as unset, so callers fall
// back to their defaults.
func readINI(path string) map[string]string {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if found && key != "" && value != "" {
			values[key] = value
		}
	}
	return values
}

// AVDInfo is what an AVD's config says about it. Fields are empty when the files are missing.
type AVDInfo struct {
	DisplayName string
	API         string
	DeviceType  string
}

var apiLevel = regexp.MustCompile(`android-(\d+)`)

// AVDInfo reads an AVD's config files.
func (o Options) AVDInfo(name string) AVDInfo {
	home := o.AVDDir()
	pointer := readINI(filepath.Join(home, name+".ini"))
	dir := pointer["path"]
	if dir == "" || !IsDir(dir) {
		dir = filepath.Join(home, name+".avd")
	}
	config := readINI(filepath.Join(dir, "config.ini"))
	target := pointer["target"]
	if target == "" {
		target = config["target"]
	}
	api := ""
	if m := apiLevel.FindStringSubmatch(target); m != nil {
		api = m[1]
	} else if m := apiLevel.FindStringSubmatch(config["image.sysdir.1"]); m != nil {
		api = m[1]
	}
	return AVDInfo{DisplayName: config["avd.ini.displayname"], API: api, DeviceType: config["hw.device.name"]}
}

// normalizeArch names a CPU the way the SDK does.
func normalizeArch(goarch string) string {
	switch goarch {
	case "arm64", "aarch64":
		return "arm64"
	case "amd64", "x86_64":
		return "x86_64"
	}
	return goarch
}

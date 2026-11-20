//go:build device

package main

// These tests run `lazymobile mcp` the way an agent's client does and call every tool on a real
// simulator and a real emulator, with a small app built from testdata/testapp. They install the
// app, crash it on purpose, and remove it again. Only the devices named in the environment are
// touched, and a real phone is refused:
//
//	LAZYMOBILE_TEST_IOS=<simulator id> LAZYMOBILE_TEST_ANDROID=<emulator name> make devices
//
// A platform whose variable is empty is skipped.

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/0xrnp/lazymobile/internal/device"
)

const (
	e2eApp    = "dev.lazymobile.e2e"
	e2eName   = "LazymobileE2E"
	e2eScheme = "lazymobilee2e"
	// maxModelEdge is the longest side the screenshot tool sends unless full_size is set.
	maxModelEdge = 1568
)

func TestDevicesIOS(t *testing.T) {
	id := os.Getenv("LAZYMOBILE_TEST_IOS")
	switch {
	case id == "":
		t.Skip("set LAZYMOBILE_TEST_IOS to a simulator id")
	case runtime.GOOS != "darwin":
		t.Skip("iOS simulators need macOS")
	}
	// A simulator has no camera permission to grant.
	runAgentFlow(t, id, buildIOSApp(t), "photos")
}

func TestDevicesAndroid(t *testing.T) {
	name := os.Getenv("LAZYMOBILE_TEST_ANDROID")
	if name == "" {
		t.Skip("set LAZYMOBILE_TEST_ANDROID to an emulator name")
	}
	runAgentFlow(t, name, buildAndroidApp(t), "camera")
}

// runAgentFlow is what an agent checking a change does, then the mistakes it can make.
func runAgentFlow(t *testing.T, id, appPath, permission string) {
	// launch remembers app ids; keep the test's out of the list people see.
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	a := connectAgent(t)
	d := a.device(id)
	if d.Kind == device.Physical {
		t.Fatalf("%s is a real phone; the device tests use only simulators and emulators", d.Name)
	}
	android := d.Platform == device.Android
	if !d.Ready() {
		t.Cleanup(func() { a.ok("shutdown", map[string]any{"device": id}, "Shut down") })
	}
	a.ok("boot", map[string]any{"device": id}, "is ready")
	restoreDevice(t, a, id, android)

	a.ok("install", map[string]any{"device": id, "path": appPath}, "Installed")
	t.Cleanup(func() { runLazymobile(t, "uninstall", id, e2eApp, "--yes") })
	if android {
		// Android lists packages without versions: reading each one's version would be slow.
		a.ok("list_apps", map[string]any{"device": id}, e2eApp)
	} else {
		a.ok("list_apps", map[string]any{"device": id}, e2eApp, "1.2.3")
	}
	// The log keeps the lines of an earlier run, so reads start at this launch and links carry
	// this run's id.
	started := time.Now()
	sinceLaunch := func() string { return strconv.Itoa(int(time.Since(started).Seconds())+5) + "s" }
	run := strconv.FormatInt(started.UnixNano(), 36)
	a.ok("launch", map[string]any{"device": id, "app": e2eApp}, "Launched")
	time.Sleep(3 * time.Second)

	full := a.screenshot(map[string]any{"device": id, "full_size": true})
	scaled := a.screenshot(map[string]any{"device": id})
	if full.format != "png" || max(scaled.w, scaled.h) > maxModelEdge {
		t.Errorf("full %+v, scaled %+v", full, scaled)
	}
	if max(full.w, full.h) > maxModelEdge && scaled.format != "jpeg" {
		t.Errorf("a %dx%d screen was sent as %s", full.w, full.h, scaled.format)
	}

	a.eventually("read_logs", map[string]any{"device": id, "app": e2eApp, "since": sinceLaunch(), "text": "launched"},
		30*time.Second, has("LAZYMOBILE_E2E", "launched"))

	a.ok("set_permission", map[string]any{"device": id, "app": e2eApp, "permission": permission, "grant": true}, "Granted")
	a.ok("set_permission", map[string]any{"device": id, "app": e2eApp, "permission": permission, "grant": false}, "Revoked")
	a.ok("set_appearance", map[string]any{"device": id, "mode": "dark"}, "dark mode")
	a.ok("set_appearance", map[string]any{"device": id, "mode": "light"}, "light mode")
	a.ok("set_location", map[string]any{"device": id, "location": "37.5665,126.978"}, "37.5665")
	if android {
		a.refused("set_location", map[string]any{"device": id, "location": "clear"}, "cannot forget")
	} else {
		a.ok("set_location", map[string]any{"device": id, "location": "clear"}, "Cleared")
	}
	a.ok("set_status_bar", map[string]any{"device": id, "mode": "clean"}, "tidy")
	a.ok("set_status_bar", map[string]any{"device": id, "mode": "reset"}, "back to normal")

	hello := e2eScheme + "://hello?run=" + run
	a.ok("open_link", map[string]any{"device": id, "url": hello}, "Opened")
	a.eventually("read_logs", map[string]any{"device": id, "app": e2eApp, "since": sinceLaunch(), "text": "opened"},
		30*time.Second, has(hello))

	if android {
		a.refused("send_push", map[string]any{"device": id, "app": e2eApp}, "Firebase")
	} else {
		pushed := "LAZYMOBILE push " + run
		a.ok("send_push", map[string]any{"device": id, "app": e2eApp, "body": pushed}, "Sent")
		a.eventually("read_logs", map[string]any{"device": id, "app": e2eApp, "since": sinceLaunch(), "text": "push"},
			30*time.Second, has(pushed))
	}

	crash := e2eScheme + "://crash?run=" + run
	crashArgs := map[string]any{"device": id, "app": e2eApp}
	// An earlier run can leave a report behind, on Android in particular, so wait for a new one.
	before := a.text("crash_report", crashArgs)
	a.ok("open_link", map[string]any{"device": id, "url": crash}, "Opened")
	a.eventually("crash_report", crashArgs, 90*time.Second, func(out string) bool {
		if out == before {
			return false
		}
		if android {
			return strings.Contains(out, crash)
		}
		// A Swift fatal error stops the app with a trap: a breakpoint on arm64, an illegal
		// instruction on Intel. Its message is in the log, not in the report.
		return strings.Contains(out, "EXC_BREAKPOINT") || strings.Contains(out, "EXC_BAD_INSTRUCTION")
	})
	a.eventually("read_logs", map[string]any{"device": id, "app": e2eApp, "since": sinceLaunch(), "crashes_only": true},
		30*time.Second, has("LAZYMOBILE_E2E crash on purpose: "+crash))

	a.ok("launch", map[string]any{"device": id, "app": e2eApp, "restart": true}, "Launched")
	a.ok("stop", map[string]any{"device": id, "app": e2eApp}, "Stopped")

	a.refused("launch", map[string]any{"device": id, "app": "dev.lazymobile.missing"}, "dev.lazymobile.missing")
	missing := filepath.Join(t.TempDir(), "missing"+filepath.Ext(appPath))
	a.refused("install", map[string]any{"device": id, "path": missing}, "There is nothing at")
	a.refused("read_logs", map[string]any{"device": id, "level": "loud"}, "unknown level")
	a.refused("set_status_bar", map[string]any{"device": id, "mode": "tidy"}, "clean or reset")
	a.ok("doctor", map[string]any{}, `"found"`)
}

// restoreDevice puts back, after the test, what the tools change on the device and the Mac.
func restoreDevice(t *testing.T, a *agent, id string, android bool) {
	var shown struct {
		Appearance string `json:"appearance"`
	}
	if err := json.Unmarshal(runLazymobile(t, "appearance", id, "--json"), &shown); err == nil && shown.Appearance != "" {
		t.Cleanup(func() { a.ok("set_appearance", map[string]any{"device": id, "mode": shown.Appearance}) })
	}
	t.Cleanup(func() { a.ok("set_status_bar", map[string]any{"device": id, "mode": "reset"}) })
	if android {
		return
	}
	t.Cleanup(func() { a.ok("set_location", map[string]any{"device": id, "location": "clear"}) })
	t.Cleanup(func() {
		home, _ := os.UserHomeDir()
		reports, _ := filepath.Glob(filepath.Join(home, "Library", "Logs", "DiagnosticReports", e2eName+"-*.ips"))
		for _, report := range reports {
			if err := os.Remove(report); err != nil {
				t.Error(err)
			}
		}
	})
}

// agent is an MCP client connected to the built binary over stdio, as Claude Code or Cursor is.
type agent struct {
	t       *testing.T
	session *mcp.ClientSession
}

func connectAgent(t *testing.T) *agent {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "lazymobile-devices-test", Version: "0"}, nil)
	session, err := client.Connect(context.Background(), &mcp.CommandTransport{Command: exec.Command(built(t), "mcp")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return &agent{t: t, session: session}
}

func (a *agent) call(name string, args map[string]any) *mcp.CallToolResult {
	a.t.Helper()
	res, err := a.session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		a.t.Fatalf("%s: %v", name, err)
	}
	return res
}

func textOf(res *mcp.CallToolResult) string {
	var parts []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// text is a tool's answer, whether it failed or not.
func (a *agent) text(name string, args map[string]any) string {
	a.t.Helper()
	return textOf(a.call(name, args))
}

// ok calls a tool that should succeed with every one of want in its answer.
func (a *agent) ok(name string, args map[string]any, want ...string) string {
	a.t.Helper()
	res := a.call(name, args)
	out := textOf(res)
	if res.IsError || !has(want...)(out) {
		a.t.Errorf("%s %v: error %v, want %q in\n%s", name, args, res.IsError, want, out)
	}
	return out
}

// refused calls a tool that should fail with every one of want in its error.
func (a *agent) refused(name string, args map[string]any, want ...string) {
	a.t.Helper()
	res := a.call(name, args)
	if out := textOf(res); !res.IsError || !has(want...)(out) {
		a.t.Errorf("%s %v: error %v, want a refusal with %q in\n%s", name, args, res.IsError, want, out)
	}
}

// eventually asks again until the answer is right: logs and crash reports reach the disk a few
// seconds after the event.
func (a *agent) eventually(name string, args map[string]any, within time.Duration, right func(string) bool) {
	a.t.Helper()
	deadline := time.Now().Add(within)
	for {
		res := a.call(name, args)
		out := textOf(res)
		if !res.IsError && right(out) {
			return
		}
		if time.Now().After(deadline) {
			a.t.Errorf("%s %v: not right within %s; last answer:\n%s", name, args, within, out)
			return
		}
		time.Sleep(2 * time.Second)
	}
}

func has(want ...string) func(string) bool {
	return func(out string) bool {
		for _, w := range want {
			if !strings.Contains(out, w) {
				return false
			}
		}
		return true
	}
}

func (a *agent) device(id string) device.Device {
	a.t.Helper()
	var list struct {
		Devices []device.Device `json:"devices"`
	}
	if err := json.Unmarshal([]byte(a.ok("list_devices", map[string]any{})), &list); err != nil {
		a.t.Fatal(err)
	}
	for _, d := range list.Devices {
		if d.ID == id {
			return d
		}
	}
	a.t.Fatalf("no device with the id %s", id)
	return device.Device{}
}

type picture struct {
	format string
	w, h   int
}

func (a *agent) screenshot(args map[string]any) picture {
	a.t.Helper()
	res := a.call("screenshot", args)
	if res.IsError || len(res.Content) != 1 {
		a.t.Fatalf("screenshot %v: %s", args, textOf(res))
	}
	img, ok := res.Content[0].(*mcp.ImageContent)
	if !ok {
		a.t.Fatalf("screenshot %v: %#v", args, res.Content[0])
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(img.Data))
	if err != nil {
		a.t.Fatalf("screenshot %v: %v", args, err)
	}
	if "image/"+format != img.MIMEType {
		a.t.Errorf("screenshot %v: a %s sent as %s", args, format, img.MIMEType)
	}
	return picture{format, cfg.Width, cfg.Height}
}

// runLazymobile runs the built binary as a person would, for what the MCP server leaves out on purpose.
func runLazymobile(t *testing.T, args ...string) []byte {
	t.Helper()
	out, err := exec.Command(built(t), args...).Output()
	if err != nil {
		t.Errorf("lazymobile %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// buildStep runs a build tool and stops the test with its output if it fails.
func buildStep(t *testing.T, env []string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

// buildIOSApp compiles testdata/testapp/ios into an .app for the simulator, signed ad hoc as a
// simulator needs.
func buildIOSApp(t *testing.T) string {
	t.Helper()
	src, err := filepath.Abs("testdata/testapp/ios")
	if err != nil {
		t.Fatal(err)
	}
	app := filepath.Join(t.TempDir(), e2eName+".app")
	if err := os.Mkdir(app, 0o755); err != nil {
		t.Fatal(err)
	}
	plist, err := os.ReadFile(filepath.Join(src, "Info.plist"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "Info.plist"), plist, 0o644); err != nil {
		t.Fatal(err)
	}
	arch := "arm64"
	if runtime.GOARCH == "amd64" {
		arch = "x86_64"
	}
	buildStep(t, nil, "xcrun", "--sdk", "iphonesimulator", "swiftc", "-module-name", e2eName,
		"-target", arch+"-apple-ios17.0-simulator", filepath.Join(src, "main.swift"), "-o", filepath.Join(app, e2eName))
	buildStep(t, nil, "codesign", "--sign", "-", "--force", app)
	return app
}

// buildAndroidApp compiles testdata/testapp/android into a signed .apk with the SDK's own tools,
// without Gradle.
func buildAndroidApp(t *testing.T) string {
	t.Helper()
	src, err := filepath.Abs("testdata/testapp/android")
	if err != nil {
		t.Fatal(err)
	}
	sdk := androidSDK(t)
	tools := filepath.Join(sdk, "build-tools", newest(t, filepath.Join(sdk, "build-tools"), ""))
	jar := filepath.Join(sdk, "platforms", "android-"+newest(t, filepath.Join(sdk, "platforms"), "android-"), "android.jar")
	jdk := javaHome(t)
	env := []string{"JAVA_HOME=" + jdk, "PATH=" + filepath.Join(jdk, "bin") + string(os.PathListSeparator) + os.Getenv("PATH")}

	work := t.TempDir()
	classes, dex := filepath.Join(work, "classes"), filepath.Join(work, "dex")
	for _, dir := range []string{classes, dex} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	buildStep(t, env, filepath.Join(jdk, "bin", "javac"), "--release", "11", "-classpath", jar, "-d", classes,
		filepath.Join(src, "src", "dev", "lazymobile", "e2e", "Main.java"))
	compiled, _ := filepath.Glob(filepath.Join(classes, "dev", "lazymobile", "e2e", "*.class"))
	buildStep(t, env, filepath.Join(tools, "d8"), append([]string{"--lib", jar, "--output", dex}, compiled...)...)
	unsigned := filepath.Join(work, "unsigned.apk")
	buildStep(t, env, filepath.Join(tools, "aapt2"), "link", "-I", jar, "--manifest", filepath.Join(src, "AndroidManifest.xml"), "-o", unsigned)
	// aapt2 writes no code, so classes.dex is added to the archive here.
	buildStep(t, env, filepath.Join(jdk, "bin", "jar"), "--update", "--no-manifest", "--file", unsigned, "-C", dex, "classes.dex")
	aligned := filepath.Join(work, "aligned.apk")
	buildStep(t, env, filepath.Join(tools, "zipalign"), "-f", "4", unsigned, aligned)
	keystore := filepath.Join(work, "test.keystore")
	buildStep(t, env, filepath.Join(jdk, "bin", "keytool"), "-genkeypair", "-keystore", keystore, "-storepass", "android",
		"-keypass", "android", "-alias", "test", "-keyalg", "RSA", "-keysize", "2048", "-validity", "1", "-dname", "CN=lazymobile e2e")
	apk := filepath.Join(work, e2eName+".apk")
	buildStep(t, env, filepath.Join(tools, "apksigner"), "sign", "--ks", keystore, "--ks-pass", "pass:android", "--out", apk, aligned)
	return apk
}

func androidSDK(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"ANDROID_HOME", "ANDROID_SDK_ROOT"} {
		if dir := os.Getenv(name); dir != "" {
			return dir
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Android", "sdk")
	}
	return filepath.Join(home, "Android", "Sdk")
}

// javaHome is a JDK with javac: JAVA_HOME, the one Android Studio ships on macOS, or the one whose
// javac is on PATH (followed through links, as Linux installs it).
func javaHome(t *testing.T) string {
	t.Helper()
	candidates := []string{os.Getenv("JAVA_HOME"), "/Applications/Android Studio.app/Contents/jbr/Contents/Home"}
	if javac, err := exec.LookPath("javac"); err == nil {
		if real, err := filepath.EvalSymlinks(javac); err == nil {
			candidates = append(candidates, filepath.Dir(filepath.Dir(real)))
		}
	}
	for _, dir := range candidates {
		if dir == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "bin", "javac")); err == nil {
			return dir
		}
	}
	t.Fatal("no JDK with javac: set JAVA_HOME")
	return ""
}

// newest is the highest released version among the folders in dir named prefix plus a version,
// such as 36.1.0 in build-tools or android-36 in platforms. A preview such as 37.0.0-rc2 is left out.
func newest(t *testing.T, dir, prefix string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	best, bestParts := "", []int(nil)
	for _, e := range entries {
		version, ok := strings.CutPrefix(e.Name(), prefix)
		if !ok || !e.IsDir() {
			continue
		}
		parts, ok := versionParts(version)
		if ok && newer(parts, bestParts) {
			best, bestParts = version, parts
		}
	}
	if best == "" {
		t.Fatalf("nothing in %s", dir)
	}
	return best
}

func versionParts(version string) ([]int, bool) {
	var parts []int
	for _, field := range strings.Split(version, ".") {
		n, err := strconv.Atoi(field)
		if err != nil {
			return nil, false
		}
		parts = append(parts, n)
	}
	return parts, true
}

func newer(a, b []int) bool {
	for i := range max(len(a), len(b)) {
		var x, y int
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			return x > y
		}
	}
	return false
}

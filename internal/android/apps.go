package android

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/logs"
)

// Install installs an .apk on a running emulator or connected phone, replacing an older copy.
func (c *Client) Install(ctx context.Context, d device.Device, apkPath string) error {
	if d.Platform != device.Android {
		return apperr.New(apperr.Unsupported, "Cannot install an .apk on %s", d.Name)
	}
	adb, err := c.Opts.tool("adb")
	if err != nil {
		return err
	}
	argv := []string{adb, "-s", d.TargetID(), "install", "-r", apkPath}
	res := c.run(ctx, argv, 5*time.Minute)
	if err := apperr.FromResult(res, argv, installHint["adb"]); err != nil {
		return err
	}
	// adb has exited 0 after printing "Failure [INSTALL_FAILED_...]".
	if out := res.Stdout + res.Stderr; strings.Contains(out, "Failure [") {
		return &apperr.Error{Code: apperr.CommandFailed, Message: "Install failed: " + strings.TrimSpace(out), Argv: argv}
	}
	return nil
}

// Uninstall removes an app, by package name, from a running emulator or connected phone, with its
// data.
func (c *Client) Uninstall(ctx context.Context, d device.Device, id string) error {
	if d.Platform != device.Android || !d.Ready() {
		return apperr.New(apperr.InvalidState, "%s is not running", d.Name)
	}
	if !logs.ValidAppID(id) {
		return apperr.New(apperr.InvalidState, "That does not look like an app id: %s", id)
	}
	adb, err := c.Opts.tool("adb")
	if err != nil {
		return err
	}
	argv := []string{adb, "-s", d.TargetID(), "uninstall", id}
	res := c.run(ctx, argv, actionTimeout)
	if err := apperr.FromResult(res, argv, installHint["adb"]); err != nil {
		return err
	}
	// Like install, adb can exit 0 after printing "Failure [DELETE_FAILED_...]".
	if out := res.Stdout + res.Stderr; strings.Contains(out, "Failure [") {
		return &apperr.Error{Code: apperr.CommandFailed, Message: "Uninstall failed: " + strings.TrimSpace(out), Argv: argv}
	}
	return nil
}

// Launch starts an app, by package name, on a running emulator or connected phone, and brings it
// to the front. An app that is already running keeps running, unless restart stops it first.
func (c *Client) Launch(ctx context.Context, d device.Device, id string, restart bool) error {
	adb, err := c.appTool(d, id)
	if err != nil {
		return err
	}
	if restart {
		if err := c.forceStop(ctx, adb, d, id); err != nil {
			return err
		}
	}
	// monkey sends one launcher event to the package, which opens its main screen without the
	// activity's name.
	argv := []string{adb, "-s", d.TargetID(), "shell", "monkey", "-p", id, "-c", "android.intent.category.LAUNCHER", "1"}
	res := c.run(ctx, argv, actionTimeout)
	if strings.Contains(res.Stdout+res.Stderr, "No activities found to run") {
		return &apperr.Error{
			Code:    apperr.NotFound,
			Message: fmt.Sprintf("%s has nothing to launch on %s", id, d.Name),
			Hint:    fmt.Sprintf("It is not installed, or it has no screen of its own. Run `lazymobile apps %q` to see what is installed.", d.Name),
			Argv:    argv, ExitCode: res.Code,
		}
	}
	return apperr.FromResult(res, argv, installHint["adb"])
}

// Stop force-stops an app, by package name, on a running emulator or connected phone. An app that
// is not running is already stopped, which is success. `am force-stop` also succeeds for a
// package that does not exist, so that is checked first.
func (c *Client) Stop(ctx context.Context, d device.Device, id string) error {
	adb, err := c.appTool(d, id)
	if err != nil {
		return err
	}
	if _, err := c.AppInfo(ctx, d, id); err != nil {
		return err
	}
	return c.forceStop(ctx, adb, d, id)
}

// appTool checks that an app on d can be acted on, and returns the path of adb.
func (c *Client) appTool(d device.Device, id string) (string, error) {
	if d.Platform != device.Android || !d.Ready() {
		return "", apperr.New(apperr.InvalidState, "%s is not running", d.Name)
	}
	// adb joins `shell` arguments into one command line for the device's shell.
	if !logs.ValidAppID(id) {
		return "", apperr.New(apperr.InvalidState, "That does not look like an app id: %s", id)
	}
	return c.Opts.tool("adb")
}

func (c *Client) forceStop(ctx context.Context, adb string, d device.Device, id string) error {
	argv := []string{adb, "-s", d.TargetID(), "shell", "am", "force-stop", id}
	return apperr.FromResult(c.run(ctx, argv, actionTimeout), argv, installHint["adb"])
}

// shellQuote makes s one word for the device's shell, which adb hands its `shell` arguments to.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// runningDevice checks that d is a running Android device, and returns the path of adb.
func (c *Client) runningDevice(d device.Device) (string, error) {
	if d.Platform != device.Android || !d.Ready() {
		return "", apperr.New(apperr.InvalidState, "%s is not running", d.Name)
	}
	return c.Opts.tool("adb")
}

// OpenURL opens a link on a running emulator or phone, in the app that handles it.
func (c *Client) OpenURL(ctx context.Context, d device.Device, link string) error {
	adb, err := c.runningDevice(d)
	if err != nil {
		return err
	}
	argv := []string{adb, "-s", d.TargetID(), "shell", "am", "start", "-a", "android.intent.action.VIEW", "-d", shellQuote(link)}
	res := c.run(ctx, argv, actionTimeout)
	// am exits 1 and says so when no app handles the link.
	if strings.Contains(res.Stdout+res.Stderr, "unable to resolve Intent") {
		return &apperr.Error{
			Code:    apperr.NotFound,
			Message: fmt.Sprintf("No app on %s opens that link", d.Name),
			Hint:    "Check the link's scheme, and that the app is installed.",
			Argv:    argv, ExitCode: res.Code,
		}
	}
	return apperr.FromResult(res, argv, installHint["adb"])
}

// InstalledApps lists the apps a user installed on a running emulator or phone, by package name.
// Android's own apps are left out. Android does not say an app's name without a slower query, so
// only the package name is given. also are package names to add when they are on the device as
// Android's own apps, such as Settings, so that an app asked about by name is not called missing.
func (c *Client) InstalledApps(ctx context.Context, d device.Device, also ...string) ([]device.App, error) {
	if d.Platform != device.Android || !d.Ready() {
		return nil, apperr.New(apperr.InvalidState, "%s is not running", d.Name)
	}
	adb, err := c.Opts.tool("adb")
	if err != nil {
		return nil, err
	}
	argv := []string{adb, "-s", d.TargetID(), "shell", "pm", "list", "packages", "-3"}
	res := c.run(ctx, argv, actionTimeout)
	if err := apperr.FromResult(res, argv, installHint["adb"]); err != nil {
		return nil, err
	}
	var apps []device.App
	for _, line := range strings.Split(res.Stdout, "\n") {
		if id, found := strings.CutPrefix(strings.TrimSpace(line), "package:"); found && id != "" {
			apps = append(apps, device.App{ID: id})
		}
	}
	if len(also) > 0 {
		apps = append(apps, c.builtinAmong(ctx, adb, d, apps, also)...)
	}
	slices.SortFunc(apps, func(a, b device.App) int { return strings.Compare(a.ID, b.ID) })
	return apps, nil
}

// builtinAmong finds which of ids are Android's own packages on d, and not already in apps. A
// failure finds none: they are then listed as before.
func (c *Client) builtinAmong(ctx context.Context, adb string, d device.Device, apps []device.App, ids []string) []device.App {
	argv := []string{adb, "-s", d.TargetID(), "shell", "pm", "list", "packages", "-s"}
	res := c.run(ctx, argv, actionTimeout)
	if apperr.FromResult(res, argv, installHint["adb"]) != nil {
		return nil
	}
	system := map[string]bool{}
	for _, line := range strings.Split(res.Stdout, "\n") {
		if id, found := strings.CutPrefix(strings.TrimSpace(line), "package:"); found {
			system[id] = true
		}
	}
	listed := map[string]bool{}
	for _, a := range apps {
		listed[a.ID] = true
	}
	var found []device.App
	for _, id := range ids {
		if system[id] && !listed[id] {
			listed[id] = true
			found = append(found, device.App{ID: id})
		}
	}
	return found
}

// AppInfo reads the version of one app, by package name, from a running emulator or phone. Any
// package works, the system's too.
func (c *Client) AppInfo(ctx context.Context, d device.Device, id string) (device.App, error) {
	if d.Platform != device.Android || !d.Ready() {
		return device.App{}, apperr.New(apperr.InvalidState, "%s is not running", d.Name)
	}
	// adb joins `shell` arguments into one command line for the device's shell.
	if !logs.ValidAppID(id) {
		return device.App{}, apperr.New(apperr.InvalidState, "That does not look like an app id: %s", id)
	}
	adb, err := c.Opts.tool("adb")
	if err != nil {
		return device.App{}, err
	}
	argv := []string{adb, "-s", d.TargetID(), "shell", "dumpsys", "package", id}
	res := c.run(ctx, argv, queryTimeout)
	if err := apperr.FromResult(res, argv, installHint["adb"]); err != nil {
		return device.App{}, err
	}
	version, build, found := ParsePackageVersion(res.Stdout, id)
	if !found {
		return device.App{}, apperr.New(apperr.NotFound, "%s is not installed on %s", id, d.Name)
	}
	return device.App{ID: id, Version: version, Build: build}, nil
}

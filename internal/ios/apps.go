package ios

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/run"
)

// AppExecutable is the executable name of an installed app ("Runner" for most Flutter apps),
// which is the process name its logs carry. The simulator must be booted.
func (c *Client) AppExecutable(ctx context.Context, d device.Device, bundleID string) (string, error) {
	argv := []string{"xcrun", "simctl", "get_app_container", d.ID, bundleID, "app"}
	res := c.Runner.Run(ctx, argv, run.Options{Timeout: actionTimeout})
	if err := apperr.FromResult(res, argv, notInstalled); err != nil {
		if e, ok := err.(*apperr.Error); ok {
			e.Hint = fmt.Sprintf("Is %s installed on %s, and is it booted?", bundleID, d.Name)
		}
		return "", err
	}
	plist := filepath.Join(strings.TrimSpace(res.Stdout), "Info.plist")
	read := []string{"plutil", "-extract", "CFBundleExecutable", "raw", "-o", "-", plist}
	out := c.Runner.Run(ctx, read, run.Options{Timeout: actionTimeout})
	name := strings.TrimSpace(out.Stdout)
	if apperr.FromResult(out, read, "") != nil || name == "" {
		return "", &apperr.Error{Code: apperr.ParseFailed, Message: "Could not read the app's executable name", Argv: read}
	}
	return name, nil
}

// Install installs an .app bundle on a booted simulator, or on a connected iPhone. An iPhone
// needs a build made for devices and signed for it, which devicectl checks.
func (c *Client) Install(ctx context.Context, d device.Device, appPath string) error {
	if d.Platform != device.IOS {
		return apperr.New(apperr.Unsupported, "Cannot install on %s: it is not an iOS device", d.Name)
	}
	if d.Kind == device.Physical {
		_, err := c.devicectlWithin(ctx, 5*time.Minute, "device", "install", "app", "--device", d.ID, appPath)
		return err
	}
	argv := []string{"xcrun", "simctl", "install", d.ID, appPath}
	return apperr.FromResult(c.Runner.Run(ctx, argv, run.Options{Timeout: 5 * time.Minute}), argv, notInstalled)
}

// Uninstall removes an app, by bundle id, with its data, from a booted simulator or a connected
// iPhone or iPad. Both tools report success for an id that is not installed, so the caller checks
// that first.
func (c *Client) Uninstall(ctx context.Context, d device.Device, bundleID string) error {
	switch {
	case d.Platform == device.IOS && d.Kind == device.Simulator:
		argv := []string{"xcrun", "simctl", "uninstall", d.ID, bundleID}
		return actionError(c.Runner.Run(ctx, argv, run.Options{Timeout: actionTimeout}), argv, uninstallAction)
	case d.Platform == device.IOS && d.Kind == device.Physical:
		_, err := c.devicectl(ctx, "device", "uninstall", "app", "--device", d.ID, bundleID)
		return err
	}
	return apperr.New(apperr.Unsupported, "Cannot uninstall from %s: only iOS devices are supported", d.Name)
}

var launchAction = action{verb: "launch an app", hint: "Boot it first."}

// Launch starts an app, by bundle id, on a booted simulator or a connected iPhone or iPad, and
// brings it to the front. An app that is already running keeps running, unless restart stops it
// first.
func (c *Client) Launch(ctx context.Context, d device.Device, bundleID string, restart bool) error {
	switch {
	case d.Platform == device.IOS && d.Kind == device.Simulator:
		argv := []string{"xcrun", "simctl", "launch"}
		if restart {
			argv = append(argv, "--terminate-running-process")
		}
		argv = append(argv, d.ID, bundleID)
		res := c.Runner.Run(ctx, argv, run.Options{Timeout: actionTimeout})
		// simctl exits 4 for a bundle id the simulator does not have.
		if res.Code == 4 && strings.Contains(res.Stderr, "FBSOpenApplicationServiceErrorDomain, code=4") {
			return notInstalledApp(d, bundleID, argv, res)
		}
		return actionError(res, argv, launchAction)
	case d.Platform == device.IOS && d.Kind == device.Physical:
		args := []string{"device", "process", "launch", "--device", d.ID}
		if restart {
			args = append(args, "--terminate-existing")
		}
		_, err := c.devicectl(ctx, append(args, bundleID)...)
		return err
	}
	return apperr.New(apperr.Unsupported, "Cannot launch an app on %s: only iOS devices are supported", d.Name)
}

// Stop ends a running app, by bundle id, on a booted simulator. An app that is not running is
// already stopped, which is success. An iPhone's apps cannot be stopped by bundle id: devicectl
// only ends a process by its number.
func (c *Client) Stop(ctx context.Context, d device.Device, bundleID string) error {
	switch {
	case d.Platform == device.IOS && d.Kind == device.Simulator:
		argv := []string{"xcrun", "simctl", "terminate", d.ID, bundleID}
		res := c.Runner.Run(ctx, argv, run.Options{Timeout: actionTimeout})
		if res.Code == 0 || !strings.Contains(res.Stderr, "found nothing to terminate") {
			return actionError(res, argv, action{verb: "stop an app", hint: "Boot it first."})
		}
		// simctl says the same for an app that is not running and for one that is not installed.
		lookup := []string{"xcrun", "simctl", "get_app_container", d.ID, bundleID, "app"}
		if found := c.Runner.Run(ctx, lookup, run.Options{Timeout: actionTimeout}); apperr.FromResult(found, lookup, notInstalled) != nil {
			return notInstalledApp(d, bundleID, argv, res)
		}
		return nil
	case d.Platform == device.IOS && d.Kind == device.Physical:
		return apperr.New(apperr.Unsupported, "An app on %s cannot be stopped from here", d.Name).
			WithHint("Restart it instead: that stops the running copy first.")
	}
	return apperr.New(apperr.Unsupported, "Cannot stop an app on %s: only iOS devices are supported", d.Name)
}

// OpenURL opens a link on a booted simulator, in the app that handles it.
func (c *Client) OpenURL(ctx context.Context, d device.Device, link string) error {
	if err := onlySimulator(d, "open a link"); err != nil {
		return err
	}
	argv := []string{"xcrun", "simctl", "openurl", d.ID, link}
	res := c.Runner.Run(ctx, argv, run.Options{Timeout: actionTimeout})
	// simctl exits 115 when no app handles the link's scheme.
	if res.Code == 115 && strings.Contains(res.Stderr, "LSApplicationWorkspaceErrorDomain") {
		return &apperr.Error{
			Code:    apperr.NotFound,
			Message: fmt.Sprintf("No app on %s opens that link", d.Name),
			Hint:    "Check the link's scheme, and that the app is installed.",
			Argv:    argv, ExitCode: res.Code, Stderr: res.Stderr,
		}
	}
	return actionError(res, argv, openURLAction)
}

func notInstalledApp(d device.Device, bundleID string, argv []string, res run.Result) error {
	return &apperr.Error{
		Code:    apperr.NotFound,
		Message: fmt.Sprintf("%s is not installed on %s", bundleID, d.Name),
		Hint:    fmt.Sprintf("Run `lazymobile apps %q` to see what is installed.", d.Name),
		Argv:    argv, ExitCode: res.Code, Stderr: res.Stderr,
	}
}

// InstalledApps lists the apps a user installed on a booted simulator, or on a connected iPhone or
// iPad, by bundle id. The apps that come with iOS are left out.
func (c *Client) InstalledApps(ctx context.Context, d device.Device) ([]device.App, error) {
	switch {
	case d.Platform == device.IOS && d.Kind == device.Simulator:
		return c.simulatorApps(ctx, d)
	case d.Platform == device.IOS && d.Kind == device.Physical:
		content, err := c.devicectl(ctx, "device", "info", "apps", "--device", d.ID)
		if err != nil {
			return nil, err
		}
		return ParseDevicectlApps(content)
	}
	return nil, apperr.New(apperr.Unsupported, "Cannot list the apps on %s: only iOS devices are supported", d.Name)
}

func (c *Client) simulatorApps(ctx context.Context, d device.Device) ([]device.App, error) {
	argv := []string{"xcrun", "simctl", "listapps", d.ID}
	res := c.Runner.Run(ctx, argv, run.Options{Timeout: listTimeout})
	if err := apperr.FromResult(res, argv, notInstalled); err != nil {
		return nil, err
	}
	// simctl prints an old-style property list, which plutil turns into JSON.
	convert := []string{"plutil", "-convert", "json", "-o", "-", "-"}
	out := c.Runner.Run(ctx, convert, run.Options{Timeout: listTimeout, Stdin: res.Stdout})
	if err := apperr.FromResult(out, convert, ""); err != nil {
		return nil, err
	}
	var listed map[string]struct {
		ApplicationType            string
		CFBundleDisplayName        string
		CFBundleName               string
		CFBundleShortVersionString string
		CFBundleVersion            string
	}
	if err := json.Unmarshal([]byte(out.Stdout), &listed); err != nil {
		return nil, &apperr.Error{Code: apperr.ParseFailed, Message: "Could not read the simulator's app list: " + err.Error(), Argv: argv}
	}
	var apps []device.App
	for id, app := range listed {
		if app.ApplicationType != "User" {
			continue
		}
		name := app.CFBundleDisplayName
		if name == "" {
			name = app.CFBundleName
		}
		apps = append(apps, device.App{ID: id, Name: name, Version: app.CFBundleShortVersionString, Build: app.CFBundleVersion})
	}
	slices.SortFunc(apps, func(a, b device.App) int { return strings.Compare(a.ID, b.ID) })
	return apps, nil
}

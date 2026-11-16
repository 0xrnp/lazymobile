// Package ios drives iOS simulators (simctl) and lists physical devices (devicectl). It needs
// macOS with Xcode.
package ios

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/run"
)

const (
	listTimeout   = 30 * time.Second
	actionTimeout = 60 * time.Second
	// The first boot of a new simulator has taken minutes, so waiting for it needs a long limit.
	bootWaitTimeout = 10 * time.Minute

	simulatorBundle = "com.apple.iphonesimulator"
	deviceHubBundle = "com.apple.dt.Devices"

	notInstalled = "Install Xcode from the App Store."
)

// Client runs the Xcode tools.
type Client struct {
	Runner run.Runner
	// Exists reports whether a path exists. Tests replace it.
	Exists func(path string) bool
}

// New returns a client that runs the real tools.
func New(r run.Runner) *Client {
	return &Client{Runner: r, Exists: func(path string) bool {
		_, err := os.Stat(path)
		return err == nil
	}}
}

// ListSimulators lists the iOS simulators.
func (c *Client) ListSimulators(ctx context.Context) ([]device.Device, error) {
	argv := []string{"xcrun", "simctl", "list", "devices", "--json"}
	res := c.Runner.Run(ctx, argv, run.Options{Timeout: listTimeout})
	if err := apperr.FromResult(res, argv, notInstalled); err != nil {
		return nil, err
	}
	return ParseSimctl(res.Stdout)
}

// devicectl runs `xcrun devicectl <args>` and returns the JSON it wrote. devicectl only writes JSON
// to a file. It holds device names, ids and app ids, so it lives in a directory only this user can
// open and is removed at once.
func (c *Client) devicectl(ctx context.Context, args ...string) (string, error) {
	return c.devicectlWithin(ctx, listTimeout, args...)
}

func (c *Client) devicectlWithin(ctx context.Context, timeout time.Duration, args ...string) (string, error) {
	dir, err := os.MkdirTemp("", "lazymobile-")
	if err != nil {
		return "", apperr.New(apperr.CommandFailed, "Could not create a temporary directory: %v", err)
	}
	defer os.RemoveAll(dir)
	out := filepath.Join(dir, "output.json")

	argv := append(append([]string{"xcrun", "devicectl"}, args...), "--json-output", out)
	res := c.Runner.Run(ctx, argv, run.Options{Timeout: timeout})
	if err := apperr.FromResult(res, argv, notInstalled); err != nil {
		return "", err
	}
	content, err := os.ReadFile(out)
	if err != nil {
		return "", apperr.New(apperr.ParseFailed, "Could not read the output of devicectl")
	}
	return string(content), nil
}

// ListPhysical lists connected and known iPhones and iPads. It needs Xcode 15 or newer.
func (c *Client) ListPhysical(ctx context.Context) ([]device.Device, error) {
	content, err := c.devicectl(ctx, "list", "devices")
	if err != nil {
		return nil, err
	}
	return ParseDevicectl(content)
}

// ListOptions says which sources to ask.
type ListOptions struct {
	Simulators bool
	Physical   bool
}

// List asks the wanted sources together. A source that fails does not hide the other: the
// devices that were found come back with the errors of the sources that failed.
func (c *Client) List(ctx context.Context, opts ListOptions) ([]device.Device, []error) {
	type result struct {
		devices []device.Device
		err     error
	}
	var sources []func(context.Context) ([]device.Device, error)
	if opts.Simulators {
		sources = append(sources, c.ListSimulators)
	}
	if opts.Physical {
		sources = append(sources, c.ListPhysical)
	}
	results := make([]result, len(sources))
	var wg sync.WaitGroup
	for i, source := range sources {
		wg.Add(1)
		go func() {
			defer wg.Done()
			devices, err := source(ctx)
			results[i] = result{devices, err}
		}()
	}
	wg.Wait()

	var devices []device.Device
	var errs []error
	seen := map[string]bool{}
	for _, r := range results {
		devices = append(devices, r.devices...)
		// Without Xcode both lookups fail the same way: say it once.
		if r.err != nil && !seen[r.err.Error()] {
			seen[r.err.Error()] = true
			errs = append(errs, r.err)
		}
	}
	return device.Dedupe(devices), errs
}

// action describes a simctl verb, for messages and for telling "already done" from a failure.
type action struct {
	verb    string
	already map[string]bool
	hint    string
}

var (
	bootAction     = action{verb: "boot", already: map[string]bool{"Booted": true, "Booting": true}}
	shutdownAction = action{verb: "shut down", already: map[string]bool{"Shutdown": true, "Shutting Down": true}}
	eraseAction    = action{verb: "erase", hint: "Shut it down first."}
	deleteAction   = action{verb: "delete"}
	// A shut-down simulator cannot be asked to uninstall.
	uninstallAction = action{verb: "uninstall", hint: "Boot it first."}
)

var currentState = regexp.MustCompile(`in current state:[ \t]*([^\r\n]*)`)

// actionError turns a finished simctl action into an error. simctl exits 148 for an unknown
// device and 149 for a wrong state ("Unable to boot device in current state: Booted"). A wrong
// state that already meets the goal is success.
func actionError(res run.Result, argv []string, a action) error {
	err := apperr.FromResult(res, argv, notInstalled)
	if err == nil || apperr.CodeOf(err) != apperr.CommandFailed {
		return err
	}
	text := res.Stderr + "\n" + res.Stdout
	if strings.Contains(text, "Invalid device") {
		return &apperr.Error{
			Code: apperr.NotFound, Message: "That simulator no longer exists", Hint: "Refresh the device list.",
			Argv: argv, ExitCode: res.Code, Stderr: res.Stderr,
		}
	}
	if m := currentState.FindStringSubmatch(text); m != nil {
		if state := strings.TrimSpace(m[1]); state != "" {
			if a.already[state] {
				return nil
			}
			return &apperr.Error{
				Code:    apperr.InvalidState,
				Message: fmt.Sprintf("Cannot %s: the simulator is %s", a.verb, strings.ToLower(state)),
				Hint:    a.hint, Argv: argv, ExitCode: res.Code, Stderr: res.Stderr,
			}
		}
	}
	return err
}

func (c *Client) runAction(ctx context.Context, d device.Device, a action, argv []string) error {
	if d.Platform != device.IOS || d.Kind != device.Simulator {
		return apperr.New(apperr.Unsupported, "Cannot %s %s: it is not an iOS simulator", a.verb, d.Name).
			WithHint("Only iOS simulators can be booted, shut down, erased or deleted.")
	}
	return actionError(c.Runner.Run(ctx, argv, run.Options{Timeout: actionTimeout}), argv, a)
}

// BootOptions change how a simulator is booted.
type BootOptions struct {
	// Wait returns only once the simulator has finished booting.
	Wait bool
	// OpenApp shows the simulator's window: Simulator.app, or DeviceHub in Xcode 27.
	OpenApp bool
}

// Boot boots a simulator. Booting one that is already booted succeeds.
func (c *Client) Boot(ctx context.Context, d device.Device, opts BootOptions) error {
	if err := c.runAction(ctx, d, bootAction, []string{"xcrun", "simctl", "boot", d.ID}); err != nil {
		return err
	}
	if opts.OpenApp {
		if err := c.openWindow(ctx, d); err != nil {
			return err
		}
	}
	if opts.Wait {
		return c.WaitBooted(ctx, d)
	}
	return nil
}

// openWindow shows the simulator. Xcode 26 and earlier ship Simulator.app, told which device to
// show by a launch argument. Xcode 27 replaced it with DeviceHub, which opens a device from the
// URL Xcode itself sends. The app inside the selected Xcode comes first, so a Mac with several
// Xcodes opens the matching one. Then any installed copy, by bundle id.
func (c *Client) openWindow(ctx context.Context, d device.Device) error {
	link := "devices://device/open?id=" + url.QueryEscape(d.ID)
	var candidates [][]string

	selected := c.Runner.Run(ctx, []string{"xcode-select", "-p"}, run.Options{Timeout: 10 * time.Second})
	if developer := strings.TrimSpace(selected.Stdout); selected.Code == 0 && selected.Err == nil && !selected.Missing && developer != "" {
		// .../Xcode.app/Contents/Developer: Simulator.app is under it, DeviceHub.app next to it.
		simulator := filepath.Join(developer, "Applications", "Simulator.app")
		deviceHub := filepath.Join(filepath.Dir(developer), "Applications", "DeviceHub.app")
		if c.Exists(simulator) {
			candidates = append(candidates, []string{"open", "-a", simulator, "--args", "-CurrentDeviceUDID", d.ID})
		}
		if c.Exists(deviceHub) {
			candidates = append(candidates, []string{"open", "-a", deviceHub, link})
		}
	}
	candidates = append(candidates,
		[]string{"open", "-b", simulatorBundle, "--args", "-CurrentDeviceUDID", d.ID},
		[]string{"open", "-b", deviceHubBundle, link},
	)

	var last error
	var lastArgv []string
	for _, argv := range candidates {
		res := c.Runner.Run(ctx, argv, run.Options{Timeout: actionTimeout})
		if last = apperr.FromResult(res, argv, ""); last == nil {
			return nil
		}
		lastArgv = argv
	}
	return &apperr.Error{
		Code:    apperr.CommandFailed,
		Message: "The simulator booted, but neither Simulator nor DeviceHub could be opened",
		Hint:    firstLine(last.Error()) + ". Open Xcode once to finish installing it, or start lazymobile with --no-window.",
		Argv:    lastArgv,
	}
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return line
}

// WaitBooted returns once the simulator has finished booting.
func (c *Client) WaitBooted(ctx context.Context, d device.Device) error {
	argv := []string{"xcrun", "simctl", "bootstatus", d.ID}
	err := actionError(c.Runner.Run(ctx, argv, run.Options{Timeout: bootWaitTimeout}), argv, bootAction)
	if e, ok := err.(*apperr.Error); ok && e.Code == apperr.Timeout {
		e.Hint = "It is still booting. The first boot of a new simulator can take several minutes."
	}
	return err
}

// Shutdown shuts a simulator down. Shutting down one that is already off succeeds.
func (c *Client) Shutdown(ctx context.Context, d device.Device) error {
	return c.runAction(ctx, d, shutdownAction, []string{"xcrun", "simctl", "shutdown", d.ID})
}

// simulatorApps are the apps that show simulators: Simulator in Xcode 26 and earlier, DeviceHub in
// Xcode 27. Both keep a window open for a simulator after it shuts down.
var simulatorApps = []struct{ process, bundle string }{
	{"Simulator", simulatorBundle},
	{"DeviceHub", deviceHubBundle},
}

// AppNotQuitError is a simulator that shut down, followed by Simulator or DeviceHub that could not
// be quit. The shutdown itself succeeded, so callers report it as a warning.
type AppNotQuitError struct{ Err *apperr.Error }

func (e *AppNotQuitError) Error() string { return e.Err.Error() }

func (e *AppNotQuitError) Unwrap() error { return e.Err }

// QuitSimulatorApps quits Simulator and DeviceHub when no simulator is running, which closes the
// windows they keep open for simulators that shut down. While any simulator runs they are left
// alone: DeviceHub stops every simulator when it quits, by default. Failures are
// *AppNotQuitError.
func (c *Client) QuitSimulatorApps(ctx context.Context) error {
	for _, app := range simulatorApps {
		// Only an app that is running is addressed. AppleScript would start one that is not, or
		// ask where one that is not installed is.
		if res := c.Runner.Run(ctx, []string{"pgrep", "-x", app.process}, run.Options{Timeout: 10 * time.Second}); res.Code != 0 || res.Err != nil || res.Missing {
			continue
		}
		// Asked again right before each quit, so a simulator booted meanwhile is not stopped.
		idle, err := c.noSimulatorRuns(ctx)
		if err != nil {
			e, _ := err.(*apperr.Error)
			return notQuit(app.process, e)
		}
		if !idle {
			return nil
		}
		argv := []string{"osascript", "-e", fmt.Sprintf(`tell application id "%s" to quit`, app.bundle)}
		if err := apperr.FromResult(c.Runner.Run(ctx, argv, run.Options{Timeout: 30 * time.Second}), argv, ""); err != nil {
			e, _ := err.(*apperr.Error)
			e = notQuit(app.process, e).Err
			e.Hint = "Allow your terminal to control " + app.process + " in System Settings > Privacy & Security > Automation, or start lazymobile with --keep-simulator-app."
			return &AppNotQuitError{e}
		}
	}
	return nil
}

func (c *Client) noSimulatorRuns(ctx context.Context) (bool, error) {
	simulators, err := c.ListSimulators(ctx)
	if err != nil {
		return false, err
	}
	for _, d := range simulators {
		if d.State != device.Shutdown {
			return false, nil
		}
	}
	return true, nil
}

func notQuit(process string, e *apperr.Error) *AppNotQuitError {
	e.Message = "The simulator shut down, but " + process + " could not be quit: " + firstLine(e.Message)
	return &AppNotQuitError{e}
}

// Erase erases a simulator's content and settings. It must be shut down.
func (c *Client) Erase(ctx context.Context, d device.Device) error {
	return c.runAction(ctx, d, eraseAction, []string{"xcrun", "simctl", "erase", d.ID})
}

// Delete deletes a simulator. simctl shuts a booted one down first.
func (c *Client) Delete(ctx context.Context, d device.Device) error {
	return c.runAction(ctx, d, deleteAction, []string{"xcrun", "simctl", "delete", d.ID})
}

// Runtimes lists installed iOS runtimes, newest first, with the device types each supports.
func (c *Client) Runtimes(ctx context.Context) ([]Runtime, error) {
	argv := []string{"xcrun", "simctl", "list", "runtimes", "--json"}
	res := c.Runner.Run(ctx, argv, run.Options{Timeout: listTimeout})
	if err := apperr.FromResult(res, argv, notInstalled); err != nil {
		return nil, err
	}
	runtimes, err := ParseRuntimes(res.Stdout)
	if err != nil {
		return nil, err
	}
	missing := false
	for _, r := range runtimes {
		missing = missing || len(r.DeviceTypes) == 0
	}
	if !missing {
		return runtimes, nil
	}
	// Older simctl does not say which device types a runtime supports: offer every iPhone and
	// iPad, and let simctl refuse a combination that does not work.
	typesArgv := []string{"xcrun", "simctl", "list", "devicetypes", "--json"}
	typesRes := c.Runner.Run(ctx, typesArgv, run.Options{Timeout: listTimeout})
	if apperr.FromResult(typesRes, typesArgv, "") == nil {
		all := ParseDeviceTypes(typesRes.Stdout)
		for i := range runtimes {
			if len(runtimes[i].DeviceTypes) == 0 {
				runtimes[i].DeviceTypes = all
			}
		}
	}
	return runtimes, nil
}

var udidPattern = regexp.MustCompile(`^[0-9A-Fa-f]+(-[0-9A-Fa-f]+){4}$`)

// Create makes a simulator and returns its UDID.
func (c *Client) Create(ctx context.Context, name, deviceType, runtime string) (string, error) {
	if strings.TrimSpace(name) == "" || strings.ContainsAny(name, "\r\n") {
		return "", apperr.New(apperr.InvalidState, "The simulator needs a name")
	}
	argv := []string{"xcrun", "simctl", "create", name, deviceType, runtime}
	res := c.Runner.Run(ctx, argv, run.Options{Timeout: actionTimeout})
	if err := apperr.FromResult(res, argv, notInstalled); err != nil {
		return "", err
	}
	udid := strings.TrimSpace(res.Stdout)
	if !udidPattern.MatchString(udid) {
		return "", &apperr.Error{Code: apperr.ParseFailed, Message: "simctl did not print the new simulator's id", Argv: argv}
	}
	return udid, nil
}

var (
	openURLAction    = action{verb: "open a link", hint: "Boot it first."}
	screenshotAction = action{verb: "take a screenshot", hint: "Boot it first."}
	appearanceAction = action{verb: "change the appearance", hint: "Boot it first."}
)

// onlySimulator refuses what Apple's tools can only do on a simulator, which is most of what
// they do. An iPhone cannot be sent a link, shown in dark mode or photographed from here.
func onlySimulator(d device.Device, what string) error {
	if d.Platform == device.IOS && d.Kind == device.Simulator {
		return nil
	}
	return apperr.New(apperr.Unsupported, "Cannot %s on %s: only iOS simulators are supported", what, d.Name).
		WithHint("Apple's tools cannot do this on an iPhone.")
}

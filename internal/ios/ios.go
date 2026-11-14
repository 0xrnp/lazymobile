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

// ListPhysical lists connected and known iPhones and iPads. It needs Xcode 15 or newer.
func (c *Client) ListPhysical(ctx context.Context) ([]device.Device, error) {
	// devicectl only writes JSON to a file. It holds device names and ids, so it lives in a
	// directory only this user can open and is removed at once.
	dir, err := os.MkdirTemp("", "lazymobile-")
	if err != nil {
		return nil, apperr.New(apperr.CommandFailed, "Could not create a temporary directory: %v", err)
	}
	defer os.RemoveAll(dir)
	out := filepath.Join(dir, "devices.json")

	argv := []string{"xcrun", "devicectl", "list", "devices", "--json-output", out}
	res := c.Runner.Run(ctx, argv, run.Options{Timeout: listTimeout})
	if err := apperr.FromResult(res, argv, notInstalled); err != nil {
		return nil, err
	}
	content, err := os.ReadFile(out)
	if err != nil {
		return nil, apperr.New(apperr.ParseFailed, "Could not read the device list from devicectl")
	}
	return ParseDevicectl(string(content))
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

// Install installs an .app bundle on a booted simulator.
func (c *Client) Install(ctx context.Context, d device.Device, appPath string) error {
	if d.Platform != device.IOS || d.Kind != device.Simulator {
		return apperr.New(apperr.Unsupported, "Cannot install on %s: only iOS simulators are supported", d.Name)
	}
	argv := []string{"xcrun", "simctl", "install", d.ID, appPath}
	return apperr.FromResult(c.Runner.Run(ctx, argv, run.Options{Timeout: 5 * time.Minute}), argv, notInstalled)
}

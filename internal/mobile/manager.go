// Package mobile is the one place that knows both platforms. The dashboard and the plain
// commands both go through it, so they cannot disagree.
package mobile

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/0xrnp/lazymobile/internal/android"
	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/ios"
	"github.com/0xrnp/lazymobile/internal/logs"
	"github.com/0xrnp/lazymobile/internal/run"
)

// Manager lists and drives the devices of both platforms.
type Manager struct {
	Runner  run.Runner
	IOS     *ios.Client
	Android *android.Client
	// GOOS is "darwin" or "linux". iOS is only available on darwin.
	GOOS string
	// OpenWindow shows a simulator's window when it boots.
	OpenWindow bool
	// QuitSimulatorApp quits Simulator or DeviceHub once the last running simulator shuts down,
	// which closes the window they keep open for it.
	QuitSimulatorApp bool
}

// New returns a manager that runs the real tools.
func New(r run.Runner, opts android.Options, goos string) *Manager {
	return &Manager{Runner: r, IOS: ios.New(r), Android: android.New(r, opts), GOOS: goos, OpenWindow: true, QuitSimulatorApp: true}
}

// List returns every device, sorted for display, with the errors of the tools that failed. On
// an OS without Xcode, iOS is left out and not reported as broken.
func (m *Manager) List(ctx context.Context) ([]device.Device, []error) {
	var (
		iosDevices, androidDevices []device.Device
		iosErrs, androidErrs       []error
		wg                         sync.WaitGroup
	)
	if m.GOOS == "darwin" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			iosDevices, iosErrs = m.IOS.List(ctx, ios.ListOptions{Simulators: true, Physical: true})
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		androidDevices, androidErrs = m.Android.List(ctx)
	}()
	wg.Wait()
	return device.Sort(append(iosDevices, androidDevices...)), append(iosErrs, androidErrs...)
}

// Find returns the one device query means: an id, an adb serial, a name, or a unique part of a
// name.
func (m *Manager) Find(ctx context.Context, query string) (device.Device, error) {
	devices, errs := m.List(ctx)
	matches := device.Match(devices, query)
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		err := apperr.New(apperr.NotFound, "No device matches %q", query)
		if len(errs) > 0 {
			err.Hint = "Some tools failed: " + errs[0].Error()
		} else {
			err.Hint = "Run `lazymobile list` to see the devices."
		}
		return device.Device{}, err
	}
	names := make([]string, len(matches))
	for i, d := range matches {
		names[i] = fmt.Sprintf("%s (%s)", d.Name, d.ID)
	}
	return device.Device{}, apperr.New(apperr.InvalidState, "%q matches several devices", query).
		WithHint("Use an id: %s", strings.Join(names, ", "))
}

// BootOptions change how a device is started.
type BootOptions struct {
	// Cold boots an emulator without its saved snapshot.
	Cold bool
	// Wipe erases an emulator's data first.
	Wipe bool
	// Wait returns only once the device has finished booting.
	Wait bool
}

// Boot starts a simulator or emulator.
func (m *Manager) Boot(ctx context.Context, d device.Device, opts BootOptions) error {
	switch {
	case d.Kind == device.Physical:
		return apperr.New(apperr.Unsupported, "%s is a physical device, and cannot be booted from here", d.Name)
	case d.Platform == device.IOS:
		if opts.Wipe {
			return apperr.New(apperr.Unsupported, "Simulators are erased, not wiped").WithHint("Use erase instead.")
		}
		return m.IOS.Boot(ctx, d, ios.BootOptions{Wait: opts.Wait, OpenApp: m.OpenWindow})
	}
	return m.Android.Boot(ctx, d, android.BootOptions{Cold: opts.Cold, Wipe: opts.Wipe, Wait: opts.Wait})
}

// Shutdown stops a simulator or emulator.
func (m *Manager) Shutdown(ctx context.Context, d device.Device) error {
	switch {
	case d.Kind == device.Physical:
		return apperr.New(apperr.Unsupported, "%s is a physical device, and cannot be shut down from here", d.Name)
	case d.Platform == device.IOS:
		if err := m.IOS.Shutdown(ctx, d); err != nil || !m.QuitSimulatorApp {
			return err
		}
		return m.IOS.QuitSimulatorApps(ctx)
	}
	return m.Android.Shutdown(ctx, d)
}

// Erase removes all content and settings. A simulator is erased in place and must be shut down.
// An emulator is started with its data wiped.
func (m *Manager) Erase(ctx context.Context, d device.Device) error {
	switch {
	case d.Kind == device.Physical:
		return apperr.New(apperr.Unsupported, "%s is a physical device, and is never erased from here", d.Name)
	case d.Platform == device.IOS:
		return m.IOS.Erase(ctx, d)
	}
	return m.Android.Boot(ctx, d, android.BootOptions{Wipe: true})
}

// Delete removes a simulator or emulator for good.
func (m *Manager) Delete(ctx context.Context, d device.Device) error {
	switch {
	case d.Kind == device.Physical:
		return apperr.New(apperr.Unsupported, "%s is a physical device, and cannot be deleted", d.Name)
	case d.Platform == device.IOS:
		return m.IOS.Delete(ctx, d)
	}
	return m.Android.Delete(ctx, d)
}

// Install puts a build on a running device: an .apk on Android, an .app folder on a simulator.
func (m *Manager) Install(ctx context.Context, d device.Device, path string) error {
	if !d.Ready() {
		return apperr.New(apperr.InvalidState, "%s is not running", d.Name).WithHint("Boot or connect it first.")
	}
	if d.Platform == device.Android {
		if !strings.HasSuffix(strings.ToLower(path), ".apk") {
			return apperr.New(apperr.InvalidState, "Android devices need an .apk file").WithHint("Got: %s", path)
		}
		return m.Android.Install(ctx, d, path)
	}
	if !strings.HasSuffix(strings.ToLower(strings.TrimRight(path, "/")), ".app") {
		return apperr.New(apperr.InvalidState, "iOS simulators need an .app folder").WithHint("Got: %s", path)
	}
	return m.IOS.Install(ctx, d, path)
}

// Uninstall removes an app and its data from a running device. Only apps the user installed can
// go: the id must be among InstalledApps. That check is also what tells a typo from success,
// because simctl and devicectl both report success for an app that is not there.
func (m *Manager) Uninstall(ctx context.Context, d device.Device, id string) error {
	if !d.Ready() {
		return apperr.New(apperr.InvalidState, "%s is not running", d.Name).WithHint("Boot or connect it first.")
	}
	if !logs.ValidAppID(id) {
		return apperr.New(apperr.InvalidState, "That does not look like an app id: %s", id)
	}
	apps, err := m.InstalledApps(ctx, d)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(apps, func(app device.App) bool { return app.ID == id }) {
		return apperr.New(apperr.NotFound, "%s is not an installed app on %s", id, d.Name).
			WithHint("Run `lazymobile apps %q` to see what is installed. Apps that come with the system cannot be uninstalled.", d.Name)
	}
	if d.Platform == device.IOS {
		return m.IOS.Uninstall(ctx, d, id)
	}
	return m.Android.Uninstall(ctx, d, id)
}

// LogDeps are the tools a log session needs.
func (m *Manager) LogDeps() logs.Deps {
	return logs.Deps{Runner: m.Runner, IOS: m.IOS, ADB: m.Android.ADB}
}

// HasIOS reports whether iOS devices can exist on this machine.
func (m *Manager) HasIOS() bool {
	return m.GOOS == "darwin"
}

// Runtimes lists the installed iOS simulator runtimes.
func (m *Manager) Runtimes(ctx context.Context) ([]ios.Runtime, error) {
	return m.IOS.Runtimes(ctx)
}

// CreateSimulator makes an iOS simulator.
func (m *Manager) CreateSimulator(ctx context.Context, name, deviceType, runtime string) error {
	_, err := m.IOS.Create(ctx, name, deviceType, runtime)
	return err
}

// SystemImages lists the installed Android system images that run on this machine.
func (m *Manager) SystemImages() []android.SystemImage {
	return m.Android.SystemImages()
}

// DeviceProfiles lists the Android hardware profiles.
func (m *Manager) DeviceProfiles(ctx context.Context) ([]string, error) {
	return m.Android.DeviceProfiles(ctx)
}

// CreateAVD makes an Android emulator.
func (m *Manager) CreateAVD(ctx context.Context, name, image, profile string) error {
	return m.Android.Create(ctx, name, image, profile)
}

// Pair pairs with an Android phone over Wi-Fi.
func (m *Manager) Pair(ctx context.Context, address, code string) error {
	return m.Android.Pair(ctx, address, code)
}

// Connect connects to an Android phone over Wi-Fi.
func (m *Manager) Connect(ctx context.Context, address string) error {
	return m.Android.Connect(ctx, address)
}

// Disconnect disconnects an Android phone that is connected over Wi-Fi.
func (m *Manager) Disconnect(ctx context.Context, d device.Device) error {
	return m.Android.Disconnect(ctx, d)
}

// StartLogs begins a log stream.
func (m *Manager) StartLogs(ctx context.Context, spec logs.Spec, h logs.Handlers) LogSession {
	return logs.Start(ctx, m.LogDeps(), spec, h)
}

// LogSession is a running log stream.
type LogSession interface {
	Stop()
	Wait() error
}

// OpenURL shows a web page in the default browser.
func (m *Manager) OpenURL(ctx context.Context, url string) error {
	if !strings.HasPrefix(url, "https://") {
		return apperr.New(apperr.InvalidState, "Not a web address: %s", url)
	}
	opener := "xdg-open"
	if m.GOOS == "darwin" {
		opener = "open"
	}
	argv := []string{opener, url}
	return apperr.FromResult(m.Runner.Run(ctx, argv, run.Options{Timeout: 30 * time.Second}), argv, "")
}

// InstalledApps lists the apps a user installed on a running simulator, emulator, iPhone or
// Android phone.
func (m *Manager) InstalledApps(ctx context.Context, d device.Device) ([]device.App, error) {
	if d.Platform == device.IOS {
		return m.IOS.InstalledApps(ctx, d)
	}
	return m.Android.InstalledApps(ctx, d)
}

// AppInfo describes one app installed on a device, with its version and build when the platform
// says. An app that is not installed is a not_found error. On an iPhone only the apps it lists
// (see InstalledApps) can be found.
func (m *Manager) AppInfo(ctx context.Context, d device.Device, id string) (device.App, error) {
	if d.Platform == device.Android {
		return m.Android.AppInfo(ctx, d, id)
	}
	apps, err := m.IOS.InstalledApps(ctx, d)
	if err != nil {
		return device.App{}, err
	}
	for _, app := range apps {
		if app.ID == id {
			return app, nil
		}
	}
	return device.App{}, apperr.New(apperr.NotFound, "%s is not installed on %s", id, d.Name)
}

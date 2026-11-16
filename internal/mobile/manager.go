// Package mobile is the one place that knows both platforms. The dashboard and the plain
// commands both go through it, so they cannot disagree.
package mobile

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
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

// Install puts a build on a running device: an .apk on Android, an .app folder on a simulator or
// an iPhone. An iPhone needs a build made for devices and signed for it.
func (m *Manager) Install(ctx context.Context, d device.Device, path string) error {
	if !d.Ready() {
		return apperr.New(apperr.InvalidState, "%s is not running", d.Name).WithHint("Boot or connect it first.")
	}
	if d.Platform == device.Android {
		if !strings.HasSuffix(strings.ToLower(path), ".apk") {
			return apperr.New(apperr.InvalidState, "Android devices need an .apk file").WithHint("Got: %s", path)
		}
	} else if !strings.HasSuffix(strings.ToLower(strings.TrimRight(path, "/")), ".app") {
		return apperr.New(apperr.InvalidState, "iOS devices need an .app folder").WithHint("Got: %s", path)
	}
	if _, err := os.Stat(path); err != nil {
		return apperr.New(apperr.NotFound, "There is nothing at %s", path).WithHint("Check the path of the build.")
	}
	if d.Platform == device.Android {
		return m.Android.Install(ctx, d, path)
	}
	return m.IOS.Install(ctx, d, path)
}

// Uninstall removes an app and its data from a running device. Only apps the user installed can
// go: the id must be among InstalledApps. That check is also what tells a typo from success,
// because simctl and devicectl both report success for an app that is not there.
func (m *Manager) Uninstall(ctx context.Context, d device.Device, id string) error {
	if err := appTarget(d, id); err != nil {
		return err
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

// LaunchOptions change how an app is started.
type LaunchOptions struct {
	// Restart stops a running copy of the app first.
	Restart bool
}

// Launch starts an app on a running device and brings it to the front. An app that is not
// installed is a not_found error.
func (m *Manager) Launch(ctx context.Context, d device.Device, id string, opts LaunchOptions) error {
	if err := appTarget(d, id); err != nil {
		return err
	}
	if d.Platform == device.IOS {
		return m.IOS.Launch(ctx, d, id, opts.Restart)
	}
	return m.Android.Launch(ctx, d, id, opts.Restart)
}

// Stop ends a running app on a simulator, emulator or Android phone. Its data is kept. An app
// that is not running is success, and one that is not installed is a not_found error.
func (m *Manager) Stop(ctx context.Context, d device.Device, id string) error {
	if err := appTarget(d, id); err != nil {
		return err
	}
	if d.Platform == device.IOS {
		return m.IOS.Stop(ctx, d, id)
	}
	return m.Android.Stop(ctx, d, id)
}

// appTarget checks that d is running and that id has the shape of an app id.
func appTarget(d device.Device, id string) error {
	if !d.Ready() {
		return apperr.New(apperr.InvalidState, "%s is not running", d.Name).WithHint("Boot or connect it first.")
	}
	if !logs.ValidAppID(id) {
		return apperr.New(apperr.InvalidState, "That does not look like an app id: %s", id).
			WithHint("Use a package name or bundle id, such as com.example.app.")
	}
	return nil
}

var linkPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:[^\x00-\x20\x7f]+$`)

// ValidLink reports whether link looks like a web address or a deep link: a scheme, a colon, and
// no spaces or control characters. An Android phone hands it to its shell, so nothing odder than
// that goes any further.
func ValidLink(link string) bool {
	return len(link) <= 2048 && linkPattern.MatchString(link)
}

// OpenLink opens a web address or deep link on a running simulator, emulator or Android phone, in
// the app that handles it. No app for it is a not_found error.
func (m *Manager) OpenLink(ctx context.Context, d device.Device, link string) error {
	if !d.Ready() {
		return apperr.New(apperr.InvalidState, "%s is not running", d.Name).WithHint("Boot or connect it first.")
	}
	if !ValidLink(link) {
		return apperr.New(apperr.InvalidState, "That does not look like a link: %s", link).
			WithHint("Use a whole link such as myapp://home?id=1 or https://example.com. Write a space as %%20.")
	}
	if d.Platform == device.IOS {
		return m.IOS.OpenURL(ctx, d, link)
	}
	return m.Android.OpenURL(ctx, d, link)
}

// Appearances are the modes SetAppearance takes.
var Appearances = []string{"dark", "light"}

// Appearance is the mode of a running simulator, emulator or Android phone: "dark" or "light", or
// "auto" on an Android device that follows the time of day.
func (m *Manager) Appearance(ctx context.Context, d device.Device) (string, error) {
	if !d.Ready() {
		return "", apperr.New(apperr.InvalidState, "%s is not running", d.Name).WithHint("Boot or connect it first.")
	}
	if d.Platform == device.IOS {
		return m.IOS.Appearance(ctx, d)
	}
	return m.Android.Appearance(ctx, d)
}

// SetAppearance switches a running simulator, emulator or Android phone to dark or light.
func (m *Manager) SetAppearance(ctx context.Context, d device.Device, mode string) error {
	if !d.Ready() {
		return apperr.New(apperr.InvalidState, "%s is not running", d.Name).WithHint("Boot or connect it first.")
	}
	if !slices.Contains(Appearances, mode) {
		return apperr.New(apperr.InvalidState, "%q is not an appearance", mode).WithHint("Use dark or light.")
	}
	if d.Platform == device.IOS {
		return m.IOS.SetAppearance(ctx, d, mode)
	}
	return m.Android.SetAppearance(ctx, d, mode)
}

// PermissionNames are the permissions an app on this platform can be given or refused.
func PermissionNames(platform device.Platform) []string {
	if platform == device.IOS {
		return ios.PermissionNames()
	}
	return android.PermissionNames()
}

// SetPermission grants or revokes a permission of an app on a running simulator or emulator. See
// PermissionNames. Real phones are left alone.
func (m *Manager) SetPermission(ctx context.Context, d device.Device, id, name string, grant bool) error {
	if err := virtualDevice(d, "its apps' permissions"); err != nil {
		return err
	}
	if !logs.ValidAppID(id) {
		return apperr.New(apperr.InvalidState, "That does not look like an app id: %s", id).
			WithHint("Use a package name or bundle id, such as com.example.app.")
	}
	if d.Platform == device.IOS {
		return m.IOS.SetPermission(ctx, d, id, name, grant)
	}
	return m.Android.SetPermission(ctx, d, id, name, grant)
}

// ClearData erases all the data of an app on a running emulator, as if it had just been
// installed. An iOS simulator has no such command: uninstalling the app does it.
func (m *Manager) ClearData(ctx context.Context, d device.Device, id string) error {
	if err := virtualDevice(d, "its apps' data"); err != nil {
		return err
	}
	if !logs.ValidAppID(id) {
		return apperr.New(apperr.InvalidState, "That does not look like an app id: %s", id).
			WithHint("Use a package name or bundle id, such as com.example.app.")
	}
	if d.Platform == device.IOS {
		return apperr.New(apperr.Unsupported, "Apple's tools cannot clear an app's data").
			WithHint("Uninstall the app and install it again.")
	}
	return m.Android.ClearData(ctx, d, id)
}

// ParseLocation reads "latitude,longitude", such as "37.7749,-122.4194".
func ParseLocation(text string) (lat, lon float64, err error) {
	parts := strings.Split(text, ",")
	if len(parts) == 2 {
		lat, err = strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
		if err == nil {
			lon, err = strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		}
	} else {
		err = errors.New("not two numbers")
	}
	if err != nil || !validPlace(lat, lon) {
		return 0, 0, notAPlace(text)
	}
	return lat, lon, nil
}

// validPlace is whether lat and lon are on the globe. ParseFloat reads "NaN" and "Inf", and simctl
// accepts a latitude of 999, so this is checked here.
func validPlace(lat, lon float64) bool {
	return !math.IsNaN(lat) && !math.IsNaN(lon) && lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180
}

func notAPlace(text string) error {
	return apperr.New(apperr.InvalidState, "That does not look like a place: %q", text).
		WithHint("Write latitude,longitude, such as 37.7749,-122.4194. Latitude is from -90 to 90, longitude from -180 to 180.")
}

// virtualDevice checks that d is a running simulator or emulator. A real phone is not told where
// it is, and its status bar is left alone.
func virtualDevice(d device.Device, what string) error {
	if !d.Ready() {
		return apperr.New(apperr.InvalidState, "%s is not running", d.Name).WithHint("Boot it first.")
	}
	if d.Kind == device.Physical {
		return apperr.New(apperr.Unsupported, "%s is a real device, and %s is left alone", d.Name, what)
	}
	return nil
}

// SetLocation puts a running simulator or emulator at a place. Use ParseLocation for the text a
// person types.
func (m *Manager) SetLocation(ctx context.Context, d device.Device, lat, lon float64) error {
	if err := virtualDevice(d, "its location"); err != nil {
		return err
	}
	if !validPlace(lat, lon) {
		return notAPlace(fmt.Sprintf("%v,%v", lat, lon))
	}
	if d.Platform == device.IOS {
		return m.IOS.SetLocation(ctx, d, lat, lon)
	}
	return m.Android.SetLocation(ctx, d, lat, lon)
}

// ClearLocation takes the fake location off a running simulator. An emulator has no way to forget
// one, short of being restarted.
func (m *Manager) ClearLocation(ctx context.Context, d device.Device) error {
	if err := virtualDevice(d, "its location"); err != nil {
		return err
	}
	if d.Platform == device.Android {
		return apperr.New(apperr.Unsupported, "An emulator cannot forget its location").
			WithHint("Set another place, or restart the emulator.")
	}
	return m.IOS.ClearLocation(ctx, d)
}

// SetStatusBar gives a running simulator or emulator the tidy status bar of a screenshot (clean),
// or takes it off again.
func (m *Manager) SetStatusBar(ctx context.Context, d device.Device, clean bool) error {
	if err := virtualDevice(d, "its status bar"); err != nil {
		return err
	}
	switch {
	case d.Platform == device.IOS && clean:
		return m.IOS.CleanStatusBar(ctx, d)
	case d.Platform == device.IOS:
		return m.IOS.ResetStatusBar(ctx, d)
	case clean:
		return m.Android.CleanStatusBar(ctx, d)
	}
	return m.Android.ResetStatusBar(ctx, d)
}

// ScreenshotName is a file name for a screenshot of d taken at now.
func ScreenshotName(d device.Device, now time.Time) string { return mediaName(d, now, "png") }

// RecordingName is a file name for a screen recording of d started at now.
func RecordingName(d device.Device, now time.Time) string {
	if d.Platform == device.IOS {
		return mediaName(d, now, "mov")
	}
	return mediaName(d, now, "mp4")
}

func mediaName(d device.Device, now time.Time, extension string) string {
	name := strings.Trim(strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_':
			return r
		}
		return '-'
	}, d.Name), "-")
	return fmt.Sprintf("lazymobile-%s-%s.%s", name, now.Format("20060102-150405"), extension)
}

// checkNewFile makes path absolute, and checks that its folder exists and that nothing is there
// yet. Pictures and videos of a screen never replace a file.
func checkNewFile(path string) (string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", apperr.New(apperr.InvalidState, "That does not look like a file name: %v", err)
	}
	if info, err := os.Stat(filepath.Dir(path)); err != nil || !info.IsDir() {
		return "", apperr.New(apperr.InvalidState, "The folder %s does not exist", filepath.Dir(path)).WithHint("Choose a file in a folder that exists.")
	}
	if _, err := os.Lstat(path); err == nil {
		return "", apperr.New(apperr.InvalidState, "%s already exists", path).WithHint("Choose another name: a screenshot or recording never replaces a file.")
	}
	return path, nil
}

// Record films a running simulator's, emulator's or Android phone's screen until ctx ends, or
// until limit has passed when it is not zero. Ending ctx is how a recording is stopped, so it is
// not an error: the video that was made is saved. An Android device records at most three minutes
// (android.MaxRecording). It returns the full path. It never replaces a file, and only the user
// can read the video, since a screen can show personal data.
func (m *Manager) Record(ctx context.Context, d device.Device, path string, limit time.Duration) (string, error) {
	if !d.Ready() {
		return "", apperr.New(apperr.InvalidState, "%s is not running", d.Name).WithHint("Boot or connect it first.")
	}
	if limit < 0 {
		return "", apperr.New(apperr.InvalidState, "A recording cannot last %s", limit)
	}
	path, err := checkNewFile(path)
	if err != nil {
		return "", err
	}
	if d.Platform == device.IOS {
		err = m.IOS.Record(ctx, d, path, limit)
	} else {
		err = m.Android.Record(ctx, d, path, limit)
	}
	if err != nil {
		return "", err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return "", apperr.New(apperr.CommandFailed, "Could not restrict %s: %v", path, err)
	}
	return path, nil
}

// Screenshot saves a PNG of a running simulator's, emulator's or Android phone's screen, and
// returns the full path. It never replaces a file, and only the user can read the result, since a
// screen can show personal data.
func (m *Manager) Screenshot(ctx context.Context, d device.Device, path string) (string, error) {
	if !d.Ready() {
		return "", apperr.New(apperr.InvalidState, "%s is not running", d.Name).WithHint("Boot or connect it first.")
	}
	path, err := checkNewFile(path)
	if err != nil {
		return "", err
	}
	if d.Platform == device.IOS {
		if err := m.IOS.Screenshot(ctx, d, path); err != nil {
			return "", err
		}
		// simctl writes the file with the default permissions.
		if err := os.Chmod(path, 0o600); err != nil {
			return "", apperr.New(apperr.CommandFailed, "Could not restrict %s: %v", path, err)
		}
		return path, nil
	}
	png, err := m.Android.Screenshot(ctx, d)
	if err != nil {
		return "", err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", apperr.New(apperr.CommandFailed, "Could not save the screenshot: %v", err)
	}
	if _, err := file.Write(png); err != nil {
		file.Close()
		return "", apperr.New(apperr.CommandFailed, "Could not save the screenshot: %v", err)
	}
	if err := file.Close(); err != nil {
		return "", apperr.New(apperr.CommandFailed, "Could not save the screenshot: %v", err)
	}
	return path, nil
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

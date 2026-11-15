package android

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/logs"
	"github.com/0xrnp/lazymobile/internal/run"
)

const (
	listTimeout   = 20 * time.Second
	queryTimeout  = 5 * time.Second
	actionTimeout = 60 * time.Second
	// avdmanager starts a JVM and may read the whole SDK, so it gets longer.
	avdmanagerTimeout = 2 * time.Minute
	bootWaitTimeout   = 5 * time.Minute
	bootPoll          = 2 * time.Second
	// How long a new emulator must stay up to count as started. Bad AVDs and missing system
	// images make it exit within a second or two.
	launchGrace = 3 * time.Second
)

// Client runs the Android SDK tools.
type Client struct {
	Runner run.Runner
	Opts   Options
	// Grace overrides how long a new emulator must stay up. Tests shorten it.
	Grace time.Duration
	// Poll overrides how often WaitBooted asks. Tests shorten it.
	Poll time.Duration
}

// New returns a client that runs the real tools.
func New(r run.Runner, opts Options) *Client {
	return &Client{Runner: r, Opts: opts}
}

func (c *Client) run(ctx context.Context, argv []string, timeout time.Duration) run.Result {
	return c.Runner.Run(ctx, argv, run.Options{Timeout: timeout})
}

func ok(res run.Result) bool {
	return res.Code == 0 && !res.Missing && res.Err == nil && !res.TimedOut
}

// query runs `adb -s <serial> <args...>` and returns trimmed stdout, or false on any failure.
func (c *Client) query(ctx context.Context, adb, serial string, args ...string) (string, bool) {
	res := c.run(ctx, append([]string{adb, "-s", serial}, args...), queryTimeout)
	if !ok(res) {
		return "", false
	}
	return strings.TrimSpace(res.Stdout), true
}

// List lists AVDs, running emulators and connected devices. A tool that is missing or fails does
// not hide what the other found: the devices come back with the errors.
func (c *Client) List(ctx context.Context) ([]device.Device, []error) {
	var (
		names    []string
		entries  []ADBEntry
		namesErr error
		adbErr   error
		adb      string
		wg       sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		emulator, err := c.Opts.tool("emulator")
		if err != nil {
			namesErr = err
			return
		}
		argv := []string{emulator, "-list-avds"}
		res := c.run(ctx, argv, listTimeout)
		if namesErr = apperr.FromResult(res, argv, installHint["emulator"]); namesErr == nil {
			names = ParseAVDNames(res.Stdout)
		}
	}()
	go func() {
		defer wg.Done()
		path, err := c.Opts.tool("adb")
		if err != nil {
			adbErr = err
			return
		}
		argv := []string{path, "devices", "-l"}
		res := c.run(ctx, argv, listTimeout)
		if adbErr = apperr.FromResult(res, argv, installHint["adb"]); adbErr == nil {
			adb, entries = path, ParseADBDevices(res.Stdout)
		}
	}()
	wg.Wait()

	var errs []error
	for _, err := range []error{namesErr, adbErr} {
		if err != nil {
			errs = append(errs, err)
		}
	}

	// Ask each usable device what we need: emulators their AVD and boot state, all of them their
	// Android version. There is one goroutine per attached device, which is a handful.
	facts := make([]Fact, len(entries))
	for i, entry := range entries {
		if entry.State != "device" {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			facts[i] = c.ask(ctx, adb, entry.Serial)
		}()
	}
	wg.Wait()
	bySerial := make(map[string]Fact, len(entries))
	for i, entry := range entries {
		bySerial[entry.Serial] = facts[i]
	}
	return device.Dedupe(Combine(names, entries, bySerial, c.Opts.AVDInfo)), errs
}

func (c *Client) ask(ctx context.Context, adb, serial string) Fact {
	var fact Fact
	fact.Release, _ = c.query(ctx, adb, serial, "shell", "getprop", "ro.build.version.release")
	if !IsEmulatorSerial(serial) {
		return fact
	}
	// Prints the name, then "OK".
	if out, found := c.query(ctx, adb, serial, "emu", "avd", "name"); found {
		if name, _, _ := strings.Cut(out, "\n"); strings.TrimSpace(name) != "OK" {
			fact.AVD = strings.TrimSpace(name)
		}
	}
	if out, found := c.query(ctx, adb, serial, "shell", "getprop", "sys.boot_completed"); found {
		fact.BootKnown, fact.Booted = true, out == "1"
	}
	return fact
}

func checkAVD(d device.Device, verb string) error {
	if d.Platform != device.Android || d.Kind != device.Emulator || d.Source != "emulator" {
		return apperr.New(apperr.Unsupported, "Cannot %s %s: it is not an Android virtual device", verb, d.Name)
	}
	return nil
}

// logReason is the most useful line of an emulator log: the first error, else the last line.
func logReason(log string) string {
	last := "no output"
	for _, line := range strings.Split(log, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "ERROR") || strings.Contains(line, "FATAL") || strings.Contains(line, "PANIC") {
			return line
		}
		if line != "" {
			last = line
		}
	}
	return last
}

var unsafeFileChar = regexp.MustCompile(`[^A-Za-z0-9._-]`)

func (c *Client) logPath(name string) (string, error) {
	dir := c.Opts.LogDir
	if dir == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(cache, "lazymobile")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "emulator-"+unsafeFileChar.ReplaceAllString(name, "_")+".log"), nil
}

// BootOptions change how an emulator is started.
type BootOptions struct {
	// Cold ignores the saved snapshot and boots from scratch.
	Cold bool
	// Wipe erases all user data first. It implies a cold boot.
	Wipe bool
	// Wait returns only once Android has finished booting.
	Wait bool
}

// Boot starts an emulator, which keeps running after this program exits. Starting one that is
// already running succeeds, unless a cold boot or wipe was asked for: then it must be shut down
// first.
func (c *Client) Boot(ctx context.Context, d device.Device, opts BootOptions) error {
	if err := checkAVD(d, "boot"); err != nil {
		return err
	}
	if d.Serial != "" && d.State != device.Shutdown {
		if opts.Cold || opts.Wipe {
			verb := "cold boot"
			if opts.Wipe {
				verb = "wipe"
			}
			return apperr.New(apperr.InvalidState, "Cannot %s %s: it is running", verb, d.Name).WithHint("Shut it down first.")
		}
		return nil
	}
	emulator, err := c.Opts.tool("emulator")
	if err != nil {
		return err
	}
	argv := []string{emulator, "-avd", d.ID}
	switch {
	case opts.Wipe:
		argv = append(argv, "-wipe-data")
	case opts.Cold:
		argv = append(argv, "-no-snapshot-load")
	}
	if c.Opts.Headless {
		argv = append(argv, "-no-window")
	}
	argv = append(argv, c.Opts.EmulatorArgs...)

	log, err := c.logPath(d.ID)
	if err != nil {
		return apperr.New(apperr.CommandFailed, "Could not prepare the emulator log: %v", err)
	}
	grace := c.Grace
	if grace == 0 {
		grace = launchGrace
	}
	started, missing, err := run.Detach(argv, log, grace)
	switch {
	case missing:
		return &apperr.Error{Code: apperr.ToolMissing, Message: "The emulator could not be started", Hint: err.Error(), Argv: argv}
	case err != nil:
		return &apperr.Error{Code: apperr.CommandFailed, Message: "The emulator could not be started", Hint: err.Error(), Argv: argv}
	case started.Exited:
		return &apperr.Error{
			Code:    apperr.CommandFailed,
			Message: "The emulator stopped right after starting: " + logReason(started.LogTail),
			Hint:    "Full log: " + log, Argv: argv, ExitCode: started.Code, Stderr: started.LogTail,
		}
	}
	if opts.Wait {
		return c.WaitBooted(ctx, d)
	}
	return nil
}

// WaitBooted returns once the emulator started from this AVD has finished booting.
func (c *Client) WaitBooted(ctx context.Context, d device.Device) error {
	ctx, cancel := context.WithTimeout(ctx, bootWaitTimeout)
	defer cancel()
	poll := c.Poll
	if poll == 0 {
		poll = bootPoll
	}
	timer := time.NewTimer(poll)
	defer timer.Stop()
	for {
		devices, _ := c.List(ctx)
		for _, found := range devices {
			if found.Source == "emulator" && found.ID == d.ID && found.State == device.Booted {
				return nil
			}
		}
		timer.Reset(poll)
		select {
		case <-ctx.Done():
			return apperr.New(apperr.Timeout, "%s did not finish booting in time", d.Name).
				WithHint("It may still be booting. The first boot after a wipe can take several minutes.")
		case <-timer.C:
		}
	}
}

// Shutdown stops a running emulator. One that is not running succeeds at once.
func (c *Client) Shutdown(ctx context.Context, d device.Device) error {
	if d.Platform != device.Android || d.Kind != device.Emulator {
		return apperr.New(apperr.Unsupported, "Cannot shut down %s: it is not an emulator", d.Name)
	}
	if d.Serial == "" || d.State == device.Shutdown {
		return nil
	}
	adb, err := c.Opts.tool("adb")
	if err != nil {
		return err
	}
	argv := []string{adb, "-s", d.Serial, "emu", "kill"}
	res := c.run(ctx, argv, actionTimeout)
	err = apperr.FromResult(res, argv, installHint["adb"])
	if err != nil && strings.Contains(res.Stderr+res.Stdout, "not found") {
		// It stopped on its own between listing and now.
		return nil
	}
	return err
}

// SystemImage is an installed emulator system image.
type SystemImage struct {
	// ID is the sdkmanager package: "system-images;android-36;google_apis_playstore;arm64-v8a".
	ID    string `json:"id"`
	API   string `json:"api"`
	Label string `json:"label"`
}

var leadingDigits = regexp.MustCompile(`^\d+`)

// SystemImages lists installed images that run natively on this machine, newest first.
func (c *Client) SystemImages() []SystemImage {
	sdk, _ := c.Opts.SDKDir()
	if sdk == "" {
		return nil
	}
	abis := map[string]bool{"x86_64": true, "x86": true}
	if normalizeArch(runtime.GOARCH) == "arm64" {
		abis = map[string]bool{"arm64-v8a": true}
	}
	paths, _ := filepath.Glob(filepath.Join(sdk, "system-images", "*", "*", "*"))
	var images []SystemImage
	for _, path := range paths {
		abi := filepath.Base(path)
		tag := filepath.Base(filepath.Dir(path))
		platform := filepath.Base(filepath.Dir(filepath.Dir(path)))
		if !abis[abi] {
			continue
		}
		if _, err := os.Stat(filepath.Join(path, "system.img")); err != nil {
			continue
		}
		api := strings.TrimPrefix(platform, "android-")
		release := ReleaseForAPI(leadingDigits.FindString(api))
		if release == "" {
			release = api
		}
		images = append(images, SystemImage{
			ID:    strings.Join([]string{"system-images", platform, tag, abi}, ";"),
			API:   api,
			Label: fmt.Sprintf("Android %s (API %s) · %s · %s", release, api, tag, abi),
		})
	}
	level := func(image SystemImage) int {
		n, _ := strconv.Atoi(leadingDigits.FindString(image.API))
		return n
	}
	sort.SliceStable(images, func(i, j int) bool {
		if level(images[i]) != level(images[j]) {
			return level(images[i]) > level(images[j])
		}
		return images[i].ID < images[j].ID
	})
	return images
}

// avdmanager finds the tool and the environment it needs. It needs Java, so it is pointed at the
// JDK that was found, which makes it work without JAVA_HOME set.
func (c *Client) avdmanager(ctx context.Context) (path string, env []string, err error) {
	path, err = c.Opts.tool("avdmanager")
	if err != nil {
		return "", nil, err
	}
	java, found := c.Opts.FindJava(ctx, c.Runner)
	if !found {
		return "", nil, apperr.New(apperr.ToolMissing, "avdmanager needs Java, and none was found").
			WithHint("Install a JDK (17 or newer), or Android Studio, which bundles one.")
	}
	if java.Home != "" {
		env = []string{"JAVA_HOME=" + java.Home}
	}
	return path, env, nil
}

// DeviceProfiles lists the hardware profiles avdmanager knows ("pixel_9", "medium_phone", ...).
func (c *Client) DeviceProfiles(ctx context.Context) ([]string, error) {
	avdmanager, env, err := c.avdmanager(ctx)
	if err != nil {
		return nil, err
	}
	argv := []string{avdmanager, "list", "device", "-c"}
	res := c.Runner.Run(ctx, argv, run.Options{Timeout: avdmanagerTimeout, Env: env})
	if err := apperr.FromResult(res, argv, installHint["avdmanager"]); err != nil {
		return nil, err
	}
	return ParseAVDNames(res.Stdout), nil
}

// Create makes an AVD. profile may be empty for avdmanager's default hardware.
func (c *Client) Create(ctx context.Context, name, image, profile string) error {
	if !ValidAVDName(name) {
		return apperr.New(apperr.InvalidState, "Invalid name: %s", name).
			WithHint("Use letters, digits, dots, dashes and underscores, starting with a letter or digit.")
	}
	avdmanager, env, err := c.avdmanager(ctx)
	if err != nil {
		return err
	}
	argv := []string{avdmanager, "create", "avd", "-n", name, "-k", image}
	if profile != "" {
		argv = append(argv, "-d", profile)
	}
	// It asks "Do you wish to create a custom hardware profile? [no]".
	res := c.Runner.Run(ctx, argv, run.Options{Timeout: avdmanagerTimeout, Env: env, Stdin: "no\n"})
	if err := apperr.FromResult(res, argv, installHint["avdmanager"]); err != nil {
		return err
	}
	// Some versions exit 0 after printing an error.
	if trimmed := strings.TrimSpace(res.Stderr); strings.HasPrefix(trimmed, "Error:") {
		line, _, _ := strings.Cut(trimmed, "\n")
		return &apperr.Error{Code: apperr.CommandFailed, Message: "avdmanager failed: " + line, Argv: argv, Stderr: res.Stderr}
	}
	return nil
}

// Delete deletes an AVD. It must not be running.
func (c *Client) Delete(ctx context.Context, d device.Device) error {
	if err := checkAVD(d, "delete"); err != nil {
		return err
	}
	if d.Serial != "" && d.State != device.Shutdown {
		return apperr.New(apperr.InvalidState, "Cannot delete %s: it is running", d.Name).WithHint("Shut it down first.")
	}
	avdmanager, env, err := c.avdmanager(ctx)
	if err != nil {
		return err
	}
	argv := []string{avdmanager, "delete", "avd", "-n", d.ID}
	res := c.Runner.Run(ctx, argv, run.Options{Timeout: avdmanagerTimeout, Env: env})
	return apperr.FromResult(res, argv, installHint["avdmanager"])
}

func badAddress(verb, address string) error {
	return apperr.New(apperr.InvalidState, "Cannot %s %s: expected host:port", verb, address).
		WithHint("On the phone: Settings > Developer options > Wireless debugging shows the address and port.")
}

var pairingCode = regexp.MustCompile(`^\d{6}$`)

// Pair pairs with a phone using the code from "Pair device with pairing code" (Android 11+).
// The pairing address differs from the connect address.
func (c *Client) Pair(ctx context.Context, address, code string) error {
	if !ValidAddress(address) {
		return badAddress("pair with", address)
	}
	if !pairingCode.MatchString(code) {
		return apperr.New(apperr.InvalidState, "The pairing code is six digits")
	}
	adb, err := c.Opts.tool("adb")
	if err != nil {
		return err
	}
	argv := []string{adb, "pair", address, code}
	res := c.run(ctx, argv, actionTimeout)
	if err := apperr.FromResult(res, argv, installHint["adb"]); err != nil {
		return err
	}
	if !strings.Contains(res.Stdout, "Successfully paired") {
		return &apperr.Error{
			Code:    apperr.CommandFailed,
			Message: "Pairing failed: " + strings.TrimSpace(res.Stdout+res.Stderr),
			Hint:    "Check the code and address, and keep the pairing dialog open on the phone.",
			Argv:    argv,
		}
	}
	return nil
}

// Connect connects to a phone over Wi-Fi. adb exits 0 even when this fails, so the output
// decides.
func (c *Client) Connect(ctx context.Context, address string) error {
	if !ValidAddress(address) {
		return badAddress("connect to", address)
	}
	adb, err := c.Opts.tool("adb")
	if err != nil {
		return err
	}
	argv := []string{adb, "connect", address}
	res := c.run(ctx, argv, actionTimeout)
	if err := apperr.FromResult(res, argv, installHint["adb"]); err != nil {
		return err
	}
	out := res.Stdout + res.Stderr
	if strings.Contains(out, "connected to") && !strings.Contains(out, "cannot") && !strings.Contains(out, "failed") {
		return nil
	}
	return &apperr.Error{
		Code:    apperr.CommandFailed,
		Message: "Could not connect: " + strings.TrimSpace(out),
		Hint:    "Pair first if this phone was never paired, and make sure both are on the same network.",
		Argv:    argv,
	}
}

// Disconnect disconnects a Wi-Fi device.
func (c *Client) Disconnect(ctx context.Context, d device.Device) error {
	if d.Platform != device.Android || d.Transport != "wifi" {
		return apperr.New(apperr.Unsupported, "Only Android devices connected over Wi-Fi can be disconnected")
	}
	adb, err := c.Opts.tool("adb")
	if err != nil {
		return err
	}
	argv := []string{adb, "disconnect", d.TargetID()}
	return apperr.FromResult(c.run(ctx, argv, actionTimeout), argv, installHint["adb"])
}

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

// ADB is the path of adb, for callers that stream from it.
func (c *Client) ADB() (string, error) {
	return c.Opts.tool("adb")
}

// InstalledApps lists the apps a user installed on a running emulator or phone, by package name.
// Android's own apps are left out. Android does not say an app's name without a slower query, so
// only the package name is given.
func (c *Client) InstalledApps(ctx context.Context, d device.Device) ([]device.App, error) {
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
	slices.SortFunc(apps, func(a, b device.App) int { return strings.Compare(a.ID, b.ID) })
	return apps, nil
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

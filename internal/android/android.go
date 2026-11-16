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

// SetLocation puts a running emulator at a place. Only an emulator has a console to ask: a phone
// has no way to be told where it is.
func (c *Client) SetLocation(ctx context.Context, d device.Device, lat, lon float64) error {
	adb, err := c.emulator(d, "set the location")
	if err != nil {
		return err
	}
	// The console takes the longitude first, and answers KO with exit code 0 when it refuses.
	argv := []string{adb, "-s", d.TargetID(), "emu", "geo", "fix",
		strconv.FormatFloat(lon, 'f', -1, 64), strconv.FormatFloat(lat, 'f', -1, 64)}
	res := c.run(ctx, argv, actionTimeout)
	if err := apperr.FromResult(res, argv, installHint["adb"]); err != nil {
		return err
	}
	if out := strings.TrimSpace(res.Stdout + res.Stderr); strings.Contains(out, "KO") {
		return &apperr.Error{Code: apperr.CommandFailed, Message: "The emulator did not set the location: " + out, Argv: argv}
	}
	return nil
}

// emulator checks that d is a running emulator, and returns the path of adb.
func (c *Client) emulator(d device.Device, what string) (string, error) {
	adb, err := c.runningDevice(d)
	if err != nil {
		return "", err
	}
	if d.Kind != device.Emulator {
		return "", apperr.New(apperr.Unsupported, "Cannot %s on %s: it is not an emulator", what, d.Name).
			WithHint("Only emulators can be told where they are, or given a tidy status bar.")
	}
	return adb, nil
}

// demo sends one command to the status bar's demo mode.
func (c *Client) demo(ctx context.Context, adb string, d device.Device, command string, extras ...string) error {
	argv := []string{adb, "-s", d.TargetID(), "shell", "am", "broadcast", "-a", "com.android.systemui.demo", "-e", "command", command}
	for i := 0; i+1 < len(extras); i += 2 {
		argv = append(argv, "-e", extras[i], extras[i+1])
	}
	return apperr.FromResult(c.run(ctx, argv, actionTimeout), argv, installHint["adb"])
}

// CleanStatusBar gives a running emulator the tidy status bar of a screenshot, through the demo
// mode of System UI: 12:00, a full battery, full signal, and no notification icons.
func (c *Client) CleanStatusBar(ctx context.Context, d device.Device) error {
	adb, err := c.emulator(d, "change the status bar")
	if err != nil {
		return err
	}
	setting := []string{adb, "-s", d.TargetID(), "shell", "settings", "put", "global", "sysui_demo_allowed", "1"}
	if err := apperr.FromResult(c.run(ctx, setting, actionTimeout), setting, installHint["adb"]); err != nil {
		return err
	}
	steps := []struct {
		command string
		extras  []string
	}{
		{"enter", nil},
		{"clock", []string{"hhmm", "1200"}},
		{"battery", []string{"level", "100", "plugged", "false"}},
		{"network", []string{"wifi", "show", "level", "4", "fully", "true"}},
		{"network", []string{"mobile", "show", "datatype", "none", "level", "4", "fully", "true"}},
		{"notifications", []string{"visible", "false"}},
	}
	for _, step := range steps {
		if err := c.demo(ctx, adb, d, step.command, step.extras...); err != nil {
			return err
		}
	}
	return nil
}

// ResetStatusBar leaves the demo mode of a running emulator's status bar.
func (c *Client) ResetStatusBar(ctx context.Context, d device.Device) error {
	adb, err := c.emulator(d, "change the status bar")
	if err != nil {
		return err
	}
	return c.demo(ctx, adb, d, "exit")
}

// androidPermissions are the permissions a person can name, and what each is on every Android
// version. Photos and notifications depend on the version, and are handled apart.
var androidPermissions = map[string][]string{
	"camera":     {"android.permission.CAMERA"},
	"microphone": {"android.permission.RECORD_AUDIO"},
	"location":   {"android.permission.ACCESS_FINE_LOCATION", "android.permission.ACCESS_COARSE_LOCATION"},
	"contacts":   {"android.permission.READ_CONTACTS", "android.permission.WRITE_CONTACTS"},
	"calendar":   {"android.permission.READ_CALENDAR", "android.permission.WRITE_CALENDAR"},
}

// PermissionNames are the permissions of an Android app that a person can name. A full name such
// as android.permission.READ_SMS works too.
func PermissionNames() []string {
	return []string{"camera", "microphone", "location", "photos", "contacts", "calendar", "notifications"}
}

// fullPermission is a permission written out, such as android.permission.READ_SMS.
var fullPermission = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z0-9_]+)+$`)

// permissionsFor is what name means on a device: one or more full permission names.
func (c *Client) permissionsFor(ctx context.Context, adb string, d device.Device, name string) ([]string, error) {
	if list, known := androidPermissions[name]; known {
		return list, nil
	}
	switch name {
	case "photos", "notifications":
		sdk, err := c.sdkLevel(ctx, adb, d)
		if err != nil {
			return nil, err
		}
		switch {
		case name == "notifications" && sdk < 33:
			return nil, apperr.New(apperr.Unsupported, "Notifications are not a permission before Android 13").WithHint("%s runs API %d.", d.Name, sdk)
		case name == "notifications":
			return []string{"android.permission.POST_NOTIFICATIONS"}, nil
		case sdk >= 33:
			return []string{"android.permission.READ_MEDIA_IMAGES"}, nil
		}
		return []string{"android.permission.READ_EXTERNAL_STORAGE"}, nil
	case "":
	default:
		if fullPermission.MatchString(name) {
			return []string{name}, nil
		}
	}
	return nil, apperr.New(apperr.InvalidState, "%q is not a permission", name).
		WithHint("Use one of: %s, or a full name such as android.permission.READ_SMS.", strings.Join(PermissionNames(), ", "))
}

// sdkLevel is the API level of a running device.
func (c *Client) sdkLevel(ctx context.Context, adb string, d device.Device) (int, error) {
	argv := []string{adb, "-s", d.TargetID(), "shell", "getprop", "ro.build.version.sdk"}
	res := c.run(ctx, argv, queryTimeout)
	if err := apperr.FromResult(res, argv, installHint["adb"]); err != nil {
		return 0, err
	}
	level, err := strconv.Atoi(strings.TrimSpace(res.Stdout))
	if err != nil {
		return 0, &apperr.Error{Code: apperr.ParseFailed, Message: "The device did not say its Android version", Argv: argv, Stderr: res.Stdout}
	}
	return level, nil
}

// SetPermission grants or revokes a permission of an app, by package name, on a running emulator
// or phone. A name that stands for several permissions, such as location, changes each the app
// asks for.
func (c *Client) SetPermission(ctx context.Context, d device.Device, id, name string, grant bool) error {
	adb, err := c.appTool(d, id)
	if err != nil {
		return err
	}
	permissions, err := c.permissionsFor(ctx, adb, d, name)
	if err != nil {
		return err
	}
	verb := "revoke"
	if grant {
		verb = "grant"
	}
	var changed bool
	var firstErr error
	for _, permission := range permissions {
		argv := []string{adb, "-s", d.TargetID(), "shell", "pm", verb, id, permission}
		res := c.run(ctx, argv, actionTimeout)
		out := res.Stdout + res.Stderr
		switch {
		case strings.Contains(out, "package not found"):
			return &apperr.Error{Code: apperr.NotFound, Message: fmt.Sprintf("%s is not installed on %s", id, d.Name),
				Hint: fmt.Sprintf("Run `lazymobile apps %q` to see what is installed.", d.Name), Argv: argv, ExitCode: res.Code}
		case strings.Contains(out, "Unknown permission"):
			return apperr.New(apperr.InvalidState, "%s is not a permission on %s", name, d.Name).
				WithHint("Use one of: %s, or a full name such as android.permission.READ_SMS.", strings.Join(PermissionNames(), ", "))
		case strings.Contains(out, "has not requested permission"), strings.Contains(out, "not a changeable permission type"):
			if firstErr == nil {
				firstErr = apperr.New(apperr.InvalidState, "%s does not ask for the %s permission", id, name).
					WithHint("An app can only be given the permissions it asks for.")
			}
			continue
		}
		if err := apperr.FromResult(res, argv, installHint["adb"]); err != nil {
			if firstErr == nil || len(permissions) == 1 {
				firstErr = err
			}
			continue
		}
		changed = true
	}
	if !changed {
		return firstErr
	}
	return nil
}

// ClearData erases all the data of an app, by package name, on a running emulator or phone, as
// if it had just been installed. The app is stopped.
func (c *Client) ClearData(ctx context.Context, d device.Device, id string) error {
	adb, err := c.appTool(d, id)
	if err != nil {
		return err
	}
	// pm clear says only "Failed" for a package that does not exist.
	if _, err := c.AppInfo(ctx, d, id); err != nil {
		return err
	}
	argv := []string{adb, "-s", d.TargetID(), "shell", "pm", "clear", id}
	res := c.run(ctx, argv, actionTimeout)
	if err := apperr.FromResult(res, argv, installHint["adb"]); err != nil {
		return err
	}
	if !strings.Contains(res.Stdout, "Success") {
		return &apperr.Error{Code: apperr.CommandFailed, Message: "The device did not clear the data of " + id + ": " + strings.TrimSpace(res.Stdout+res.Stderr), Argv: argv}
	}
	return nil
}

var nightMode = regexp.MustCompile(`Night mode:\s*(\w+)`)

// Appearance is "dark", "light", or "auto" when the device follows the time of day. It needs
// Android 10 or newer.
func (c *Client) Appearance(ctx context.Context, d device.Device) (string, error) {
	adb, err := c.runningDevice(d)
	if err != nil {
		return "", err
	}
	argv := []string{adb, "-s", d.TargetID(), "shell", "cmd", "uimode", "night"}
	res := c.run(ctx, argv, actionTimeout)
	if err := apperr.FromResult(res, argv, installHint["adb"]); err != nil {
		return "", err
	}
	m := nightMode.FindStringSubmatch(res.Stdout)
	if m == nil {
		return "", &apperr.Error{Code: apperr.ParseFailed, Message: "The device did not say its night mode", Hint: "Dark mode needs Android 10 or newer.", Argv: argv, Stderr: res.Stdout}
	}
	switch m[1] {
	case "yes":
		return "dark", nil
	case "no":
		return "light", nil
	}
	return "auto", nil
}

// SetAppearance switches a running emulator or phone to "dark" or "light".
func (c *Client) SetAppearance(ctx context.Context, d device.Device, mode string) error {
	adb, err := c.runningDevice(d)
	if err != nil {
		return err
	}
	night := "no"
	if mode == "dark" {
		night = "yes"
	}
	argv := []string{adb, "-s", d.TargetID(), "shell", "cmd", "uimode", "night", night}
	res := c.run(ctx, argv, actionTimeout)
	if err := apperr.FromResult(res, argv, installHint["adb"]); err != nil {
		return err
	}
	// Android before 10 has no such command, and adb still exits 0.
	if !nightMode.MatchString(res.Stdout) {
		return &apperr.Error{Code: apperr.CommandFailed, Message: "The device did not change its night mode: " + strings.TrimSpace(res.Stdout+res.Stderr),
			Hint: "Dark mode needs Android 10 or newer.", Argv: argv}
	}
	return nil
}

var pngSignature = "\x89PNG\r\n\x1a\n"

// Screenshot returns a PNG of a running emulator's or phone's screen.
func (c *Client) Screenshot(ctx context.Context, d device.Device) ([]byte, error) {
	adb, err := c.runningDevice(d)
	if err != nil {
		return nil, err
	}
	// exec-out gives the bytes as they are. `shell` would turn the PNG's newlines into CR LF.
	argv := []string{adb, "-s", d.TargetID(), "exec-out", "screencap", "-p"}
	res := c.run(ctx, argv, actionTimeout)
	if err := apperr.FromResult(res, argv, installHint["adb"]); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(res.Stdout, pngSignature) {
		return nil, &apperr.Error{Code: apperr.ParseFailed, Message: "The device did not send a screenshot", Argv: argv, Stderr: res.Stderr}
	}
	return []byte(res.Stdout), nil
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

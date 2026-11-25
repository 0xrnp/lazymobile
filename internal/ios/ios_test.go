package ios

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/run"
	"github.com/0xrnp/lazymobile/internal/run/runtest"
)

var (
	shutdownSim = device.Device{ID: udidShutdown, Name: "iPhone 16 Pro", Platform: device.IOS, Kind: device.Simulator, State: device.Shutdown}
	bootedSim   = device.Device{ID: udidBooted, Name: "iPhone 16", Platform: device.IOS, Kind: device.Simulator, State: device.Booted}
	pixel       = device.Device{ID: "Pixel_9", Name: "Pixel 9", Platform: device.Android, Kind: device.Emulator}
)

func client(fake *runtest.Fake, existing ...string) *Client {
	return &Client{Runner: fake, Exists: func(path string) bool {
		for _, p := range existing {
			if p == path {
				return true
			}
		}
		return false
	}}
}

func TestListKeepsDevicesWhenOneSourceFails(t *testing.T) {
	fake := &runtest.Fake{Answer: func(command string, argv []string, _ run.Options) run.Result {
		switch {
		case strings.Contains(command, "simctl list devices"):
			return runtest.Out(simctlFixture())
		case strings.Contains(command, "devicectl list devices"):
			return runtest.Fail(1, "devicectl: device support is not installed")
		}
		return run.Result{Missing: true}
	}}
	devices, errs := client(fake).List(context.Background(), ListOptions{Simulators: true, Physical: true})
	if len(devices) != 9 {
		t.Errorf("got %d devices, want the 9 simulators", len(devices))
	}
	if len(errs) != 1 || apperr.CodeOf(errs[0]) != apperr.CommandFailed {
		t.Errorf("errors = %v, want one command failure", errs)
	}
}

func TestListReadsTheFileDevicectlWrites(t *testing.T) {
	fake := &runtest.Fake{Answer: func(command string, argv []string, _ run.Options) run.Result {
		if strings.Contains(command, "devicectl list devices") {
			if err := os.WriteFile(argv[len(argv)-1], []byte(devicectlFixture("both")), 0o600); err != nil {
				t.Error(err)
			}
			return run.Result{}
		}
		return run.Result{Missing: true}
	}}
	devices, errs := client(fake).List(context.Background(), ListOptions{Physical: true})
	if len(errs) != 0 || len(devices) != 5 {
		t.Errorf("got %d devices and errors %v, want 5 and none", len(devices), errs)
	}
}

func TestListSaysXcodeIsMissingOnce(t *testing.T) {
	devices, errs := client(&runtest.Fake{}).List(context.Background(), ListOptions{Simulators: true, Physical: true})
	if len(devices) != 0 || len(errs) != 1 || apperr.CodeOf(errs[0]) != apperr.ToolMissing {
		t.Errorf("devices = %v, errors = %v, want one tool_missing", devices, errs)
	}
	if !strings.Contains(errs[0].Error(), "Install Xcode") {
		t.Errorf("error should say how to fix it: %v", errs[0])
	}
}

func TestSimulatorActions(t *testing.T) {
	const wrongState = "An error was encountered processing the command (domain=com.apple.CoreSimulator.SimError, code=405):\nUnable to boot device in current state: Booted"
	tests := []struct {
		name   string
		act    func(*Client) error
		answer run.Result
		want   apperr.Code
	}{
		{"boot succeeds", func(c *Client) error { return c.Boot(context.Background(), shutdownSim, BootOptions{}) }, run.Result{}, ""},
		{"booting a booted simulator succeeds", func(c *Client) error { return c.Boot(context.Background(), bootedSim, BootOptions{}) },
			runtest.Fail(149, wrongState), ""},
		{"shutting down a stopped simulator succeeds", func(c *Client) error { return c.Shutdown(context.Background(), shutdownSim) },
			runtest.Fail(149, "Unable to shutdown device in current state: Shutdown"), ""},
		{"erasing a booted simulator is refused", func(c *Client) error { return c.Erase(context.Background(), bootedSim) },
			runtest.Fail(149, "Unable to erase contents and settings in current state: Booted"), apperr.InvalidState},
		{"an unknown simulator", func(c *Client) error { return c.Delete(context.Background(), shutdownSim) },
			runtest.Fail(148, "Invalid device: "+udidShutdown), apperr.NotFound},
		{"another failure is passed on", func(c *Client) error { return c.Boot(context.Background(), shutdownSim, BootOptions{}) },
			runtest.Fail(1, "Something else broke"), apperr.CommandFailed},
		{"a state message with no state is a plain failure", func(c *Client) error { return c.Boot(context.Background(), shutdownSim, BootOptions{}) },
			runtest.Fail(149, "Unable to boot device in current state: "), apperr.CommandFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &runtest.Fake{Answer: func(string, []string, run.Options) run.Result { return tt.answer }}
			err := tt.act(client(fake))
			if apperr.CodeOf(err) != tt.want {
				t.Errorf("error = %v (code %q), want code %q", err, apperr.CodeOf(err), tt.want)
			}
		})
	}
}

func TestActionsRefuseOtherDevices(t *testing.T) {
	fake := &runtest.Fake{}
	c := client(fake)
	for name, err := range map[string]error{
		"boot":     c.Boot(context.Background(), pixel, BootOptions{}),
		"shutdown": c.Shutdown(context.Background(), pixel),
		"erase":    c.Erase(context.Background(), pixel),
		"delete":   c.Delete(context.Background(), pixel),
	} {
		if apperr.CodeOf(err) != apperr.Unsupported {
			t.Errorf("%s: error = %v, want unsupported", name, err)
		}
	}
	if len(fake.Calls()) != 0 {
		t.Errorf("no tool should run for a device of another kind, ran %v", fake.Calls())
	}
}

const developerDir = "/Applications/Xcode.app/Contents/Developer"

// openAnswers builds a runner where simctl works, xcode-select answers, and only the `open`
// commands containing one of working succeed.
func openAnswers(selected run.Result, working ...string) *runtest.Fake {
	return &runtest.Fake{Answer: func(command string, _ []string, _ run.Options) run.Result {
		switch {
		case strings.HasPrefix(command, "xcode-select"):
			return selected
		case strings.HasPrefix(command, "open "):
			for _, part := range working {
				if strings.Contains(command, part) {
					return run.Result{}
				}
			}
			return runtest.Fail(1, "Unable to find application")
		}
		return run.Result{}
	}}
}

func lastOpen(fake *runtest.Fake) string {
	last := ""
	for _, call := range fake.Calls() {
		if strings.HasPrefix(call, "open ") {
			last = call
		}
	}
	return last
}

func TestBootOpensTheWindowOfTheSelectedXcode(t *testing.T) {
	simulatorApp := developerDir + "/Applications/Simulator.app"
	deviceHubApp := "/Applications/Xcode.app/Contents/Applications/DeviceHub.app"
	link := "devices://device/open?id=" + udidShutdown

	tests := []struct {
		name     string
		selected run.Result
		existing []string
		working  []string
		want     string
	}{
		{"Xcode 26 and earlier: Simulator.app inside the selected Xcode", runtest.Out(developerDir + "\n"),
			[]string{simulatorApp}, []string{"open -a"},
			"open -a " + simulatorApp + " --args -CurrentDeviceUDID " + udidShutdown},
		{"Xcode 27: DeviceHub next to the developer folder, opened by URL", runtest.Out(developerDir + "\n"),
			[]string{deviceHubApp}, []string{"open -a"}, "open -a " + deviceHubApp + " " + link},
		{"no app in the selected Xcode: Simulator by bundle id", runtest.Out(developerDir + "\n"),
			nil, []string{"com.apple.iphonesimulator"}, "open -b com.apple.iphonesimulator --args -CurrentDeviceUDID " + udidShutdown},
		{"only DeviceHub is installed somewhere", runtest.Fail(2, "no developer directory"),
			nil, []string{"com.apple.dt.Devices"}, "open -b com.apple.dt.Devices " + link},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := openAnswers(tt.selected, tt.working...)
			if err := client(fake, tt.existing...).Boot(context.Background(), shutdownSim, BootOptions{OpenApp: true}); err != nil {
				t.Fatal(err)
			}
			if got := lastOpen(fake); got != tt.want {
				t.Errorf("opened with %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBootSaysWhenNoWindowCouldBeOpened(t *testing.T) {
	fake := openAnswers(runtest.Out(developerDir))
	err := client(fake).Boot(context.Background(), shutdownSim, BootOptions{OpenApp: true})
	if apperr.CodeOf(err) != apperr.CommandFailed || !strings.Contains(err.Error(), "neither Simulator nor DeviceHub") {
		t.Errorf("error = %v", err)
	}
	if !strings.Contains(err.Error(), "--no-window") {
		t.Errorf("the error should say how to carry on without a window: %v", err)
	}
}

func TestBootWithoutAWindowOpensNothing(t *testing.T) {
	fake := openAnswers(runtest.Out(developerDir))
	if err := client(fake).Boot(context.Background(), shutdownSim, BootOptions{}); err != nil {
		t.Fatal(err)
	}
	if fake.Called("open ") || fake.Called("xcode-select") {
		t.Errorf("ran %v", fake.Calls())
	}
}

func TestBootWaitsWhenAsked(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{"simctl": {}})
	if err := client(fake).Boot(context.Background(), shutdownSim, BootOptions{Wait: true}); err != nil {
		t.Fatal(err)
	}
	if !fake.Called("simctl bootstatus " + udidShutdown) {
		t.Errorf("did not wait for the boot: %v", fake.Calls())
	}
}

func TestCreate(t *testing.T) {
	tests := []struct {
		name    string
		simName string
		answer  run.Result
		want    apperr.Code
	}{
		{"prints the new id", "Mine", runtest.Out("00000000-0000-4000-8000-00000000ABCD\n"), ""},
		{"unexpected output", "Mine", runtest.Out("created!\n"), apperr.ParseFailed},
		{"simctl refuses", "Mine", runtest.Fail(1, "Incompatible device"), apperr.CommandFailed},
		{"blank name", "  ", run.Result{}, apperr.InvalidState},
		{"name with a line break", "a\nb", run.Result{}, apperr.InvalidState},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &runtest.Fake{Answer: func(string, []string, run.Options) run.Result { return tt.answer }}
			udid, err := client(fake).Create(context.Background(), tt.simName, "type", "runtime")
			if apperr.CodeOf(err) != tt.want {
				t.Fatalf("error = %v, want code %q", err, tt.want)
			}
			if tt.want == "" && udid != "00000000-0000-4000-8000-00000000ABCD" {
				t.Errorf("udid = %q", udid)
			}
		})
	}
}

func TestRuntimesFillsDeviceTypesFromOlderSimctl(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{
		"list runtimes": runtest.Out(encode(object{"runtimes": []object{
			{"identifier": runtimePrefix + "iOS-12-1", "name": "iOS 12.1", "version": "12.1", "availability": "(available)"},
		}})),
		"list devicetypes": runtest.Out(encode(object{"devicetypes": []object{{"identifier": "phone", "name": "iPhone X"}}})),
	})
	runtimes, err := client(fake).Runtimes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(runtimes) != 1 || len(runtimes[0].DeviceTypes) != 1 || runtimes[0].DeviceTypes[0].Name != "iPhone X" {
		t.Errorf("runtimes = %+v", runtimes)
	}
}

func TestAppExecutable(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{
		"get_app_container": runtest.Out("/fake/Containers/Runner.app\n"),
		"plutil":            runtest.Out("Runner\n"),
	})
	name, err := client(fake).AppExecutable(context.Background(), bootedSim, "com.example.app")
	if err != nil || name != "Runner" {
		t.Fatalf("name = %q, err = %v", name, err)
	}
	if !fake.Called("/fake/Containers/Runner.app/Info.plist") {
		t.Errorf("did not read the app's Info.plist: %v", fake.Calls())
	}

	notInstalledApp := runtest.Table(map[string]run.Result{"get_app_container": runtest.Fail(2, "No such file or directory")})
	_, err = client(notInstalledApp).AppExecutable(context.Background(), bootedSim, "com.example.app")
	if err == nil || !strings.Contains(err.Error(), "installed on iPhone 16") {
		t.Errorf("error should ask whether the app is installed: %v", err)
	}
}

// simulatorAppAnswers lists one simulator in this state and has these apps running.
func simulatorAppAnswers(state string, running ...string) *runtest.Fake {
	answers := map[string]run.Result{
		"simctl list devices": runtest.Out(`{"devices": {"com.apple.CoreSimulator.SimRuntime.iOS-27-2": [
			{"udid": "` + udidShutdown + `", "name": "iPhone 17", "state": "` + state + `", "isAvailable": true}]}}`),
		"pgrep":     runtest.Fail(1, ""),
		"osascript": {},
	}
	for _, process := range running {
		answers["pgrep -x "+process] = run.Result{}
	}
	return runtest.Table(answers)
}

func TestQuitSimulatorAppsQuitsOnlyRunningApps(t *testing.T) {
	fake := simulatorAppAnswers("Shutdown", "DeviceHub")
	if err := client(fake).QuitSimulatorApps(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !fake.Called(`osascript -e tell application id "com.apple.dt.Devices" to quit`) {
		t.Errorf("DeviceHub was not quit: %v", fake.Calls())
	}
	if fake.Called("com.apple.iphonesimulator") {
		t.Errorf("Simulator is not running, so it must not be addressed: %v", fake.Calls())
	}
}

// DeviceHub stops every simulator when it quits, so it is left alone while one runs.
func TestQuitSimulatorAppsLeavesThemWhileASimulatorRuns(t *testing.T) {
	for _, state := range []string{"Booted", "Booting", "Shutting Down"} {
		fake := simulatorAppAnswers(state, "DeviceHub", "Simulator")
		if err := client(fake).QuitSimulatorApps(context.Background()); err != nil {
			t.Fatal(err)
		}
		if fake.Called("osascript") {
			t.Errorf("%s: ran %v", state, fake.Calls())
		}
	}
}

func TestQuitSimulatorAppsExplainsADeniedPermission(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{
		"simctl list devices": runtest.Out(`{"devices": {}}`),
		"pgrep -x DeviceHub":  {},
		"pgrep":               runtest.Fail(1, ""),
		"osascript":           runtest.Fail(1, "execution error: Not authorized to send Apple events to DeviceHub. (-1743)"),
	})
	err := client(fake).QuitSimulatorApps(context.Background())
	var notQuit *AppNotQuitError
	if !errors.As(err, &notQuit) || !strings.Contains(err.Error(), "DeviceHub could not be quit") || !strings.Contains(err.Error(), "Automation") {
		t.Errorf("error = %v", err)
	}
	if apperr.CodeOf(err) != apperr.CommandFailed {
		t.Errorf("code = %q, want the underlying command_failed", apperr.CodeOf(err))
	}
}

func TestQuitSimulatorAppsDoesNothingWhenNoAppRuns(t *testing.T) {
	fake := simulatorAppAnswers("Shutdown")
	if err := client(fake).QuitSimulatorApps(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fake.Called("osascript") || fake.Called("simctl list") {
		t.Errorf("ran %v", fake.Calls())
	}
}

// simctl listapps prints an old-style property list; this is the JSON plutil makes of a real
// one, shortened, with one app a user installed among the system's.
const listappsJSON = `{
  "com.apple.mobilesafari": {"ApplicationType": "System", "CFBundleDisplayName": "Safari"},
  "com.example.zeta": {"ApplicationType": "User", "CFBundleName": "Zeta"},
  "com.example.app": {"ApplicationType": "User", "CFBundleDisplayName": "Example", "CFBundleName": "ExampleApp",
    "CFBundleShortVersionString": "2.0.0", "CFBundleVersion": "1"}
}`

func TestInstalledAppsAreTheOnesAUserAdded(t *testing.T) {
	var stdin string
	fake := &runtest.Fake{Answer: func(command string, _ []string, opts run.Options) run.Result {
		switch {
		case strings.HasPrefix(command, "xcrun simctl listapps SIM-1"):
			return runtest.Out("{ \"com.example.app\" = { ApplicationType = User; }; }")
		case strings.HasPrefix(command, "plutil -convert json"):
			stdin = opts.Stdin
			return runtest.Out(listappsJSON)
		}
		return run.Result{Missing: true}
	}}
	sim := device.Device{ID: "SIM-1", Name: "iPhone", Platform: device.IOS, Kind: device.Simulator, State: device.Booted}
	apps, err := client(fake).InstalledApps(context.Background(), sim)
	if err != nil {
		t.Fatal(err)
	}
	// Zeta's plist has no version keys, which is not an error.
	want := []device.App{{ID: "com.example.app", Name: "Example", Version: "2.0.0", Build: "1"}, {ID: "com.example.zeta", Name: "Zeta"}}
	if !slices.Equal(apps, want) {
		t.Errorf("apps = %v", apps)
	}
	if !strings.Contains(stdin, "ApplicationType = User") {
		t.Errorf("plutil was given %q, not simctl's output", stdin)
	}

	if _, err := client(fake).InstalledApps(context.Background(), pixel); apperr.CodeOf(err) != apperr.Unsupported {
		t.Errorf("an Android device: %v", err)
	}
}

var iphone = device.Device{ID: "P1", Name: "Phone", Platform: device.IOS, Kind: device.Physical, State: device.Connected}

func TestInstalledAppsOfAnIPhoneComeFromDevicectl(t *testing.T) {
	var written string
	fake := &runtest.Fake{Answer: func(command string, argv []string, _ run.Options) run.Result {
		if !strings.HasPrefix(command, "xcrun devicectl device info apps --device P1 --json-output ") {
			return run.Result{Missing: true}
		}
		written = argv[len(argv)-1]
		if err := os.WriteFile(written, []byte(devicectlAppsFixture()), 0o600); err != nil {
			t.Error(err)
		}
		return run.Result{}
	}}
	apps, err := client(fake).InstalledApps(context.Background(), iphone)
	if err != nil {
		t.Fatal(err)
	}
	want := []device.App{
		{ID: "com.example.app.staging", Name: "Example Staging", Version: "1.4.2", Build: "87"},
		{ID: "com.example.preview", Name: "Example Preview", Version: "2.0.0", Build: "1"},
	}
	if !slices.Equal(apps, want) {
		t.Errorf("apps = %+v", apps)
	}
	// The app ids of someone's phone do not stay in a temporary folder.
	if _, err := os.Stat(filepath.Dir(written)); !os.IsNotExist(err) {
		t.Errorf("the temporary folder %s was left behind (%v)", filepath.Dir(written), err)
	}
}

func TestInstalledAppsOfAnIPhoneThatCannotBeReached(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{
		"devicectl device info apps": runtest.Fail(1, "ERROR: The device is not able to fulfill the requested usage assertion requirements. (com.apple.dt.CoreDeviceError error 4016 (0xFB0))"),
	})
	_, err := client(fake).InstalledApps(context.Background(), iphone)
	if apperr.CodeOf(err) != apperr.CommandFailed || !strings.Contains(err.Error(), "not able to fulfill") {
		t.Errorf("error = %v", err)
	}
}

func TestUninstallFromASimulatorOrAnIPhone(t *testing.T) {
	fake := &runtest.Fake{Answer: func(command string, argv []string, _ run.Options) run.Result {
		if strings.HasPrefix(command, "xcrun devicectl device uninstall app --device P1 com.example.app --json-output ") {
			// devicectl always writes its JSON, and prints "App uninstalled." on success.
			if err := os.WriteFile(argv[len(argv)-1], []byte(`{"info": {"outcome": "success"}}`), 0o600); err != nil {
				t.Error(err)
			}
		}
		return run.Result{}
	}}
	ctx := context.Background()
	if err := client(fake).Uninstall(ctx, bootedSim, "com.example.app"); err != nil {
		t.Errorf("a simulator: %v", err)
	}
	if !fake.Called("xcrun simctl uninstall " + udidBooted + " com.example.app") {
		t.Errorf("ran %v", fake.Calls())
	}
	if err := client(fake).Uninstall(ctx, iphone, "com.example.app"); err != nil {
		t.Errorf("an iPhone: %v", err)
	}
	if err := client(fake).Uninstall(ctx, pixel, "com.example.app"); apperr.CodeOf(err) != apperr.Unsupported {
		t.Errorf("an Android device: %v", err)
	}
}

func TestUninstallFailures(t *testing.T) {
	// simctl exits 149 for a simulator that is shut down.
	shut := runtest.Table(map[string]run.Result{"simctl uninstall": runtest.Fail(149, "Unable to lookup in current state: Shutdown")})
	err := client(shut).Uninstall(context.Background(), shutdownSim, "com.example.app")
	if apperr.CodeOf(err) != apperr.InvalidState || !strings.Contains(err.Error(), "shutdown") {
		t.Errorf("a shut-down simulator: %v", err)
	}

	unreachable := runtest.Table(map[string]run.Result{"devicectl device uninstall": runtest.Fail(1, "ERROR: The device is not able to fulfill the requested usage assertion requirements. (com.apple.dt.CoreDeviceError error 4016 (0xFB0))")})
	err = client(unreachable).Uninstall(context.Background(), iphone, "com.example.app")
	if apperr.CodeOf(err) != apperr.CommandFailed || !strings.Contains(err.Error(), "not able to fulfill") {
		t.Errorf("an iPhone that cannot be reached: %v", err)
	}
}

// What simctl of Xcode 27.2 printed on a real simulator.
const (
	simctlLaunchUnknown = `An error was encountered processing the command (domain=FBSOpenApplicationServiceErrorDomain, code=4):
Simulator device failed to launch com.example.nope.
Underlying error (domain=FBSOpenApplicationServiceErrorDomain, code=4):
	The request to open "com.example.nope" failed.
`
	simctlNothingToTerminate = `An error was encountered processing the command (domain=NSPOSIXErrorDomain, code=3):
Simulator device failed to terminate com.example.app.
found nothing to terminate
Underlying error (domain=NSPOSIXErrorDomain, code=3):
	The request to terminate "com.example.app" failed. found nothing to terminate
	found nothing to terminate
`
	simctlNoContainer = `An error was encountered processing the command (domain=NSPOSIXErrorDomain, code=2):
The operation couldn’t be completed. No such file or directory
No such file or directory
`
)

func TestLaunchOnASimulatorOrAnIPhone(t *testing.T) {
	fake := &runtest.Fake{Answer: func(command string, argv []string, _ run.Options) run.Result {
		if strings.HasPrefix(command, "xcrun devicectl device process launch") {
			if err := os.WriteFile(argv[len(argv)-1], []byte(`{"result": {}}`), 0o600); err != nil {
				t.Error(err)
			}
			return run.Result{}
		}
		return runtest.Out("com.example.app: 10223\n")
	}}
	ctx := context.Background()
	c := client(fake)
	if err := c.Launch(ctx, bootedSim, "com.example.app", false); err != nil {
		t.Fatal(err)
	}
	if err := c.Launch(ctx, bootedSim, "com.example.app", true); err != nil {
		t.Fatal(err)
	}
	if err := c.Launch(ctx, iphone, "com.example.app", false); err != nil {
		t.Fatal(err)
	}
	if err := c.Launch(ctx, iphone, "com.example.app", true); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"xcrun simctl launch " + udidBooted + " com.example.app",
		"xcrun simctl launch --terminate-running-process " + udidBooted + " com.example.app",
		"xcrun devicectl device process launch --device P1 com.example.app --json-output",
		"xcrun devicectl device process launch --device P1 --terminate-existing com.example.app --json-output",
	} {
		if !fake.Called(want) {
			t.Errorf("did not run %q: %v", want, fake.Calls())
		}
	}
	if err := c.Launch(ctx, pixel, "com.example.app", false); apperr.CodeOf(err) != apperr.Unsupported {
		t.Errorf("an Android device: %v", err)
	}
}

func TestLaunchOfAnAppThatIsNotInstalledSaysSo(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{"simctl launch": runtest.Fail(4, simctlLaunchUnknown)})
	err := client(fake).Launch(context.Background(), bootedSim, "com.example.nope", false)
	if apperr.CodeOf(err) != apperr.NotFound || !strings.Contains(err.Error(), "com.example.nope is not installed on iPhone 16") {
		t.Errorf("error = %v", err)
	}
}

func TestStopOnASimulator(t *testing.T) {
	ctx := context.Background()
	running := runtest.Table(map[string]run.Result{"simctl terminate": {}})
	if err := client(running).Stop(ctx, bootedSim, "com.example.app"); err != nil {
		t.Fatal(err)
	}
	if !running.Called("xcrun simctl terminate " + udidBooted + " com.example.app") {
		t.Errorf("ran %v", running.Calls())
	}

	// An app that is installed and not running is already stopped.
	idle := runtest.Table(map[string]run.Result{
		"simctl terminate":         runtest.Fail(3, simctlNothingToTerminate),
		"simctl get_app_container": runtest.Out("/path/to/Example.app\n"),
	})
	if err := client(idle).Stop(ctx, bootedSim, "com.example.app"); err != nil {
		t.Errorf("an app that is not running: %v", err)
	}

	typo := runtest.Table(map[string]run.Result{
		"simctl terminate":         runtest.Fail(3, simctlNothingToTerminate),
		"simctl get_app_container": runtest.Fail(2, simctlNoContainer),
	})
	if err := client(typo).Stop(ctx, bootedSim, "com.example.ap"); apperr.CodeOf(err) != apperr.NotFound {
		t.Errorf("an app that is not installed: %v", err)
	}

	shut := runtest.Table(map[string]run.Result{"simctl terminate": runtest.Fail(149, "Unable to lookup in current state: Shutdown")})
	if err := client(shut).Stop(ctx, shutdownSim, "com.example.app"); apperr.CodeOf(err) != apperr.InvalidState {
		t.Errorf("a simulator that is shut down: %v", err)
	}
}

func TestStopOnAnIPhoneIsRefusedWithoutAskingIt(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{})
	err := client(fake).Stop(context.Background(), iphone, "com.example.app")
	if apperr.CodeOf(err) != apperr.Unsupported || !strings.Contains(err.Error(), "Restart it instead") {
		t.Errorf("error = %v", err)
	}
	if calls := fake.Calls(); len(calls) != 0 {
		t.Errorf("the phone was asked: %v", calls)
	}
}

// What simctl of Xcode 27.2 printed for a link that no app opens (exit code 115) and for a
// screenshot.
const (
	simctlOpenURLUnknown = `An error was encountered processing the command (domain=LSApplicationWorkspaceErrorDomain, code=115):
Simulator device failed to open nosuchscheme://x.
Underlying error (domain=LSApplicationWorkspaceErrorDomain, code=115):
	The operation couldn’t be completed. (LSApplicationWorkspaceErrorDomain error 115.)
`
	simctlScreenshotNote = "Note: No display specified. Defaulting to display: 2C9C7E61-DE00-4779-990F-554225539EF7 (screenID: 1, name: LCD)\nWrote screenshot to: /tmp/x.png\n"
)

func TestOpenURLOnASimulator(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{"simctl openurl": {}})
	if err := client(fake).OpenURL(context.Background(), bootedSim, "myapp://home?id=1&x=2"); err != nil {
		t.Fatal(err)
	}
	if !fake.Called("xcrun simctl openurl " + udidBooted + " myapp://home?id=1&x=2") {
		t.Errorf("ran %v", fake.Calls())
	}
}

func TestOpenURLNoAppHandlesSaysSoAndAnIPhoneIsRefused(t *testing.T) {
	unknown := runtest.Table(map[string]run.Result{"simctl openurl": runtest.Fail(115, simctlOpenURLUnknown)})
	err := client(unknown).OpenURL(context.Background(), bootedSim, "nosuchscheme://x")
	if apperr.CodeOf(err) != apperr.NotFound || !strings.Contains(err.Error(), "No app on iPhone 16 opens that link") {
		t.Errorf("an unknown scheme: %v", err)
	}
	shut := runtest.Table(map[string]run.Result{"simctl openurl": runtest.Fail(149, "Unable to lookup in current state: Shutdown")})
	if err := client(shut).OpenURL(context.Background(), shutdownSim, "https://example.com"); apperr.CodeOf(err) != apperr.InvalidState {
		t.Errorf("a shut-down simulator: %v", err)
	}
	fake := runtest.Table(map[string]run.Result{})
	if err := client(fake).OpenURL(context.Background(), iphone, "https://example.com"); apperr.CodeOf(err) != apperr.Unsupported || len(fake.Calls()) != 0 {
		t.Errorf("an iPhone: %v, calls %v", err, fake.Calls())
	}
}

func TestScreenshotOfASimulatorAsksForAPNG(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{"simctl io": runtest.Out(simctlScreenshotNote)})
	if err := client(fake).Screenshot(context.Background(), bootedSim, "/tmp/x.png"); err != nil {
		t.Fatal(err)
	}
	if !fake.Called("xcrun simctl io " + udidBooted + " screenshot --type=png /tmp/x.png") {
		t.Errorf("ran %v", fake.Calls())
	}
	// An iPad that is not a simulator or a phone is still refused.
	none := runtest.Table(map[string]run.Result{})
	android := device.Device{ID: "X", Name: "X", Platform: device.Android, Kind: device.Emulator}
	if err := client(none).Screenshot(context.Background(), android, "/tmp/x.png"); apperr.CodeOf(err) != apperr.Unsupported || len(none.Calls()) != 0 {
		t.Errorf("an Android device: %v, calls %v", err, none.Calls())
	}
}

func TestScreenshotAndRecordingOfAnIPhoneUseDevicectlAndNeedTheRightFileType(t *testing.T) {
	ctx := context.Background()
	shot := runtest.Table(map[string]run.Result{"devicectl device capture screenshot": {}})
	if err := client(shot).Screenshot(ctx, iphone, "/tmp/x.png"); err != nil ||
		!shot.Called("xcrun devicectl device capture screenshot --device P1 --destination /tmp/x.png --quiet") {
		t.Errorf("screenshot: %v, calls %v", err, shot.Calls())
	}
	// devicectl only writes .png and .mp4, so anything else is refused before the phone is asked.
	none := runtest.Table(map[string]run.Result{})
	if err := client(none).Screenshot(ctx, iphone, "/tmp/x.jpg"); apperr.CodeOf(err) != apperr.InvalidState || len(none.Calls()) != 0 {
		t.Errorf("a .jpg: %v, calls %v", err, none.Calls())
	}
	if err := client(none).Record(ctx, iphone, "/tmp/x.mov", 0); apperr.CodeOf(err) != apperr.InvalidState || len(none.Calls()) != 0 {
		t.Errorf("a .mov: %v, calls %v", err, none.Calls())
	}
	// A locked phone says what to do.
	locked := runtest.Table(map[string]run.Result{"devicectl device capture screenshot": runtest.Fail(1, "The device is locked.")})
	if err := client(locked).Screenshot(ctx, iphone, "/tmp/x.png"); err == nil || !hasHint(err, "Unlock it") {
		t.Errorf("a locked iPhone: %v", err)
	}
	// An iPhone whose iOS lacks the capability, as the real iPhone 17 Pro on iOS 27.0 answered for
	// screen recording, is unsupported, with the way around it.
	unsupported := runtest.Table(map[string]run.Result{"devicectl device capture screen-record": runtest.Fail(1, "ERROR: The capability “Screen Recording” is not supported by this device. (com.apple.dt.CoreDeviceError error 1001 (0x3E9))")})
	if err := client(unsupported).Record(ctx, iphone, filepath.Join(t.TempDir(), "x.mp4"), 0); apperr.CodeOf(err) != apperr.Unsupported || !hasHint(err, "QuickTime") {
		t.Errorf("an iPhone without screen recording: %v", err)
	}
	video := filepath.Join(t.TempDir(), "x.mp4")
	film := &runtest.Fake{Answer: func(command string, _ []string, _ run.Options) run.Result {
		if err := os.WriteFile(video, []byte("mp4"), 0o600); err != nil {
			t.Error(err)
		}
		return run.Result{}
	}}
	if err := client(film).Record(ctx, iphone, video, 0); err != nil ||
		!film.Called("xcrun devicectl device capture screen-record --device P1 --destination "+video+" --quiet") {
		t.Errorf("record: %v, calls %v", err, film.Calls())
	}
}

func TestAppearanceOfASimulatorIsReadAndSet(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{"simctl ui " + udidBooted + " appearance dark": {}, "simctl ui " + udidBooted + " appearance": runtest.Out("light\n")})
	c := client(fake)
	if mode, err := c.Appearance(context.Background(), bootedSim); err != nil || mode != "light" {
		t.Errorf("mode = %q, error = %v", mode, err)
	}
	if err := c.SetAppearance(context.Background(), bootedSim, "dark"); err != nil || !fake.Called("xcrun simctl ui "+udidBooted+" appearance dark") {
		t.Errorf("set: %v, calls %v", err, fake.Calls())
	}
}

func TestAppearanceRefusesAnIPhoneAndNoticesAnAnswerItDoesNotKnow(t *testing.T) {
	none := runtest.Table(map[string]run.Result{})
	if _, err := client(none).Appearance(context.Background(), iphone); apperr.CodeOf(err) != apperr.Unsupported || len(none.Calls()) != 0 {
		t.Errorf("an iPhone: %v, calls %v", err, none.Calls())
	}
	odd := runtest.Table(map[string]run.Result{"simctl ui": runtest.Out("unsupported\n")})
	if _, err := client(odd).Appearance(context.Background(), bootedSim); apperr.CodeOf(err) != apperr.ParseFailed {
		t.Errorf("an unknown answer: %v", err)
	}
}

func TestLocationOfASimulatorIsSetAndCleared(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{"simctl location": {}})
	c := client(fake)
	if err := c.SetLocation(context.Background(), bootedSim, 37.7749, -122.4194); err != nil || !fake.Called("xcrun simctl location "+udidBooted+" set 37.7749,-122.4194") {
		t.Errorf("set: %v, calls %v", err, fake.Calls())
	}
	if err := c.ClearLocation(context.Background(), bootedSim); err != nil || !fake.Called("xcrun simctl location "+udidBooted+" clear") {
		t.Errorf("clear: %v, calls %v", err, fake.Calls())
	}
	none := runtest.Table(map[string]run.Result{})
	if err := client(none).SetLocation(context.Background(), iphone, 1, 2); apperr.CodeOf(err) != apperr.Unsupported || len(none.Calls()) != 0 {
		t.Errorf("an iPhone: %v, calls %v", err, none.Calls())
	}
}

func TestStatusBarOfASimulatorIsTidiedAndRestored(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{"simctl status_bar": {}})
	c := client(fake)
	if err := c.CleanStatusBar(context.Background(), bootedSim); err != nil {
		t.Fatal(err)
	}
	if got := fake.Calls()[0]; !strings.Contains(got, "status_bar "+udidBooted+" override --time 9:41 --batteryState charged --batteryLevel 100") {
		t.Errorf("ran %q", got)
	}
	if err := c.ResetStatusBar(context.Background(), bootedSim); err != nil || !fake.Called("xcrun simctl status_bar "+udidBooted+" clear") {
		t.Errorf("reset: %v, calls %v", err, fake.Calls())
	}
	shut := runtest.Table(map[string]run.Result{"status_bar": runtest.Fail(149, "Unable to lookup in current state: Shutdown")})
	if err := client(shut).CleanStatusBar(context.Background(), shutdownSim); apperr.CodeOf(err) != apperr.InvalidState {
		t.Errorf("a shut-down simulator: %v", err)
	}
}

func TestPermissionIsGrantedOrRevokedOnlyForAnInstalledApp(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{
		"simctl get_app_container " + udidBooted + " com.example.app": runtest.Out("/path/App.app\n"),
		"simctl privacy": {},
	})
	c := client(fake)
	ctx := context.Background()
	if err := c.SetPermission(ctx, bootedSim, "com.example.app", "photos", true); err != nil || !fake.Called("xcrun simctl privacy "+udidBooted+" grant photos com.example.app") {
		t.Errorf("grant: %v, calls %v", err, fake.Calls())
	}
	if err := c.SetPermission(ctx, bootedSim, "com.example.app", "location", false); err != nil || !fake.Called("xcrun simctl privacy "+udidBooted+" revoke location com.example.app") {
		t.Errorf("revoke: %v, calls %v", err, fake.Calls())
	}

	// simctl says success for an app that is not there, so nothing may be sent for one.
	missing := runtest.Table(map[string]run.Result{"simctl get_app_container": runtest.Fail(2, simctlNoContainer), "simctl privacy": {}})
	err := client(missing).SetPermission(ctx, bootedSim, "com.example.nope", "photos", true)
	if apperr.CodeOf(err) != apperr.NotFound || missing.Called("simctl privacy") {
		t.Errorf("an app that is not installed: %v, calls %v", err, missing.Calls())
	}
}

func TestPermissionRefusesWhatSimctlHasNoServiceForAndAnIPhone(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{})
	ctx := context.Background()
	for _, service := range []string{"camera", "bogus", "all", "photos; reboot", ""} {
		if err := client(fake).SetPermission(ctx, bootedSim, "com.example.app", service, true); apperr.CodeOf(err) != apperr.InvalidState {
			t.Errorf("%q: %v, want invalid_state", service, err)
		}
	}
	if err := client(fake).SetPermission(ctx, iphone, "com.example.app", "photos", true); apperr.CodeOf(err) != apperr.Unsupported {
		t.Errorf("an iPhone: %v", err)
	}
	if calls := fake.Calls(); len(calls) != 0 {
		t.Errorf("a tool ran: %v", calls)
	}
}

func TestInstallGoesToASimulatorWithSimctlAndToAnIPhoneWithDevicectl(t *testing.T) {
	fake := &runtest.Fake{Answer: func(command string, argv []string, _ run.Options) run.Result {
		if strings.HasPrefix(command, "xcrun devicectl device install app --device P1 /b/Runner.app --json-output ") {
			if err := os.WriteFile(argv[len(argv)-1], []byte(`{"info": {"outcome": "success"}}`), 0o600); err != nil {
				t.Error(err)
			}
		}
		return run.Result{}
	}}
	ctx := context.Background()
	if err := client(fake).Install(ctx, bootedSim, "/b/App.app"); err != nil || !fake.Called("xcrun simctl install "+udidBooted+" /b/App.app") {
		t.Errorf("a simulator: %v, calls %v", err, fake.Calls())
	}
	if err := client(fake).Install(ctx, iphone, "/b/Runner.app"); err != nil || !fake.Called("xcrun devicectl device install app --device P1 /b/Runner.app") {
		t.Errorf("an iPhone: %v, calls %v", err, fake.Calls())
	}
	if err := client(fake).Install(ctx, pixel, "/b/app.apk"); apperr.CodeOf(err) != apperr.Unsupported {
		t.Errorf("an Android device: %v", err)
	}
	failed := runtest.Table(map[string]run.Result{"devicectl device install": runtest.Fail(1, "The application could not be verified")})
	if err := client(failed).Install(ctx, iphone, "/b/Runner.app"); err == nil || !strings.Contains(err.Error(), "could not be verified") {
		t.Errorf("a refusal by the phone: %v", err)
	}
}

func TestPhoneAppProcessIsFoundByItsFolder(t *testing.T) {
	apps := `{"result":{"apps":[
	  {"bundleIdentifier":"com.example.test","url":"file:///private/var/containers/Bundle/Application/AAA/Example%20-%20Test.app/"},
	  {"bundleIdentifier":"com.example.other","url":"file:///private/var/containers/Bundle/Application/BBB/Other.app/"}]}}`
	procs := `{"result":{"runningProcesses":[
	  {"executable":"file:///private/var/containers/Bundle/Application/AAA/Example - Test.app/PlugIns/Chat.appex/Chat","processIdentifier":1},
	  {"executable":"file:///private/var/containers/Bundle/Application/AAA/Example - Test.app/Example - Test","processIdentifier":2}]}}`
	folder, err := ParseAppFolder(apps, "com.example.test")
	if err != nil {
		t.Fatal(err)
	}
	if name, ok := ParseRunningExecutable(procs, folder); !ok || name != "Example - Test" {
		t.Errorf("name = %q, %v; want the app, not its extension", name, ok)
	}
	other, _ := ParseAppFolder(apps, "com.example.other")
	if name, ok := ParseRunningExecutable(procs, other); ok {
		t.Errorf("found %q for an app that is not running", name)
	}
	if _, err := ParseAppFolder(apps, "com.missing"); apperr.CodeOf(err) != apperr.NotFound {
		t.Errorf("error = %v, want NotFound", err)
	}
}

func TestPhoneAppThatIsNotRunningSaysSo(t *testing.T) {
	fake := &runtest.Fake{Answer: func(command string, argv []string, _ run.Options) run.Result {
		out := `{"result":{"apps":[{"bundleIdentifier":"com.example.app","url":"file:///x/App.app/"}]}}`
		if strings.Contains(command, "info processes") {
			out = `{"result":{"runningProcesses":[]}}`
		}
		if err := os.WriteFile(argv[len(argv)-1], []byte(out), 0o600); err != nil {
			t.Error(err)
		}
		return run.Result{}
	}}
	_, err := client(fake).AppExecutable(context.Background(), iphone, "com.example.app")
	if apperr.CodeOf(err) != apperr.InvalidState || !strings.Contains(err.Error(), "not running") {
		t.Errorf("error = %v", err)
	}
}

// unavailableSimctl answers `simctl list devices --json` with two unavailable simulators until
// `simctl delete unavailable` runs, and with none after it, unless deleteFails.
func unavailableSimctl(deleteFails bool) (*runtest.Fake, *bool) {
	deleted := false
	fake := &runtest.Fake{Answer: func(command string, _ []string, _ run.Options) run.Result {
		switch {
		case command == "xcrun simctl delete unavailable":
			if deleteFails {
				return run.Result{Code: 1, Stderr: "boom"}
			}
			deleted = true
			return run.Result{}
		case strings.HasPrefix(command, "xcrun simctl list devices"):
			if deleted {
				return run.Result{Stdout: encode(object{"devices": object{runtimePrefix + "iOS-18-2": []object{sim(udidBooted, "iPhone 16", "Booted")}}})}
			}
			return run.Result{Stdout: sizedFixture()}
		}
		return run.Result{Missing: true}
	}}
	return fake, &deleted
}

func TestUnavailableIsWhatTheLatestListingSaw(t *testing.T) {
	fake, _ := unavailableSimctl(false)
	c := client(fake)
	if got := c.Unavailable(); len(got) != 0 {
		t.Errorf("before any listing: %+v", got)
	}
	if _, err := c.ListSimulators(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := c.Unavailable(); len(got) != 3 {
		t.Errorf("after a listing: %+v", got)
	}
	// Asking does not run anything.
	calls := len(fake.Calls())
	c.Unavailable()
	if len(fake.Calls()) != calls {
		t.Error("Unavailable ran a command")
	}
}

func TestDeleteUnavailableRunsOneCallAndReportsWhatWentWithItsSize(t *testing.T) {
	fake, _ := unavailableSimctl(false)
	c := client(fake)
	deleted, err := c.DeleteUnavailable(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var freed int64
	for _, u := range deleted {
		freed += u.DataBytes
	}
	if len(deleted) != 3 || freed != 18_337_797 {
		t.Errorf("deleted %+v", deleted)
	}
	count := 0
	for _, call := range fake.Calls() {
		if call == "xcrun simctl delete unavailable" {
			count++
		}
		if strings.Contains(call, "simctl delete ") && call != "xcrun simctl delete unavailable" {
			t.Errorf("a simulator was deleted by id: %s", call)
		}
	}
	if count != 1 {
		t.Errorf("delete unavailable ran %d times", count)
	}
	if got := c.Unavailable(); len(got) != 0 {
		t.Errorf("still listed: %+v", got)
	}
}

func TestDeleteUnavailableWithNothingToDeleteRunsNothing(t *testing.T) {
	fake := &runtest.Fake{Answer: func(command string, _ []string, _ run.Options) run.Result {
		return run.Result{Stdout: encode(object{"devices": object{runtimePrefix + "iOS-18-2": []object{sim(udidBooted, "iPhone 16", "Booted")}}})}
	}}
	deleted, err := client(fake).DeleteUnavailable(context.Background(), nil)
	if err != nil || len(deleted) != 0 || fake.Called("delete unavailable") {
		t.Errorf("deleted %v, err %v, calls %v", deleted, err, fake.Calls())
	}
}

func TestDeleteUnavailableSaysWhenSimctlFails(t *testing.T) {
	fake, _ := unavailableSimctl(true)
	deleted, err := client(fake).DeleteUnavailable(context.Background(), nil)
	if err == nil || len(deleted) != 0 || !strings.Contains(err.Error(), "boom") {
		t.Errorf("deleted %v, err %v", deleted, err)
	}
}

// scripted answers `simctl list devices --json` with the listings in order, one per call (the last
// one repeats), and `simctl delete unavailable` with deleteResult.
func scripted(listings []string, deleteResult run.Result) *runtest.Fake {
	var mu sync.Mutex
	next := 0
	return &runtest.Fake{Answer: func(command string, _ []string, _ run.Options) run.Result {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case command == "xcrun simctl delete unavailable":
			return deleteResult
		case strings.HasPrefix(command, "xcrun simctl list devices"):
			out := listings[min(next, len(listings)-1)]
			next++
			if out == "" {
				return run.Result{Code: 1, Stderr: "simctl is down"}
			}
			return run.Result{Stdout: out}
		}
		return run.Result{Missing: true}
	}}
}

func listingOf(entries ...object) string {
	return encode(object{"devices": object{runtimePrefix + "iOS-27-0": entries}})
}

func orphan(id, name string, bytes int64) object {
	return withSize(sim(id, name, "Shutdown"), bytes, "runtime profile not found")
}

func TestDeleteUnavailableDeletesNothingWhenThereAreMoreThanWereShown(t *testing.T) {
	fake := scripted([]string{listingOf(orphan("A", "Shown", 10), orphan("B", "New One", 20))}, run.Result{})
	deleted, err := client(fake).DeleteUnavailable(context.Background(), []string{"A"})
	if apperr.CodeOf(err) != apperr.InvalidState || !strings.Contains(err.Error(), "New One") || len(deleted) != 0 {
		t.Errorf("deleted %v, err %v", deleted, err)
	}
	if fake.Called("delete unavailable") {
		t.Error("simctl deleted them anyway")
	}
	// Fewer than were shown is fine: what is deleted is only what was shown or less.
	fake = scripted([]string{listingOf(orphan("A", "Shown", 10)), listingOf()}, run.Result{})
	deleted, err = client(fake).DeleteUnavailable(context.Background(), []string{"A", "Gone Already"})
	if err != nil || len(deleted) != 1 || deleted[0].ID != "A" {
		t.Errorf("deleted %v, err %v", deleted, err)
	}
}

func TestOnlyASimulatorThatIsNoLongerListedCountsAsDeleted(t *testing.T) {
	// B is still listed afterwards, now usable (its runtime came back), and C is still unavailable.
	after := listingOf(sim("B", "Back", "Shutdown"), orphan("C", "Stuck", 30))
	fake := scripted([]string{listingOf(orphan("A", "Gone", 10), orphan("B", "Back", 20), orphan("C", "Stuck", 30)), after}, run.Result{})
	deleted, err := client(fake).DeleteUnavailable(context.Background(), nil)
	if err != nil || len(deleted) != 1 || deleted[0].ID != "A" {
		t.Errorf("deleted %+v, err %v", deleted, err)
	}
}

func TestDeleteUnavailableSaysSoWhenItCannotCheckAfterwards(t *testing.T) {
	// The second listing fails: nothing is claimed to be deleted.
	fake := scripted([]string{listingOf(orphan("A", "One", 10)), ""}, run.Result{})
	deleted, err := client(fake).DeleteUnavailable(context.Background(), nil)
	if err == nil || len(deleted) != 0 || !strings.Contains(err.Error(), "could not be read afterwards") {
		t.Errorf("deleted %+v, err %v", deleted, err)
	}
}

func TestDeleteUnavailableReportsWhatWentBeforeSimctlFailed(t *testing.T) {
	fake := scripted([]string{listingOf(orphan("A", "Went", 10), orphan("B", "Stayed", 20)), listingOf(orphan("B", "Stayed", 20))},
		run.Result{Code: 1, Stderr: "disk error"})
	deleted, err := client(fake).DeleteUnavailable(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "disk error") || len(deleted) != 1 || deleted[0].ID != "A" {
		t.Errorf("deleted %+v, err %v", deleted, err)
	}
}

// hasHint reports whether err carries a hint that contains text.
func hasHint(err error, text string) bool {
	var e *apperr.Error
	return errors.As(err, &e) && strings.Contains(e.Hint, text)
}

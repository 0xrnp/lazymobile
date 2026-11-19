package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/0xrnp/lazymobile/internal/android"
	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/ios"
	"github.com/0xrnp/lazymobile/internal/logs"
	"github.com/0xrnp/lazymobile/internal/mobile"
	"github.com/0xrnp/lazymobile/internal/state"
	"unicode/utf8"
)

var (
	iphone   = device.Device{ID: "SIM-1", Name: "iPhone 17", Platform: device.IOS, Kind: device.Simulator, State: device.Booted, OSVersion: "27.2", Source: "simctl"}
	ipad     = device.Device{ID: "SIM-2", Name: "iPad Air", Platform: device.IOS, Kind: device.Simulator, State: device.Shutdown, OSVersion: "27.2", Source: "simctl"}
	pixel    = device.Device{ID: "Pixel_9a", Name: "Pixel 9a", Platform: device.Android, Kind: device.Emulator, State: device.Booted, OSVersion: "16", Serial: "emulator-5554", Source: "emulator"}
	tablet   = device.Device{ID: "Tablet", Name: "Tablet", Platform: device.Android, Kind: device.Emulator, State: device.Shutdown, OSVersion: "15", Source: "emulator"}
	galaxy   = device.Device{ID: "R58M", Name: "Galaxy S23", Platform: device.Android, Kind: device.Physical, State: device.Unauthorized, Transport: "usb", Source: "adb", Detail: "Unlock the phone and accept the USB debugging prompt."}
	usbPhone = device.Device{ID: "UDID-1", Name: "Test iPhone", Platform: device.IOS, Kind: device.Physical, State: device.Connected, Transport: "usb", Source: "devicectl"}
	wifiPhon = device.Device{ID: "192.168.1.9:5555", Name: "Pixel 8", Platform: device.Android, Kind: device.Physical, State: device.Connected, Transport: "wifi", Serial: "192.168.1.9:5555", Source: "adb"}
)

// fakeSession is a log stream the test ends by hand.
type fakeSession struct {
	once    sync.Once
	stopped chan struct{}
	err     error
}

func newFakeSession() *fakeSession { return &fakeSession{stopped: make(chan struct{})} }

func (s *fakeSession) Stop() { s.once.Do(func() { close(s.stopped) }) }

func (s *fakeSession) Wait() error {
	<-s.stopped
	return s.err
}

func (s *fakeSession) isStopped() bool {
	select {
	case <-s.stopped:
		return true
	default:
		return false
	}
}

// fakeBackend records what the dashboard asks for.
type fakeBackend struct {
	// mode is the appearance every device reports.
	mode     string
	mu       sync.Mutex
	calls    []string
	devices  []device.Device
	listErrs []error
	// fail maps a call prefix, such as "boot", to the error it returns.
	fail     map[string]error
	noIOS    bool
	runtimes []ios.Runtime
	images   []android.SystemImage
	profiles []string
	specs    []logs.Spec
	handlers []logs.Handlers
	// installed are the apps InstalledApps reports, and info what AppInfo knows about an app that
	// is not among them, or that InstalledApps gives without a version, as Android does.
	installed []device.App
	reported  mobile.Report
	// unavailable are the simulators that cannot run, until they are deleted.
	unavailable []ios.Unavailable
	info        []device.App
	sessions    []*fakeSession
}

func (f *fakeBackend) record(format string, args ...any) error {
	call := fmt.Sprintf(format, args...)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
	for prefix, err := range f.fail {
		if strings.HasPrefix(call, prefix) {
			return err
		}
	}
	return nil
}

func (f *fakeBackend) called(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, call := range f.calls {
		if strings.HasPrefix(call, prefix) {
			n++
		}
	}
	return n
}

func (f *fakeBackend) HasIOS() bool { return !f.noIOS }
func (f *fakeBackend) List(context.Context) ([]device.Device, []error) {
	_ = f.record("list")
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]device.Device(nil), f.devices...), f.listErrs
}
func (f *fakeBackend) Boot(_ context.Context, d device.Device, o mobile.BootOptions) error {
	return f.record("boot %s cold=%v", d.ID, o.Cold)
}
func (f *fakeBackend) Shutdown(_ context.Context, d device.Device) error {
	return f.record("shutdown %s", d.ID)
}
func (f *fakeBackend) Erase(_ context.Context, d device.Device) error {
	return f.record("erase %s", d.ID)
}
func (f *fakeBackend) Delete(_ context.Context, d device.Device) error {
	return f.record("delete %s", d.ID)
}
func (f *fakeBackend) Runtimes(context.Context) ([]ios.Runtime, error) {
	return f.runtimes, f.record("runtimes")
}
func (f *fakeBackend) CreateSimulator(_ context.Context, name, deviceType, runtime string) error {
	return f.record("create-simulator %s %s %s", name, deviceType, runtime)
}
func (f *fakeBackend) SystemImages() []android.SystemImage { return f.images }
func (f *fakeBackend) DeviceProfiles(context.Context) ([]string, error) {
	return f.profiles, f.record("profiles")
}
func (f *fakeBackend) CreateAVD(_ context.Context, name, image, profile string) error {
	return f.record("create-avd %s %s %q", name, image, profile)
}
func (f *fakeBackend) Pair(_ context.Context, address, code string) error {
	return f.record("pair %s %s", address, code)
}
func (f *fakeBackend) Connect(_ context.Context, address string) error {
	return f.record("connect %s", address)
}
func (f *fakeBackend) Disconnect(_ context.Context, d device.Device) error {
	return f.record("disconnect %s", d.ID)
}
func (f *fakeBackend) Install(_ context.Context, d device.Device, path string) error {
	return f.record("install %s %s", d.ID, path)
}
func (f *fakeBackend) Uninstall(_ context.Context, d device.Device, id string) error {
	return f.record("uninstall %s %s", d.ID, id)
}
func (f *fakeBackend) Launch(_ context.Context, d device.Device, id string, o mobile.LaunchOptions) error {
	return f.record("launch %s %s restart=%v", d.ID, id, o.Restart)
}
func (f *fakeBackend) Stop(_ context.Context, d device.Device, id string) error {
	return f.record("stop %s %s", d.ID, id)
}
func (f *fakeBackend) Appearance(_ context.Context, d device.Device) (string, error) {
	if err := f.record("appearance %s", d.ID); err != nil {
		return "", err
	}
	return f.mode, nil
}
func (f *fakeBackend) SetAppearance(_ context.Context, d device.Device, mode string) error {
	return f.record("set-appearance %s %s", d.ID, mode)
}
func (f *fakeBackend) SetLocation(_ context.Context, d device.Device, lat, lon float64) error {
	return f.record("location %s %v,%v", d.ID, lat, lon)
}
func (f *fakeBackend) ClearLocation(_ context.Context, d device.Device) error {
	return f.record("clear-location %s", d.ID)
}
func (f *fakeBackend) SetStatusBar(_ context.Context, d device.Device, clean bool) error {
	return f.record("statusbar %s clean=%v", d.ID, clean)
}
func (f *fakeBackend) SetPermission(_ context.Context, d device.Device, id, name string, grant bool) error {
	return f.record("permission %s %s %s grant=%v", d.ID, id, name, grant)
}
func (f *fakeBackend) ClearData(_ context.Context, d device.Device, id string) error {
	return f.record("cleardata %s %s", d.ID, id)
}
func (f *fakeBackend) OpenLink(_ context.Context, d device.Device, link string) error {
	return f.record("link %s %s", d.ID, link)
}
func (f *fakeBackend) Dev(_ context.Context, d device.Device, action string, port int) error {
	return f.record("dev %s %s %d", d.ID, action, port)
}
func (f *fakeBackend) Crash(_ context.Context, d device.Device, id string) (device.CrashReport, error) {
	if err := f.record("crash %s %s", d.ID, id); err != nil {
		return device.CrashReport{}, err
	}
	return device.CrashReport{App: id, Text: "FATAL EXCEPTION: main\nProcess: " + id, Path: "/reports/" + id + ".ips"}, nil
}
func (f *fakeBackend) Screenshot(_ context.Context, d device.Device, path string) (string, error) {
	return path, f.record("screenshot %s %s", d.ID, path)
}

// Record films until ctx ends, as the real one does, and then saves the video.
func (f *fakeBackend) Record(ctx context.Context, d device.Device, path string, _ time.Duration, _ func()) (string, error) {
	if err := f.record("record start %s %s", d.ID, path); err != nil {
		return "", err
	}
	<-ctx.Done()
	return path, f.record("record stop %s %s", d.ID, path)
}
func (f *fakeBackend) CopyText(_ context.Context, text string) error {
	return f.record("copytext %s", strings.ReplaceAll(text, "\n", "|"))
}
func (f *fakeBackend) CopyFile(_ context.Context, path string) error {
	return f.record("copyfile %s", path)
}
func (f *fakeBackend) UnavailableSimulators() []ios.Unavailable {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ios.Unavailable(nil), f.unavailable...)
}
func (f *fakeBackend) DeleteUnavailable(_ context.Context, confirmed []string) ([]ios.Unavailable, error) {
	if err := f.record("delete unavailable %s", strings.Join(confirmed, ",")); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	deleted := f.unavailable
	f.unavailable = nil
	return deleted, nil
}
func (f *fakeBackend) Report(_ context.Context, d device.Device, id string, opts mobile.ReportOptions) (mobile.Report, error) {
	if err := f.record("report %s %s %s %s", d.ID, id, opts.Dir, opts.Version); err != nil {
		return mobile.Report{}, err
	}
	return f.reported, nil
}
func (f *fakeBackend) InstalledApps(_ context.Context, d device.Device) ([]device.App, error) {
	if err := f.record("apps %s", d.ID); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.installed, nil
}
func (f *fakeBackend) AppInfo(_ context.Context, d device.Device, id string) (device.App, error) {
	if err := f.record("appinfo %s %s", d.ID, id); err != nil {
		return device.App{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, app := range append(append([]device.App(nil), f.info...), f.installed...) {
		if app.ID == id {
			return app, nil
		}
	}
	return device.App{}, apperr.New(apperr.NotFound, "%s is not installed", id)
}
func (f *fakeBackend) OpenURL(_ context.Context, url string) error { return f.record("open %s", url) }
func (f *fakeBackend) StartLogs(_ context.Context, spec logs.Spec, h logs.Handlers) mobile.LogSession {
	_ = f.record("logs %s mode=%s app=%s", spec.Device.ID, spec.Mode, spec.App)
	session := newFakeSession()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.specs, f.handlers, f.sessions = append(f.specs, spec), append(f.handlers, h), append(f.sessions, session)
	return session
}

// harness drives a model the way the program would.
type harness struct {
	t       *testing.T
	m       *Model
	backend *fakeBackend
	clock   time.Time
}

func start(t *testing.T, devices ...device.Device) *harness {
	t.Helper()
	return startWith(t, Memory{}, devices...)
}

// startWith starts a dashboard that remembers through memory, as it does when run for real.
func startWith(t *testing.T, memory Memory, devices ...device.Device) *harness {
	t.Helper()
	return startWithApps(t, memory, nil, devices...)
}

// startWithApps is startWith, with apps installed on every device from the start.
func startWithApps(t *testing.T, memory Memory, apps []device.App, devices ...device.Device) *harness {
	t.Helper()
	backend := &fakeBackend{devices: devices, installed: apps, fail: map[string]error{}}
	h := &harness{t: t, backend: backend, clock: time.Unix(1_800_000_000, 0)}
	h.m = New(context.Background(), backend, nil)
	h.m.memory = memory
	h.m.now = func() time.Time { return h.clock }
	// Messages sent from outside the loop are applied at once.
	h.m.send = func(msg tea.Msg) { h.m.Update(msg) }
	h.m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	h.run(h.m.refresh())
	return h
}

// run executes a command and feeds its result back, as the program does. A command that blocks
// (a timer, or waiting for a log stream to end) is left behind.
func (h *harness) run(cmd tea.Cmd) {
	h.t.Helper()
	if cmd == nil {
		return
	}
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-result:
	case <-time.After(40 * time.Millisecond):
		return
	}
	switch msg := msg.(type) {
	case nil, tickMsg, tea.QuitMsg:
	case tea.BatchMsg:
		for _, c := range msg {
			h.run(c)
		}
	default:
		_, next := h.m.Update(msg)
		h.run(next)
	}
}

var special = map[string]tea.KeyPressMsg{
	"enter":     {Code: tea.KeyEnter},
	"esc":       {Code: tea.KeyEscape},
	"tab":       {Code: tea.KeyTab},
	"shift+tab": {Code: tea.KeyTab, Mod: tea.ModShift},
	"backspace": {Code: tea.KeyBackspace},
	"up":        {Code: tea.KeyUp},
	"down":      {Code: tea.KeyDown},
	"ctrl+c":    {Code: 'c', Mod: tea.ModCtrl},
	"ctrl+u":    {Code: 'u', Mod: tea.ModCtrl},
	"ctrl+p":    {Code: 'p', Mod: tea.ModCtrl},
}

// press sends keys: named ones such as "enter", or single characters.
func (h *harness) press(keys ...string) {
	h.t.Helper()
	for _, key := range keys {
		msg, named := special[key]
		if !named {
			r := []rune(key)
			if len(r) != 1 {
				h.t.Fatalf("unknown key %q", key)
			}
			msg = tea.KeyPressMsg{Code: r[0], Text: key}
		}
		_, cmd := h.m.Update(msg)
		h.run(cmd)
	}
}

// typeText types each character of text.
func (h *harness) typeText(text string) {
	h.t.Helper()
	for _, r := range text {
		h.press(string(r))
	}
}

func (h *harness) screen() string {
	return ansi.Strip(h.m.render())
}

// window is the open modal by itself, without the dashboard behind it.
func (h *harness) window() string {
	h.t.Helper()
	if h.m.modal == nil {
		h.t.Fatal("no window is open")
	}
	return ansi.Strip(strings.Join(h.m.modal.render(h.m.width, h.m.layout().bodyHeight), "\n"))
}

// flowed is the screen as running text: borders and line breaks become single spaces, so a
// sentence that was wrapped can still be found.
func flowed(screen string) string {
	return strings.Join(strings.FieldsFunc(screen, func(r rune) bool {
		return r == ' ' || r == '\n' || r == '│'
	}), " ")
}

func (h *harness) wantScreen(parts ...string) {
	h.t.Helper()
	screen := h.screen()
	for _, part := range parts {
		if !strings.Contains(screen, part) && !strings.Contains(flowed(screen), part) {
			h.t.Errorf("the screen should show %q:\n%s", part, screen)
		}
	}
}

func (h *harness) wantNoScreen(parts ...string) {
	h.t.Helper()
	screen := h.screen()
	for _, part := range parts {
		if strings.Contains(screen, part) || strings.Contains(flowed(screen), part) {
			h.t.Errorf("the screen should not show %q:\n%s", part, screen)
		}
	}
}

func (h *harness) wantCalls(prefix string, n int) {
	h.t.Helper()
	if got := h.backend.called(prefix); got != n {
		h.t.Errorf("%q was called %d times, want %d. Calls: %v", prefix, got, n, h.backend.calls)
	}
}

// selectDevice moves the cursor to a device.
func (h *harness) selectDevice(d device.Device) {
	h.t.Helper()
	for i, candidate := range h.m.devices {
		if candidate.Key() == d.Key() {
			h.m.cursor = i
			return
		}
	}
	h.t.Fatalf("%s is not in the list", d.Name)
}

func TestScreenFillsTheTerminalExactly(t *testing.T) {
	h := start(t, iphone, ipad, pixel, tablet, galaxy)
	check := func(size [2]int) {
		t.Helper()
		lines := strings.Split(h.screen(), "\n")
		if len(lines) != size[1] {
			t.Errorf("%dx%d: %d lines", size[0], size[1], len(lines))
		}
		for i, line := range lines {
			if w := ansi.StringWidth(line); w != size[0] {
				t.Errorf("%dx%d: line %d is %d wide: %q", size[0], size[1], i, w, line)
			}
		}
	}
	for _, size := range [][2]int{{120, 30}, {100, 12}, {80, 24}, {44, 10}, {200, 60}} {
		h.m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		check(size)
		h.press("?")
		check(size)
		h.press("esc", "tab")
		check(size)
	}
}

func TestDevicesAreGroupedWithTheirState(t *testing.T) {
	h := start(t, pixel, galaxy, ipad, tablet, iphone)
	h.wantScreen("iOS simulators", "Android emulators", "Phones and tablets",
		"iPhone 17", "iOS 27.2", "booted", "Pixel 9a", "Android 16", "Galaxy S23", "unauthorized")
	screen := h.screen()
	order := []string{"iOS simulators", "iPhone 17", "iPad Air", "Android emulators", "Pixel 9a", "Tablet", "Phones and tablets", "Galaxy S23"}
	last := -1
	for _, part := range order {
		at := strings.Index(screen, part)
		if at < last {
			t.Errorf("%q is out of order:\n%s", part, screen)
		}
		last = at
	}
}

func TestSelectedDeviceShowsWhatIsWrongWithIt(t *testing.T) {
	h := start(t, iphone, galaxy)
	h.wantNoScreen("accept the USB debugging prompt")
	h.selectDevice(galaxy)
	h.wantScreen("accept the USB debugging prompt")
}

func TestBootRunsInTheBackgroundAndRefreshes(t *testing.T) {
	h := start(t, iphone, ipad)
	h.selectDevice(ipad)
	h.press("b")
	h.wantCalls("boot SIM-2 cold=false", 1)
	h.wantCalls("list", 2)
	h.wantScreen("Started iPad Air")
	if len(h.m.busy) != 0 {
		t.Errorf("busy = %v after the action finished", h.m.busy)
	}
}

func TestADeviceBeingChangedIsMarkedAndNotChangedTwice(t *testing.T) {
	h := start(t, ipad)
	_, cmd := h.m.Update(tea.KeyPressMsg{Code: 'b', Text: "b"})
	h.wantScreen("booting", "Booting iPad Air")
	if _, second := h.m.Update(tea.KeyPressMsg{Code: 's', Text: "s"}); second != nil {
		t.Error("a second action started while the first was running")
	}
	h.wantScreen("iPad Air is busy")
	h.run(cmd)
	h.wantCalls("boot", 1)
	h.wantCalls("shutdown", 0)
}

func TestAFailedActionStaysOnScreenWithItsAdvice(t *testing.T) {
	h := start(t, tablet)
	h.backend.fail["boot"] = apperr.New(apperr.CommandFailed, "The emulator stopped right after starting: PANIC: Broken AVD").
		WithHint("Full log: /tmp/emulator-Tablet.log")
	h.press("b")
	h.wantScreen("PANIC: Broken AVD", "Full log: /tmp/emulator-Tablet.log")
	h.press("enter")
	h.wantNoScreen("Full log")
	h.wantScreen("Tablet")
}

func TestEraseAndDeleteAskFirst(t *testing.T) {
	for _, tt := range []struct{ key, call, title string }{
		{"e", "erase", "Erase iPad Air?"},
		{"d", "delete", "Delete iPad Air?"},
	} {
		t.Run(tt.call, func(t *testing.T) {
			h := start(t, ipad)
			h.press(tt.key)
			h.wantScreen(tt.title)
			h.wantCalls(tt.call, 0)

			// Enter must not confirm something that cannot be undone.
			h.press("enter")
			h.wantCalls(tt.call, 0)
			h.press("n")
			h.wantCalls(tt.call, 0)
			h.wantNoScreen(tt.title)

			h.press(tt.key, "y")
			h.wantCalls(tt.call+" SIM-2", 1)
		})
	}
}

func TestRealDevicesAreNeverBootedErasedOrDeleted(t *testing.T) {
	h := start(t, galaxy)
	h.press("b", "s", "e", "d", "B")
	if h.m.modal != nil {
		t.Error("a confirmation opened for a real device")
	}
	for _, call := range []string{"boot", "shutdown", "erase", "delete"} {
		h.wantCalls(call, 0)
	}
	h.press("e")
	h.wantScreen("real device")
}

func TestColdBootIsForEmulators(t *testing.T) {
	h := start(t, tablet, ipad)
	h.selectDevice(tablet)
	h.press("B")
	h.wantCalls("boot Tablet cold=true", 1)
	h.selectDevice(ipad)
	h.press("B")
	h.wantCalls("boot SIM-2", 0)
}

func TestSelectionFollowsTheDeviceWhenTheListChanges(t *testing.T) {
	h := start(t, ipad, tablet)
	h.selectDevice(tablet)
	h.backend.devices = []device.Device{iphone, ipad, pixel, tablet}
	h.press("r")
	if d, _ := h.m.selected(); d.ID != "Tablet" {
		t.Errorf("selected %s after the refresh, want Tablet", d.Name)
	}
	h.backend.devices = []device.Device{iphone}
	h.press("r")
	if d, ok := h.m.selected(); !ok || d.ID != "SIM-1" {
		t.Errorf("selected %+v after its device went away", d)
	}
}

func TestToolProblemsAreShownWithTheirFix(t *testing.T) {
	h := start(t, iphone)
	h.backend.listErrs = []error{apperr.New(apperr.ToolMissing, "adb was not found").WithHint(`Install it with: sdkmanager "platform-tools"`)}
	h.press("r")
	h.wantScreen("iPhone 17", "adb was not found", `sdkmanager "platform-tools"`)
}

func TestEmptyListSaysWhatToDo(t *testing.T) {
	h := start(t)
	h.wantScreen("No devices yet", "Press n to create")
}

func TestTooSmallATerminal(t *testing.T) {
	h := start(t, iphone)
	h.m.Update(tea.WindowSizeMsg{Width: 30, Height: 8})
	h.wantScreen("needs at least")
}

func TestNarrowTerminalShowsOnePaneAtATime(t *testing.T) {
	h := start(t, iphone)
	h.m.Update(tea.WindowSizeMsg{Width: 70, Height: 20})
	h.wantScreen("Devices", "iPhone 17")
	h.wantNoScreen("Select a running device")
	h.press("tab")
	h.wantScreen("Select a running device")
	h.wantNoScreen("iPhone 17")
}

func TestPlusAndMinusMoveTheLineBetweenThePanes(t *testing.T) {
	h := start(t, iphone)
	before := h.m.layout().devicesWidth
	h.press("+", "+")
	if got := h.m.layout().devicesWidth; got != before+2*resizeStep {
		t.Fatalf("devices width after + +: got %d, want %d", got, before+2*resizeStep)
	}
	h.press("-", "-", "-")
	if got := h.m.layout().devicesWidth; got != before-resizeStep {
		t.Fatalf("devices width after - - -: got %d, want %d", got, before-resizeStep)
	}

	// Past the limit the logs keep their minimum width, and one - moves the line back at once.
	for range 40 {
		h.press("+")
	}
	l := h.m.layout()
	if l.logsWidth != minPaneWidth || l.devicesWidth+l.logsWidth != 120 {
		t.Fatalf("widest devices pane: %+v", l)
	}
	h.press("-")
	if got := h.m.layout().logsWidth; got != minPaneWidth+resizeStep {
		t.Fatalf("logs width after one -: got %d, want %d", got, minPaneWidth+resizeStep)
	}
	for _, line := range strings.Split(h.screen(), "\n") {
		if w := ansi.StringWidth(line); w != 120 {
			t.Fatalf("a line is %d columns wide, want 120: %q", w, line)
		}
	}
}

func TestZoomShowsOnlyTheFocusedPane(t *testing.T) {
	h := start(t, iphone)
	h.press("z")
	h.wantScreen("iPhone 17")
	h.wantNoScreen("Select a running device")
	h.press("tab")
	h.wantScreen("Select a running device")
	h.wantNoScreen("iPhone 17")
	h.press("z")
	h.wantScreen("iPhone 17", "Select a running device")

	h.press("z", "+")
	h.wantScreen("Press z to show both")
}

// tick advances the clock and delivers the dashboard's one-second tick.
func (h *harness) tick(after time.Duration) {
	h.t.Helper()
	h.clock = h.clock.Add(after)
	_, cmd := h.m.Update(tickMsg{})
	h.run(cmd)
}

// setDevices changes what the next listing reports.
func (h *harness) setDevices(devices ...device.Device) {
	h.backend.mu.Lock()
	defer h.backend.mu.Unlock()
	h.backend.devices = devices
}

func TestRefreshIsQuickOnlyWhileSomethingIsChanging(t *testing.T) {
	h := start(t, ipad)
	h.tick(2 * time.Second)
	h.wantCalls("list", 1)
	h.tick(time.Second)
	h.wantCalls("list", 2)

	booting := ipad
	booting.State = device.Booting
	h.setDevices(booting)
	h.press("r")
	h.wantCalls("list", 3)
	h.tick(time.Second)
	h.wantCalls("list", 4)
}

// A device stopped or plugged in outside the dashboard shows up within the idle interval.
func TestChangesMadeElsewhereShowUpWithinSeconds(t *testing.T) {
	h := start(t, pixel)
	stopped := pixel
	stopped.State, stopped.Serial = device.Shutdown, ""
	h.setDevices(stopped, wifiPhon)
	h.tick(refreshIdle)
	h.wantScreen("Pixel 8")
	h.wantNoScreen("booted")
}

// adb emu kill returns while the emulator is still listed. The row must keep saying so, and the
// list must keep refreshing quickly, until the emulator is gone.
func TestShutdownIsFollowedUntilTheDeviceStops(t *testing.T) {
	h := start(t, pixel)
	h.press("s")
	h.wantCalls("shutdown Pixel_9a", 1)
	h.wantScreen("shutting down")

	h.tick(refreshBusy)
	h.wantCalls("list", 3)
	h.wantScreen("shutting down")

	stopped := pixel
	stopped.State, stopped.Serial = device.Shutdown, ""
	h.setDevices(stopped)
	h.tick(refreshBusy)
	h.wantNoScreen("booted")
	if doing := h.m.doing(pixel.Key()); doing != "" {
		t.Errorf("the emulator is still marked %q", doing)
	}
	if len(h.m.settling) != 0 {
		t.Errorf("settling = %v after the emulator stopped", h.m.settling)
	}
	// Back to the idle interval.
	h.tick(refreshBusy)
	h.wantCalls("list", 4)
}

func TestBootIsFollowedUntilTheDeviceIsReady(t *testing.T) {
	h := start(t, tablet)
	h.press("b")
	h.wantScreen("booting")
	h.tick(refreshBusy)
	h.wantScreen("booting")

	running := tablet
	running.State, running.Serial = device.Booted, "emulator-5556"
	h.setDevices(running)
	h.tick(refreshBusy)
	h.wantScreen("booted")
	if len(h.m.settling) != 0 {
		t.Errorf("settling = %v after the emulator booted", h.m.settling)
	}
}

func TestDeleteIsFollowedUntilTheDeviceIsGone(t *testing.T) {
	h := start(t, tablet, ipad)
	h.selectDevice(tablet)
	h.press("d", "y")
	h.wantCalls("delete Tablet", 1)
	h.wantScreen("deleting")
	h.setDevices(ipad)
	h.tick(refreshBusy)
	if len(h.m.devices) != 1 || len(h.m.settling) != 0 {
		t.Errorf("settling = %v after the emulator was deleted", h.m.settling)
	}
}

// An emulator that crashes while booting never becomes ready. It must not be locked until the
// time limit: it can be booted again, or shut down, at once.
func TestASettlingDeviceCanBeActedOnAgain(t *testing.T) {
	h := start(t, tablet)
	h.press("b")
	h.wantCalls("boot Tablet", 1)
	h.press("b")
	h.wantCalls("boot Tablet", 2)
	h.press("s")
	h.wantCalls("shutdown Tablet", 1)
}

// A simulator that shut down is a success even when DeviceHub could not be quit after it.
func TestAShutdownThatLeftTheSimulatorAppOpenIsAWarning(t *testing.T) {
	h := start(t, iphone)
	h.backend.fail["shutdown"] = &ios.AppNotQuitError{Err: apperr.New(apperr.CommandFailed, "The simulator shut down, but DeviceHub could not be quit: osascript failed")}
	h.press("s")
	if h.m.modal != nil {
		t.Errorf("a failure window opened: %s", h.window())
	}
	h.wantScreen("DeviceHub could not be quit", "shutting down")
}

// A device that never reaches the state is not watched forever.
func TestFollowingAnActionGivesUp(t *testing.T) {
	h := start(t, tablet)
	h.press("b")
	h.tick(settleTimeout)
	if len(h.m.settling) != 0 {
		t.Errorf("settling = %v after the time limit", h.m.settling)
	}
	h.wantNoScreen("booting")
	h.press("b")
	h.wantCalls("boot Tablet", 2)
}

// A failed action has nothing to wait for.
func TestAFailedActionIsNotFollowed(t *testing.T) {
	h := start(t, pixel)
	h.backend.fail["shutdown"] = errors.New("adb: device offline")
	h.press("s")
	if len(h.m.settling) != 0 {
		t.Errorf("settling = %v after a failure", h.m.settling)
	}
}

func TestCopyID(t *testing.T) {
	h := start(t, pixel)
	_, cmd := h.m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if cmd == nil {
		t.Fatal("nothing was copied")
	}
	h.wantScreen("Copied emulator-5554")
}

// Logs ---------------------------------------------------------------------------------------

func (h *harness) openLogs(d device.Device, app string) {
	h.t.Helper()
	h.selectDevice(d)
	h.press("l")
	h.typeText(app)
	h.press("enter")
}

func (h *harness) emit(lines ...logs.Line) {
	h.t.Helper()
	handlers := h.backend.handlers
	handlers[len(handlers)-1].Lines(lines)
}

func TestLongLogLinesCanBeWrapped(t *testing.T) {
	h := start(t, pixel)
	h.openLogs(pixel, "")
	h.emit(logs.Line{Text: "start " + strings.Repeat("x", 100) + " end-of-line", Level: logs.Info})
	h.wantNoScreen("end-of-line")
	h.wantScreen("long lines cut: slide or h l to scroll")

	h.press("w")
	h.wantScreen("end-of-line")
	h.wantNoScreen("long lines cut")
	for _, line := range strings.Split(h.screen(), "\n") {
		if w := ansi.StringWidth(line); w != 120 {
			t.Fatalf("a line is %d columns wide, want 120: %q", w, line)
		}
	}

	// While following, the newest line stays in view however many rows the lines above take.
	for i := range 40 {
		h.emit(logs.Line{Text: fmt.Sprintf("%d %s", i, strings.Repeat("y", 100)), Level: logs.Info})
	}
	h.emit(logs.Line{Text: "newest", Level: logs.Info})
	h.wantScreen("newest")
}

func TestASelectedLogLineOpensInFullWithTheLinesThatBelongToIt(t *testing.T) {
	h := start(t, pixel)
	h.openLogs(pixel, "")
	h.emit(
		logs.Line{Text: "E crash: something broke " + strings.Repeat("z", 90) + " tail-of-message", Level: logs.Error},
		logs.Line{Text: "    at com.example.Main.run(Main.java:12)"},
		logs.Line{Text: "I after", Level: logs.Info},
	)
	// k moves the selection up from the newest line, to the stack trace line.
	h.press("k")
	if h.m.logPanes[0].follow {
		t.Fatal("the log still follows after k")
	}
	h.press("enter")
	window := h.window()
	for _, want := range []string{"crash: something broke", "tail-of-message", "Main.java:12"} {
		if !strings.Contains(window, want) {
			t.Errorf("the window should show %q:\n%s", want, window)
		}
	}
	if strings.Contains(window, "I after") {
		t.Errorf("the next entry was included:\n%s", window)
	}
	h.press("esc")

	_, cmd := h.m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if cmd == nil {
		t.Fatal("y gave nothing to copy")
	}
	if copied := fmt.Sprint(cmd()); !strings.Contains(copied, "crash: something broke") || !strings.Contains(copied, "Main.java:12") {
		t.Errorf("copied %q", copied)
	}
	h.wantScreen("Copied the selected log line")

	// The selection stays in view when the pane gets shorter, and wrapping is kept for a new stream.
	h.press("g")
	for i := range 30 {
		h.emit(logs.Line{Text: fmt.Sprintf("filler %02d", i), Level: logs.Info})
	}
	// Ten lines down from the first: the crash, its trace and "I after" come before filler 00.
	h.press("j", "j", "j", "j", "j", "j", "j", "j", "j", "j")
	h.m.Update(tea.WindowSizeMsg{Width: 120, Height: 10})
	h.wantScreen("filler 07")
	h.m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	h.press("w", "r")
	if !h.m.logPanes[0].wrap {
		t.Error("wrapping was turned off by restarting the stream")
	}
	h.press("w")

	// Back at the newest line, following starts again.
	h.press("j", "j")
	if !h.m.logPanes[0].follow {
		t.Error("the log does not follow after reaching the newest line")
	}
}

func TestAnIOSSystemLogStartsAtInfoUntilALevelIsChosen(t *testing.T) {
	h := start(t, iphone, pixel)
	h.selectDevice(iphone)
	h.press("l", "enter")
	h.typeText("com.example.app")
	h.press("enter")
	h.wantCalls("logs SIM-1 mode=system app=com.example.app", 1)
	if h.m.logPanes[0].level != logs.Info {
		t.Fatalf("level = %v, want info", h.m.logPanes[0].level)
	}
	h.emit(logs.Line{Text: "Db framework noise", Level: logs.Debug}, logs.Line{Text: "Df app message", Level: logs.Info})
	h.wantNoScreen("framework noise")
	h.wantScreen("app message", "1 hidden")

	// Android starts with everything. A log opens beside the one that is open, so that one is
	// closed first.
	h.press("X")
	h.openLogs(pixel, "")
	if h.m.logPanes[0].level != logs.NoLevel {
		t.Fatalf("logcat level = %v, want none", h.m.logPanes[0].level)
	}

	// A level chosen by hand is kept for the next stream.
	h.press("L")
	h.press("X")
	h.openLogs(pixel, "")
	if h.m.logPanes[0].level != logs.Verbose {
		t.Fatalf("level after L = %v, want verbose", h.m.logPanes[0].level)
	}
}

func TestTheSelectedDeviceIsDescribedUnderTheList(t *testing.T) {
	h := start(t, pixel, iphone)
	h.selectDevice(pixel)
	h.wantScreen("emulator-5554  (y copies)", "AVD    Pixel_9a", "Android emulator")
	h.selectDevice(iphone)
	h.wantScreen("SIM-1  (y copies)", "iOS simulator")
	h.wantNoScreen("AVD    ")
}

func TestSlidingSidewaysScrollsTheLogUpToItsWidestLine(t *testing.T) {
	h := start(t, pixel)
	h.openLogs(pixel, "")
	h.emit(logs.Line{Text: "begin " + strings.Repeat("x", 100) + " finish", Level: logs.Info})
	l := h.m.layout()
	x := l.devicesWidth + 5
	slide := func(button tea.MouseButton, mod tea.KeyMod, times int) {
		for range times {
			h.m.Update(tea.MouseWheelMsg{X: x, Y: 5, Button: button, Mod: mod})
		}
	}
	slide(tea.MouseWheelRight, 0, 100)
	widest := len("begin ") + 100 + len(" finish")
	if want := widest - (l.logsWidth - 2); h.m.logPanes[0].left != want {
		t.Fatalf("left = %d after sliding to the end, want %d", h.m.logPanes[0].left, want)
	}
	h.wantScreen("finish")
	h.wantNoScreen("begin")

	// Shift with the normal wheel slides too, for terminals without sideways events.
	slide(tea.MouseWheelUp, tea.ModShift, 100)
	if h.m.logPanes[0].left != 0 {
		t.Fatalf("left = %d after sliding back", h.m.logPanes[0].left)
	}
	h.wantScreen("begin")

	h.press("w")
	slide(tea.MouseWheelRight, 0, 3)
	if h.m.logPanes[0].left != 0 {
		t.Errorf("a wrapped log slid sideways to %d", h.m.logPanes[0].left)
	}
}

func TestTheMouseWheelScrollsThePaneUnderIt(t *testing.T) {
	h := start(t, pixel, iphone)
	h.openLogs(pixel, "")
	for i := range 100 {
		h.emit(logs.Line{Text: fmt.Sprintf("line %d", i), Level: logs.Info})
	}
	logsX := h.m.layout().devicesWidth + 5
	h.m.Update(tea.MouseWheelMsg{X: logsX, Y: 5, Button: tea.MouseWheelUp})
	if h.m.logPanes[0].follow {
		t.Fatal("the log still follows after scrolling up")
	}
	h.wantScreen("scrolled")

	// Over the devices it moves the selection, and the focus stays on the logs.
	cursor := h.m.cursor
	_, cmd := h.m.Update(tea.MouseWheelMsg{X: 5, Y: 3, Button: tea.MouseWheelUp})
	h.run(cmd)
	if h.m.cursor != cursor-1 || h.m.focus != paneLogs {
		t.Fatalf("cursor %d → %d, focus %v", cursor, h.m.cursor, h.m.focus)
	}

	h.m.Update(tea.MouseClickMsg{X: 5, Y: 3, Button: tea.MouseLeft})
	if h.m.focus != paneDevices {
		t.Fatalf("a click on the devices left the focus on %v", h.m.focus)
	}
}

func TestLogsNeedARunningDevice(t *testing.T) {
	h := start(t, ipad)
	h.press("l")
	h.wantScreen("iPad Air is not running")
	h.wantCalls("logs", 0)
}

func TestLogsOfAnAndroidApp(t *testing.T) {
	h := start(t, pixel)
	h.press("l")
	h.wantScreen("App on Pixel 9a", "Leave empty for every app")
	h.typeText("com.example.app")
	h.press("enter")
	h.wantCalls("logs Pixel_9a mode=logcat app=com.example.app", 1)

	h.emit(
		logs.Line{Text: "11-13 20:49:44.298   100   100 I App: hello", Level: logs.Info},
		logs.Line{Text: "11-13 20:49:45.100   300   300 E App: it broke", Level: logs.Error},
	)
	h.wantScreen("Logs: Pixel 9a · com.example.app", "App: hello", "App: it broke", "2 shown")
	if h.m.focus != paneLogs {
		t.Error("opening a log should move to the logs pane")
	}
}

func TestLogsOfEverythingWithAnEmptyAppID(t *testing.T) {
	h := start(t, pixel)
	h.openLogs(pixel, "")
	h.wantCalls("logs Pixel_9a mode=logcat app=", 1)
}

func TestAnInvalidAppIDIsRefusedInThePrompt(t *testing.T) {
	h := start(t, pixel)
	h.press("l")
	h.typeText("com.example; reboot")
	h.press("enter")
	h.wantScreen("does not look like an app id")
	h.wantCalls("logs", 0)

	h.press("ctrl+u")
	h.typeText("com.example.ap")
	h.press("backspace")
	h.typeText("pp")
	h.press("enter")
	h.wantCalls("logs Pixel_9a mode=logcat app=com.example.app", 1)
}

func TestTheAppIDIsRememberedForTheNextTime(t *testing.T) {
	h := start(t, pixel)
	h.openLogs(pixel, "com.example.app")
	h.press("tab", "l")
	h.wantScreen("> com.example.app")
}

func TestSimulatorOffersItsTwoLogs(t *testing.T) {
	h := start(t, iphone)
	h.press("l")
	h.wantScreen("Which log of iPhone 17?", "System log", "Launch an app")
	h.press("down", "enter")
	h.press("enter")
	h.wantScreen("An app id is needed")
	h.typeText("com.example.app")
	h.press("enter")
	h.wantCalls("logs SIM-1 mode=console app=com.example.app", 1)
}

func TestLogFilters(t *testing.T) {
	h := start(t, pixel)
	h.openLogs(pixel, "")
	h.emit(
		logs.Line{Text: "D debug line", Level: logs.Debug},
		logs.Line{Text: "E network failed", Level: logs.Error},
		logs.Line{Text: "    at com.example.Net.call(Net.java:10)"},
		logs.Line{Text: "I network ok", Level: logs.Info},
		logs.Line{Text: "    a line after an info line"},
	)

	// L steps the minimum level up. At "error" the trace under the error stays with it.
	for h.m.logPanes[0].level != logs.Error {
		h.press("L")
	}
	h.wantScreen("E network failed", "at com.example.Net.call", "error+")
	h.wantNoScreen("D debug line", "I network ok", "a line after an info line")

	for h.m.logPanes[0].level != logs.NoLevel {
		h.press("L")
	}
	h.press("/")
	h.typeText("NETWORK")
	h.press("enter")
	h.wantScreen("E network failed", "I network ok", `"NETWORK"`, "2 shown, 3 hidden")
	h.wantNoScreen("D debug line")

	h.press("/", "ctrl+u", "enter")
	h.wantScreen("D debug line", "5 shown")
}

func TestPauseKeepsWhatArrivesMeanwhile(t *testing.T) {
	h := start(t, pixel)
	h.openLogs(pixel, "")
	h.emit(logs.Line{Text: "before the pause"})
	h.press("p")
	h.emit(logs.Line{Text: "during the pause"})
	h.wantScreen("before the pause", "paused")
	h.wantNoScreen("during the pause")
	h.press("p")
	h.wantScreen("before the pause", "during the pause")
}

func TestClearAndScroll(t *testing.T) {
	h := start(t, pixel)
	h.openLogs(pixel, "")
	var lines []logs.Line
	for i := 1; i <= 100; i++ {
		lines = append(lines, logs.Line{Text: fmt.Sprintf("line-%03d", i)})
	}
	h.emit(lines...)
	h.wantScreen("line-100")
	h.wantNoScreen("line-001")

	h.press("g")
	h.wantScreen("line-001", "scrolled")
	h.wantNoScreen("line-100")
	h.emit(logs.Line{Text: "line-101"})
	h.wantScreen("line-001")
	h.wantNoScreen("line-101")

	h.press("G")
	h.wantScreen("line-101")

	h.press("c")
	h.wantNoScreen("line-101")
	h.wantScreen("Waiting for log lines")
}

// Device output must never reach the terminal as control sequences.
func TestLogLinesCannotDriveTheTerminal(t *testing.T) {
	h := start(t, pixel)
	h.openLogs(pixel, "")
	h.emit(logs.Line{Text: "safe\x1b[2J\x1b]0;owned\x07 text\ttabbed\x00\r"})
	raw := h.m.render()
	for _, bad := range []string{"\x1b[2J", "\x1b]0;", "\x07", "\x00", "\r", "\t"} {
		if strings.Contains(raw, bad) {
			t.Errorf("the screen contains %q", bad)
		}
	}
	h.wantScreen("safe text    tabbed")
}

func TestOnlyTheNewestLinesAreKept(t *testing.T) {
	h := start(t, pixel)
	h.openLogs(pixel, "")
	batch := make([]logs.Line, 1000)
	for i := 0; i < 25; i++ {
		for j := range batch {
			batch[j] = logs.Line{Text: fmt.Sprintf("line-%d", i*1000+j)}
		}
		h.emit(batch...)
	}
	if got := len(h.m.logPanes[0].all); got != maxLogLines {
		t.Errorf("kept %d lines, want %d", got, maxLogLines)
	}
	if first := h.m.logPanes[0].shown[0].Text; first != "line-5000" {
		t.Errorf("the oldest line kept is %s, want line-5000", first)
	}
	h.wantScreen("line-24999")
}

func TestANewStreamReplacesTheOldOne(t *testing.T) {
	h := start(t, pixel, iphone)
	h.openLogs(pixel, "")
	first := h.backend.handlers[0]
	h.emit(logs.Line{Text: "from the pixel"})

	h.press("X")
	h.selectDevice(iphone)
	h.press("l", "enter", "enter")
	if !h.backend.sessions[0].isStopped() {
		t.Error("the first stream was left running")
	}
	h.wantNoScreen("from the pixel")

	// Lines the old stream still had in flight must not appear in the new log.
	first.Lines([]logs.Line{{Text: "late line from the pixel"}})
	h.wantNoScreen("late line from the pixel")
	h.wantScreen("Logs: iPhone 17")
}

func TestAStreamThatFailsSaysWhy(t *testing.T) {
	h := start(t, pixel)
	h.openLogs(pixel, "")
	h.emit(logs.Line{Text: "the last line"})
	h.m.Update(logEndMsg{id: h.m.logPanes[0].id, err: apperr.New(apperr.CommandFailed, "adb failed: device offline")})
	h.wantScreen("the last line", "adb failed: device offline", "ended")

	h.press("r")
	h.wantCalls("logs Pixel_9a", 2)
	h.wantNoScreen("device offline")
}

func TestSaveLog(t *testing.T) {
	h := start(t, pixel)
	h.openLogs(pixel, "")
	h.emit(logs.Line{Text: "D hidden", Level: logs.Debug}, logs.Line{Text: "E kept", Level: logs.Error})
	for h.m.logPanes[0].level != logs.Error {
		h.press("L")
	}
	path := filepath.Join(t.TempDir(), "saved.log")
	h.press("S")
	h.wantScreen("lazymobile-Pixel-9a-")
	h.press("ctrl+u")
	h.typeText(path)
	h.press("enter")

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "E kept\n" {
		t.Errorf("saved %q, want only the shown line", content)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %v, want only the user to read it", info.Mode().Perm())
	}
	h.wantScreen("Saved 1 lines")
}

func TestQuitStopsTheLogStreamFirst(t *testing.T) {
	h := start(t, pixel)
	h.openLogs(pixel, "")
	_, cmd := h.m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if cmd == nil {
		t.Fatal("q did nothing")
	}
	if _, isQuit := cmd().(tea.QuitMsg); !isQuit {
		t.Error("q did not quit")
	}
	if !h.backend.sessions[0].isStopped() {
		t.Error("the log stream was left running")
	}
}

// Creating and connecting ----------------------------------------------------------------------

func TestCreateASimulator(t *testing.T) {
	h := start(t)
	h.backend.runtimes = []ios.Runtime{
		{ID: "runtime-27", Name: "iOS 27.2", DeviceTypes: []ios.Choice{{ID: "type-17", Name: "iPhone 17"}, {ID: "type-air", Name: "iPad Air"}}},
		{ID: "runtime-26", Name: "iOS 26.0", DeviceTypes: []ios.Choice{{ID: "type-16", Name: "iPhone 16"}}},
	}
	h.press("n")
	h.wantScreen("New device", "iOS simulator", "Android emulator")
	h.press("enter")
	h.wantScreen("iOS version", "iOS 27.2", "iOS 26.0")
	h.press("enter")
	h.wantScreen("Device for iOS 27.2", "iPhone 17", "iPad Air")
	h.press("down", "enter")
	h.wantScreen("Name of the new simulator", "> iPad Air")
	h.typeText(" 2")
	h.press("enter")
	h.wantCalls("create-simulator iPad Air 2 type-air runtime-27", 1)
	h.wantScreen("Created iPad Air 2")
	h.wantCalls("list", 2)
}

func TestCreateAnEmulator(t *testing.T) {
	h := start(t)
	h.backend.noIOS = true
	h.backend.images = []android.SystemImage{{ID: "system-images;android-36;google_apis;arm64-v8a", API: "36", Label: "Android 16 (API 36)"}}
	h.backend.profiles = []string{"pixel_9", "medium_phone"}
	h.press("n")
	h.wantScreen("Android version", "Android 16 (API 36)")
	h.press("enter")
	h.wantScreen("Hardware profile", "Default hardware", "pixel_9")
	h.press("down", "enter")
	h.wantScreen("> pixel_9_API_36")
	h.typeText(" x")
	h.press("enter")
	h.wantScreen("Use letters, digits")
	h.wantCalls("create-avd", 0)
	h.press("backspace", "backspace", "enter")
	h.wantCalls(`create-avd pixel_9_API_36 system-images;android-36;google_apis;arm64-v8a "pixel_9"`, 1)
}

func TestCreateWithoutASystemImageSaysHowToGetOne(t *testing.T) {
	h := start(t)
	h.backend.noIOS = true
	h.press("n")
	h.wantScreen("No Android system image is installed", "SDK Manager")
}

func TestPairThenConnect(t *testing.T) {
	h := start(t)
	h.press("w", "enter")
	h.typeText("192.168.1.5")
	h.press("enter")
	h.wantScreen("Type an address and port")
	h.typeText(":37123")
	h.press("enter")
	h.typeText("12345")
	h.press("enter")
	h.wantScreen("The code is six digits")
	h.typeText("6")
	h.press("enter")
	h.wantCalls("pair 192.168.1.5:37123 123456", 1)

	// After pairing it goes straight on to connecting.
	h.wantScreen("Connect address")
	h.typeText("192.168.1.5:40001")
	h.press("enter")
	h.wantCalls("connect 192.168.1.5:40001", 1)
	h.wantScreen("Connected to 192.168.1.5:40001")
}

func TestDisconnectIsForWifiPhones(t *testing.T) {
	h := start(t, wifiPhon, galaxy)
	h.selectDevice(wifiPhon)
	h.press("x")
	h.wantCalls("disconnect 192.168.1.9:5555", 1)
	h.selectDevice(galaxy)
	h.press("x")
	h.wantCalls("disconnect R58M", 0)
}

func TestPasteGoesIntoAPrompt(t *testing.T) {
	h := start(t, pixel)
	h.press("l")
	h.m.Update(tea.PasteMsg{Content: "  com.example.app\nsecond line"})
	h.press("enter")
	h.wantCalls("logs Pixel_9a mode=logcat app=com.example.app", 1)
}

// A window opens over the dashboard, which stays in view behind it.
func TestTheDashboardStaysVisibleBehindAWindow(t *testing.T) {
	h := start(t, iphone, pixel)
	h.press("?")
	screen := h.screen()
	// The menu scrolls, so only what comes first is on screen. keys_test.go checks the sections.
	for _, want := range []string{"Keys", "Boot a simulator", "Devices"} {
		if !strings.Contains(screen, want) {
			t.Errorf("the screen should show %q:\n%s", want, screen)
		}
	}
	lines := strings.Split(screen, "\n")
	if len(lines) != 30 {
		t.Fatalf("the screen has %d lines, want 30", len(lines))
	}
	for i, line := range lines {
		if w := ansi.StringWidth(line); w != 120 {
			t.Errorf("line %d is %d columns wide, want 120: %q", i, w, line)
		}
	}
	// The window is drawn over the panes, so one row holds the device behind and the menu in front.
	both := false
	for _, line := range lines {
		both = both || strings.Contains(line, "Pixel 9a") && strings.Contains(line, "j k  Move")
	}
	if !both {
		t.Errorf("the devices are hidden behind the window:\n%s", screen)
	}
}

// Wide characters behind the window must not push a row past the edge of the terminal.
func TestAWindowOverWideCharactersFillsTheTerminalExactly(t *testing.T) {
	h := start(t, pixel)
	h.openLogs(pixel, "")
	h.emit(logs.Line{Text: strings.Repeat("日本", 60)}, logs.Line{Text: "x" + strings.Repeat("日本", 60)})
	h.press("tab", "tab")
	for _, size := range [][2]int{{44, 10}, {80, 12}, {100, 10}, {121, 24}, {160, 30}} {
		h.m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		h.press("?")
		for i, line := range strings.Split(h.screen(), "\n") {
			if w := ansi.StringWidth(line); w != size[0] {
				t.Errorf("%dx%d: line %d is %d columns wide", size[0], size[1], i, w)
			}
		}
		h.press("esc")
	}
}

func TestATallMessageScrolls(t *testing.T) {
	h := start(t, iphone)
	h.m.Update(tea.WindowSizeMsg{Width: 120, Height: 16})
	var body []string
	for i := 1; i <= 40; i++ {
		body = append(body, fmt.Sprintf("line %d", i))
	}
	h.m.message("Long", strings.Join(body, "\n"))
	h.wantScreen("j k: scroll", "0%")
	h.wantNoScreen("line 40")
	h.press("j")
	if h.m.modal == nil {
		t.Fatal("scrolling closed the window")
	}
	h.press("G")
	h.wantScreen("line 40", "100%")
	h.press("g")
	h.wantScreen("0%")
	h.press("x")
	if h.m.modal != nil {
		t.Error("a key that does not scroll should close the window")
	}
}

// A message that fits closes on any key, scroll keys included.
func TestAShortMessageClosesOnAnyKey(t *testing.T) {
	h := start(t, iphone)
	h.m.message("Done", "It worked.")
	h.wantScreen("any key: close")
	h.press("j")
	if h.m.modal != nil {
		t.Error("the message did not close")
	}
}

func TestListFailureOnAnOtherwiseEmptyMachine(t *testing.T) {
	h := start(t)
	h.backend.listErrs = []error{errors.New("xcrun was not found")}
	h.press("r")
	h.wantScreen("xcrun was not found")
	h.wantNoScreen("No devices yet")
}

// Remembering ----------------------------------------------------------------------------------

func memoryIn(t *testing.T) Memory {
	t.Helper()
	return Memory{Store: state.Store{Path: filepath.Join(t.TempDir(), "state.json")}, Folder: "/work/app"}
}

func TestAppsUsedBeforeAndInstalledOnesAreOffered(t *testing.T) {
	memory := memoryIn(t)
	if err := memory.Store.Update(func(f *state.File) { f.RememberApp(device.Android, "com.used.before") }); err != nil {
		t.Fatal(err)
	}
	h := startWith(t, memory, pixel)
	h.backend.installed = []device.App{{ID: "com.installed.one"}, {ID: "com.used.before"}}
	h.selectDevice(pixel)
	h.press("l")
	h.wantCalls("apps Pixel_9a", 1)
	// The list starts with the app used before, and tab adds the rest.
	h.wantScreen("(used before)", "● com.used.before", "tab: all 2 apps", "Every app", "Type an app id")
	h.wantNoScreen("com.installed.one")
	h.press("tab")
	window := h.window()
	for _, want := range []string{"● com.used.before", "  com.installed.one", "tab: used before", "Every app", "Type an app id"} {
		if !strings.Contains(window, want) {
			t.Errorf("the list should show %q:\n%s", want, window)
		}
	}
	if strings.Count(window, "com.used.before") != 1 {
		t.Errorf("an app is listed twice:\n%s", window)
	}

	// The second one is chosen, and becomes the newest app used.
	h.press("j", "enter")
	h.wantCalls("logs Pixel_9a mode=logcat app=com.installed.one", 1)
	if got := memory.Store.Load().Apps[device.Android]; !slices.Equal(got, []string{"com.installed.one", "com.used.before"}) {
		t.Errorf("remembered %v", got)
	}

	// "Every app" opens the log without an app.
	h.press("tab")
	h.press("l", "j", "j", "enter")
	h.wantCalls("logs Pixel_9a mode=logcat app=", 2)
}

func TestAnAppUsedOnAnotherDeviceStaysListedWhileItIsNotInstalledHere(t *testing.T) {
	memory := memoryIn(t)
	if err := memory.Store.Update(func(f *state.File) { f.RememberApp(device.Android, "com.new.build") }); err != nil {
		t.Fatal(err)
	}
	h := startWith(t, memory, pixel)
	h.backend.installed = []device.App{{ID: "com.other.app"}}
	h.selectDevice(pixel)
	h.press("l")
	h.wantScreen("● com.new.build", "not in the installed list")

	// It can still be chosen, for the log of an app that is about to be installed.
	h.press("enter")
	h.wantCalls("logs Pixel_9a mode=logcat app=com.new.build", 1)
}

func TestAppsUsedBeforeAreNotMarkedMissingWhenTheDeviceListFailed(t *testing.T) {
	memory := memoryIn(t)
	if err := memory.Store.Update(func(f *state.File) { f.RememberApp(device.Android, "com.new.build") }); err != nil {
		t.Fatal(err)
	}
	h := startWith(t, memory, pixel)
	h.backend.fail = map[string]error{"apps ": errors.New("adb: device offline")}
	h.selectDevice(pixel)
	h.press("l")
	h.wantScreen("com.new.build", "Every app")
	h.wantNoScreen("not in the installed list")
}

func TestTheAppListIsFilteredByTyping(t *testing.T) {
	memory := memoryIn(t)
	if err := memory.Store.Update(func(f *state.File) { f.RememberApp(device.Android, "com.used.before") }); err != nil {
		t.Fatal(err)
	}
	h := startWith(t, memory, pixel)
	h.backend.installed = []device.App{{ID: "com.used.before"}, {ID: "com.shop.app"}, {ID: "com.shop.beta"}, {ID: "org.other"}}
	h.selectDevice(pixel)
	h.press("l")
	h.press("tab")
	h.wantScreen("com.shop.app", "com.shop.beta", "org.other", "/: filter")

	// Typing narrows the list to what matches, and the fixed choices stay.
	h.press("/")
	h.typeText("shop")
	h.wantScreen("/ shop", "com.shop.app", "com.shop.beta", "Type an app id")
	h.wantNoScreen("org.other", "com.used.before")
	h.typeText("x")
	h.wantScreen("Nothing matches shopx", "Type an app id")

	// Enter chooses the first match, not the first app of the whole list.
	h.press("backspace", "backspace", "backspace", "backspace", "backspace")
	h.typeText("beta")
	h.wantScreen("com.shop.beta")
	h.press("enter")
	h.wantCalls("logs Pixel_9a mode=logcat app=com.shop.beta", 1)
}

func TestEscapeClearsTheAppFilterBeforeClosingTheList(t *testing.T) {
	h := start(t, pixel)
	h.backend.installed = []device.App{{ID: "com.shop.app"}, {ID: "org.other"}}
	h.selectDevice(pixel)
	h.press("l")
	h.wantCalls("apps Pixel_9a", 1)
	h.press("/")
	h.typeText("shop")
	h.wantNoScreen("org.other")
	h.press("esc")
	h.wantScreen("org.other")
	h.wantScreen("Which app on Pixel 9a?")
	h.press("esc")
	h.wantNoScreen("Which app on Pixel 9a?")
}

func TestAnIPhoneOffersItsInstalledAppsWithTheirVersion(t *testing.T) {
	h := start(t, usbPhone)
	h.backend.installed = []device.App{
		{ID: "com.example.app.staging", Name: "Example Staging", Version: "1.4.2", Build: "87"},
		{ID: "com.example.preview", Name: "Example Preview", Version: "2.0.0"},
	}
	h.selectDevice(usbPhone)
	h.press("l")
	h.wantScreen("Which log of Test iPhone?")

	// The first of its two logs launches an app, which is chosen from the ones on the phone.
	h.press("enter")
	h.wantCalls("apps UDID-1", 1)
	h.wantScreen("Which app on Test iPhone?", "com.example.app.staging  1.4.2 (87)  Example Staging", "com.example.preview", "2.0.0", "Example Preview")
	h.wantNoScreen("tab:")
	h.press("enter")
	h.wantCalls("logs UDID-1 mode=console app=com.example.app.staging", 1)
	// The version stays in the log's heading, to be read after the list is gone.
	h.wantScreen("Logs: Test iPhone · com.example.app.staging 1.4.2 (87)")
}

func TestRestartingALogAsksForTheInstalledVersionAgain(t *testing.T) {
	h := start(t, usbPhone)
	h.backend.installed = []device.App{{ID: "com.example.app.staging", Version: "1.4.2", Build: "87"}}
	h.selectDevice(usbPhone)
	h.press("l", "enter", "enter")
	h.wantScreen("com.example.app.staging 1.4.2 (87)")

	// A new build is installed while the log is open, and the log is restarted.
	h.backend.installed = []device.App{{ID: "com.example.app.staging", Version: "1.4.3", Build: "88"}}
	h.press("r")
	h.wantCalls("appinfo UDID-1 com.example.app.staging", 1)
	h.wantScreen("com.example.app.staging 1.4.3 (88)")
	h.wantNoScreen("1.4.2 (87)")

	// A phone that cannot answer now shows no version, not the old one.
	h.backend.fail["appinfo"] = errors.New("unreachable")
	h.press("r")
	h.wantScreen("Logs: Test iPhone · com.example.app.staging")
	h.wantNoScreen("1.4.3", "1.4.2")

	// And it is asked again at the next restart, once the phone answers.
	delete(h.backend.fail, "appinfo")
	h.press("r")
	h.wantScreen("com.example.app.staging 1.4.3 (88)")
}

func TestAVersionJustListedIsNotAskedForAgain(t *testing.T) {
	h := start(t, usbPhone)
	h.backend.installed = []device.App{{ID: "com.example.app.staging", Version: "1.4.2", Build: "87"}}
	h.selectDevice(usbPhone)
	h.press("l", "enter", "enter")
	h.wantScreen("com.example.app.staging 1.4.2 (87)")
	h.wantCalls("apps UDID-1", 1)
	h.wantCalls("appinfo", 0)
}

func TestAnAndroidLogShowsTheVersionTheListDoesNotGive(t *testing.T) {
	h := start(t, pixel)
	// The list of an Android device has package names only. The version comes from asking about the app.
	h.backend.installed = []device.App{{ID: "com.example.app"}}
	h.backend.info = []device.App{{ID: "com.example.app", Version: "0.0.47", Build: "37"}}
	h.selectDevice(pixel)
	h.press("l", "enter")
	h.wantCalls("logs Pixel_9a mode=logcat app=com.example.app", 1)
	h.wantCalls("appinfo Pixel_9a com.example.app", 1)
	h.wantScreen("Logs: Pixel 9a · com.example.app 0.0.47 (37)")

	// An app with no version name still shows its build.
	h.backend.info = []device.App{{ID: "com.example.app", Build: "38"}}
	h.press("r")
	h.wantScreen("Logs: Pixel 9a · com.example.app (38)")
}

func TestALogReopenedFromAPastRunAsksForTheVersion(t *testing.T) {
	apps := []device.App{{ID: "com.example.app.staging", Version: "1.4.2", Build: "87"}}
	memory := memoryIn(t)
	first := startWithApps(t, memory, apps, usbPhone)
	first.selectDevice(usbPhone)
	// The device system log of the first app.
	first.press("l", "down", "enter", "enter")
	first.wantCalls("logs UDID-1 mode=system app=com.example.app.staging", 1)
	first.press("q")

	h := startWithApps(t, memory, apps, usbPhone)
	h.wantCalls("logs UDID-1 mode=system app=com.example.app.staging", 1)
	// The system log of an iPhone starts at "info+", which takes room in the heading too.
	h.m.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	h.wantScreen("Logs: Test iPhone · com.example.app.staging 1.4.2 (87) · info+")
}

func TestATypedAppIDAsksForTheVersion(t *testing.T) {
	h := startWithApps(t, Memory{}, []device.App{{ID: "com.example.app.staging", Version: "1.4.2", Build: "87"}}, usbPhone)
	h.selectDevice(usbPhone)
	// "Type an app id…" is the last choice.
	h.press("l", "enter", "G", "enter")
	h.typeText("com.example.app.staging")
	h.press("enter")
	h.wantCalls("logs UDID-1 mode=console app=com.example.app.staging", 1)
	h.wantScreen("com.example.app.staging 1.4.2 (87)")
}

func TestAVersionThatArrivesForAnEarlierStartIsDropped(t *testing.T) {
	h := start(t, usbPhone)
	h.backend.installed = []device.App{{ID: "com.example.app.staging", Version: "1.4.2", Build: "87"}}
	h.selectDevice(usbPhone)
	h.press("l", "enter", "enter")

	// The log is restarted twice. The first answer comes last, and is for a stream that is gone.
	h.backend.installed = []device.App{{ID: "com.example.app.staging", Version: "1.4.3", Build: "88"}}
	_, first := h.m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	_, second := h.m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	h.run(second)
	h.backend.installed = []device.App{{ID: "com.example.app.staging", Version: "9.9.9", Build: "1"}}
	h.run(first)
	h.wantScreen("com.example.app.staging 1.4.3 (88)")
	h.wantNoScreen("9.9.9")
}

func TestTheLogHeadingKeepsTheVersionUntilNothingElseFits(t *testing.T) {
	v := logView{
		opened: true, ended: true, version: "1.4.2 (87)",
		spec: logs.Spec{Device: device.Device{Name: "Test iPhone"}, Mode: logs.Console, App: "com.example.app.staging"},
	}
	for _, c := range []struct {
		width int
		want  string
	}{
		{120, "Logs: Test iPhone · com.example.app.staging 1.4.2 (87) · console · ended"},
		// The word "console" goes first: the log's first line already says it.
		{70, "Logs: Test iPhone · com.example.app.staging 1.4.2 (87) · ended"},
		// Then the version, so "ended" is not what gets cut.
		{50, "Logs: Test iPhone · com.example.app.staging · ended"},
	} {
		if got := v.title(c.width, ""); got != c.want {
			t.Errorf("title(%d) = %q, want %q", c.width, got, c.want)
		}
	}
	if got := (&logView{}).title(80, ""); got != "Logs" {
		t.Errorf("a log that is not open: %q", got)
	}
}

func TestTypingAnAppIDStartsFromTheLastOne(t *testing.T) {
	memory := memoryIn(t)
	if err := memory.Store.Update(func(f *state.File) { f.RememberApp(device.Android, "com.used.before") }); err != nil {
		t.Fatal(err)
	}
	h := startWith(t, memory, pixel)
	h.selectDevice(pixel)
	h.press("l", "G", "enter")
	if h.m.modal == nil || h.m.modal.kind != modalPrompt || h.m.modal.value != "com.used.before" {
		t.Fatalf("modal = %+v", h.m.modal)
	}
}

func TestTheLogLeftOpenComesBackOnTheNextStart(t *testing.T) {
	memory := memoryIn(t)
	first := startWith(t, memory, pixel, iphone)
	first.openLogs(pixel, "com.example.app")
	first.press("w", "L")
	first.m.logPanes[0].filter = "needle"
	first.press("q")

	h := startWith(t, memory, pixel, iphone)
	h.wantCalls("logs Pixel_9a mode=logcat app=com.example.app", 1)
	h.wantScreen("Opened the log you left: Pixel 9a")
	if v := h.m.logPanes[0]; !v.wrap || v.filter != "needle" || v.level != logs.Verbose || !v.levelChosen {
		t.Errorf("log settings not restored: wrap %v filter %q level %v chosen %v", v.wrap, v.filter, v.level, v.levelChosen)
	}
	if d, _ := h.m.selected(); d.Key() != pixel.Key() {
		t.Errorf("selected %s", d.Name)
	}

	// Another folder has its own view.
	other := memory
	other.Folder = "/somewhere/else"
	h = startWith(t, other, pixel, iphone)
	h.wantCalls("logs", 0)
}

func TestALogThatLaunchesTheAppIsNotStartedAgainOnItsOwn(t *testing.T) {
	memory := memoryIn(t)
	err := memory.Store.Update(func(f *state.File) {
		f.Views = map[string]state.View{memory.Folder: {
			Device: iphone.Key(),
			Log:    &state.Log{Device: iphone.Key(), Mode: logs.Console, App: "com.example.app"},
		}}
	})
	if err != nil {
		t.Fatal(err)
	}
	h := startWith(t, memory, pixel, iphone)
	h.wantCalls("logs", 0)
	if d, _ := h.m.selected(); d.Key() != iphone.Key() {
		t.Errorf("selected %s", d.Name)
	}
}

func TestALogOfADeviceThatStoppedIsNotReopened(t *testing.T) {
	memory := memoryIn(t)
	first := startWith(t, memory, pixel)
	first.openLogs(pixel, "")
	first.press("q")

	stopped := pixel
	stopped.State = device.Shutdown
	h := startWith(t, memory, stopped)
	h.wantCalls("logs", 0)
}

// screenRow is the screen row that shows text.
func screenRow(t *testing.T, h *harness, text string) int {
	t.Helper()
	for y, line := range strings.Split(h.screen(), "\n") {
		if strings.Contains(line, text) {
			return y
		}
	}
	t.Fatalf("%q is not on the screen:\n%s", text, h.screen())
	return -1
}

func TestClickingADeviceSelectsIt(t *testing.T) {
	h := start(t, iphone, pixel)
	h.selectDevice(iphone)
	h.press("tab")
	h.m.Update(tea.MouseClickMsg{X: 5, Y: screenRow(t, h, "Pixel 9a"), Button: tea.MouseLeft})
	if d, _ := h.m.selected(); d.Key() != pixel.Key() || h.m.focus != paneDevices {
		t.Errorf("selected %s, focus %v", d.Name, h.m.focus)
	}
	// A heading is not a device: the selection stays.
	h.m.Update(tea.MouseClickMsg{X: 5, Y: screenRow(t, h, "Android emulators"), Button: tea.MouseLeft})
	if d, _ := h.m.selected(); d.Key() != pixel.Key() {
		t.Errorf("a click on a heading selected %s", d.Name)
	}
}

func TestFilterMatchesAreMarked(t *testing.T) {
	out := highlight("Needle in a stack of needles", "needle", lipgloss.NewStyle())
	if ansi.Strip(out) != "Needle in a stack of needles" {
		t.Errorf("the text changed: %q", ansi.Strip(out))
	}
	for _, want := range []string{match.Render("Needle"), match.Render("needle")} {
		if !strings.Contains(out, want) {
			t.Errorf("%q is not marked in %q", want, out)
		}
	}
	if got := highlight("nothing here", "needle", lipgloss.NewStyle()); strings.Contains(got, match.Render("needle")) {
		t.Errorf("marked text that is not there: %q", got)
	}
}

func TestAnAppListThatIsNoLongerWantedIsDropped(t *testing.T) {
	h := start(t, pixel, iphone)
	h.backend.installed = []device.App{{ID: "com.installed.one"}}
	h.selectDevice(pixel)
	_, slow := h.m.Update(tea.KeyPressMsg{Code: 'l', Text: "l"})
	// The selection moves on before the pixel's apps arrive.
	h.selectDevice(iphone)
	h.run(slow)
	if h.m.modal != nil {
		t.Fatalf("a list for the pixel opened while the iPhone is selected:\n%s", h.window())
	}

	// Asked twice for the same device, only the second answer opens the list.
	h.selectDevice(pixel)
	_, first := h.m.Update(tea.KeyPressMsg{Code: 'l', Text: "l"})
	_, second := h.m.Update(tea.KeyPressMsg{Code: 'l', Text: "l"})
	h.run(first)
	if h.m.modal != nil {
		t.Fatal("the first, older answer opened the list")
	}
	h.run(second)
	if h.m.modal == nil || !strings.Contains(h.window(), "Which app on Pixel 9a?") {
		t.Fatal("the latest answer did not open the list")
	}
}

func TestTheViewIsSavedWithoutQuitting(t *testing.T) {
	memory := memoryIn(t)
	first := startWith(t, memory, pixel)
	first.openLogs(pixel, "")
	first.press("w")
	first.tick(time.Second)
	// lazymobile ends here without a quit, as when Neovim closes the terminal.
	if view := memory.Store.Load().Views[memory.Folder]; view.Log == nil || !view.Wrap {
		t.Fatalf("saved %+v", view)
	}

	// A stream that ended is saved as closed, so it is not reopened.
	first.m.Update(logEndMsg{id: first.m.logPanes[0].id})
	first.tick(time.Second)
	if view := memory.Store.Load().Views[memory.Folder]; view.Log != nil {
		t.Errorf("an ended stream was saved as open: %+v", view.Log)
	}
	h := startWith(t, memory, pixel)
	h.wantCalls("logs", 0)
}

func TestSavingWaitsForTheSavedViewToBePutBack(t *testing.T) {
	memory := memoryIn(t)
	first := startWith(t, memory, pixel, iphone)
	first.openLogs(pixel, "com.example.app")
	first.press("q")

	// The first list misses the pixel because adb failed.
	h := &harness{t: t, backend: &fakeBackend{devices: []device.Device{iphone}, listErrs: []error{errors.New("adb failed")}, fail: map[string]error{}}, clock: time.Unix(1_800_000_000, 0)}
	h.m = New(context.Background(), h.backend, nil)
	h.m.memory = memory
	h.m.now = func() time.Time { return h.clock }
	h.m.send = func(msg tea.Msg) { h.m.Update(msg) }
	h.m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	h.run(h.m.refresh())
	h.tick(time.Second)
	if view := memory.Store.Load().Views[memory.Folder]; view.Log == nil {
		t.Fatalf("the saved view was replaced before it was put back: %+v", view)
	}

	// The next list has the pixel again, and its log comes back.
	h.backend.mu.Lock()
	h.backend.devices, h.backend.listErrs = []device.Device{pixel, iphone}, nil
	h.backend.mu.Unlock()
	h.run(h.m.refresh())
	h.wantCalls("logs Pixel_9a mode=logcat app=com.example.app", 1)
}

func TestMarkingKeepsCharactersWhole(t *testing.T) {
	// Lowercasing "Ⱥ" takes one byte more and "ẞ" one byte less, so the whole text keeps its
	// length while the positions after "Ⱥ" move.
	text := "Ⱥ needle ẞ"
	out := highlight(text, "needle", lipgloss.NewStyle())
	if ansi.Strip(out) != text || !utf8.ValidString(out) {
		t.Errorf("the text changed: %q", out)
	}
	if strings.Contains(out, match.Render("eedle ")) {
		t.Errorf("the wrong part is marked: %q", out)
	}
}

func TestUninstallListsTheAppsAndAsksBeforeRemovingOne(t *testing.T) {
	h := start(t, iphone)
	h.backend.installed = []device.App{
		{ID: "com.example.app", Name: "Example", Version: "1.4.2", Build: "87"},
		{ID: "com.example.other", Name: "Other"},
	}
	h.press("u")
	h.wantCalls("apps SIM-1", 1)
	h.wantScreen("Uninstall which app from iPhone 17?", "com.example.app    1.4.2 (87)  Example", "com.example.other", "Other")

	h.press("enter")
	h.wantScreen("Uninstall com.example.app?", "all its data from iPhone 17", "cannot be undone")
	h.wantCalls("uninstall", 0)

	// Enter must not confirm something that cannot be undone, and no leaves it alone.
	h.press("enter")
	h.wantCalls("uninstall", 0)
	h.press("n")
	h.wantNoScreen("Uninstall com.example.app?")
	h.wantCalls("uninstall", 0)

	h.press("u", "down", "enter", "y")
	h.wantCalls("uninstall SIM-1 com.example.other", 1)
	h.wantScreen("Uninstalled com.example.other from iPhone 17")
}

func TestUninstallFromARealPhoneSaysSo(t *testing.T) {
	h := start(t, usbPhone, wifiPhon)
	h.backend.installed = []device.App{{ID: "com.example.app"}}
	h.selectDevice(usbPhone)
	h.press("u", "enter")
	h.wantScreen("Uninstall com.example.app?", "Test iPhone is a real device.")
	h.press("y")
	h.wantCalls("uninstall UDID-1 com.example.app", 1)

	h.selectDevice(wifiPhon)
	h.press("u", "enter")
	h.wantScreen("Pixel 8 is a real device.")
}

func TestUninstallNeedsARunningDevice(t *testing.T) {
	h := start(t, ipad)
	h.press("u")
	h.wantScreen("iPad Air is not running")
	h.wantCalls("apps", 0)
	if h.m.modal != nil {
		t.Error("a window opened for a device that is not running")
	}
}

func TestUninstallSaysWhenThereIsNothingToRemoveOrTheListFails(t *testing.T) {
	h := start(t, iphone)
	h.press("u")
	h.wantScreen("No apps of yours are installed on iPhone 17")
	if h.m.modal != nil {
		t.Error("a window opened for an empty list")
	}

	h.backend.fail["apps"] = apperr.New(apperr.CommandFailed, "devicectl could not reach the phone")
	h.press("u")
	h.wantScreen("Could not list the apps on iPhone 17", "could not reach the phone")
	h.wantCalls("uninstall", 0)
}

func TestAFailedUninstallIsExplained(t *testing.T) {
	h := start(t, pixel)
	h.backend.installed = []device.App{{ID: "com.example.app"}}
	h.backend.fail["uninstall"] = apperr.New(apperr.CommandFailed, "Uninstall failed: Failure [DELETE_FAILED_DEVICE_POLICY_MANAGER]")
	h.press("u", "enter", "y")
	h.wantScreen("Could not finish: uninstalling com.example.app from Pixel 9a", "DELETE_FAILED_DEVICE_POLICY_MANAGER")
}

func TestAnAppIsLaunchedRestartedOrStoppedFromTheDevices(t *testing.T) {
	h := start(t, iphone)
	h.backend.installed = []device.App{
		{ID: "com.example.app", Name: "Example", Version: "1.4.2", Build: "87"},
		{ID: "com.example.other", Name: "Other"},
	}
	h.press("o")
	h.wantCalls("apps SIM-1", 1)
	h.wantScreen("Which app on iPhone 17?", "com.example.app", "1.4.2 (87)", "com.example.other")

	h.press("enter")
	h.wantScreen("com.example.app on iPhone 17", "Launch", "Restart", "Stop")
	h.wantCalls("launch", 0)
	h.press("enter")
	h.wantCalls("launch SIM-1 com.example.app restart=false", 1)
	h.wantScreen("Launched com.example.app on iPhone 17")

	h.press("o", "enter", "down", "enter")
	h.wantCalls("launch SIM-1 com.example.app restart=true", 1)
	h.wantScreen("Restarted com.example.app on iPhone 17")

	h.press("o", "down", "enter", "down", "down", "enter")
	h.wantCalls("stop SIM-1 com.example.other", 1)
	h.wantScreen("Stopped com.example.other on iPhone 17")
}

func TestAnIPhoneOffersNoStop(t *testing.T) {
	h := start(t, usbPhone)
	h.backend.installed = []device.App{{ID: "com.example.app"}}
	h.press("o", "enter")
	h.wantScreen("com.example.app on Test iPhone", "Launch", "Restart")
	h.wantNoScreen("Stop")
	h.press("down", "down", "enter")
	h.wantCalls("stop", 0)
}

func TestTheLatestCrashOfAnAppIsShownFromTheAppMenu(t *testing.T) {
	h := start(t, iphone)
	h.backend.installed = []device.App{{ID: "com.example.app"}}
	// Launch, Restart, Stop, Grant, Revoke, then the crash report.
	h.press("o", "enter", "down", "down", "down", "down", "down")
	h.wantScreen("Show its latest crash report")
	h.press("enter")
	h.wantCalls("crash SIM-1 com.example.app", 1)
	h.wantScreen("Latest crash of com.example.app on iPhone 17", "FATAL EXCEPTION: main", "Full report: /reports/com.example.app.ips")

	h.press("esc")
	h.backend.fail = map[string]error{"crash": apperr.New(apperr.NotFound, "No crash of com.example.app found for iPhone 17")}
	h.press("o", "enter", "down", "down", "down", "down", "down", "enter")
	h.wantScreen("No crash of com.example.app found for iPhone 17")
}

func TestAnIPhoneOffersNoCrashReport(t *testing.T) {
	h := start(t, usbPhone)
	h.backend.installed = []device.App{{ID: "com.example.app"}}
	h.press("o", "enter")
	h.wantNoScreen("crash report")
}

func TestAppActionsNeedARunningDeviceAndSayWhenTheyFail(t *testing.T) {
	h := start(t, ipad, pixel)
	h.press("o")
	h.wantScreen("iPad Air is not running")
	h.wantCalls("apps", 0)

	h.selectDevice(pixel)
	h.press("o")
	h.wantScreen("No apps of yours are installed on Pixel 9a")

	h.backend.installed = []device.App{{ID: "com.example.app"}}
	h.backend.fail["launch"] = apperr.New(apperr.NotFound, "com.example.app has nothing to launch on Pixel 9a")
	h.press("o", "enter", "enter")
	h.wantScreen("Could not finish: launching com.example.app on Pixel 9a", "has nothing to launch")
}

// androidCrash is a real crash report from an Android 16 emulator (`am crash` on Settings).
const androidCrash = `11-16 07:50:32.344 11123 11123 D AndroidRuntime: Shutting down VM
11-16 07:50:32.344 11123 11123 E AndroidRuntime: FATAL EXCEPTION: main
11-16 07:50:32.344 11123 11123 E AndroidRuntime: Process: com.android.settings, PID: 11123
11-16 07:50:32.344 11123 11123 E AndroidRuntime: android.app.RemoteServiceException$CrashedByAdbException: shell-induced crash
11-16 07:50:32.344 11123 11123 E AndroidRuntime: 	at android.app.ActivityThread.throwRemoteServiceException(ActivityThread.java:2377)
11-16 07:50:32.344 11123 11123 E AndroidRuntime: 	at com.android.internal.os.ZygoteInit.main(ZygoteInit.java:932)`

func TestEAndShiftEJumpBetweenErrorsAndCrashes(t *testing.T) {
	h := start(t, pixel)
	h.openLogs(pixel, "")
	info := func(from, to int) {
		for i := from; i < to; i++ {
			h.emit(logs.Line{Text: fmt.Sprintf("info %d", i), Level: logs.Info})
		}
	}
	info(0, 40)
	// A crash report: every line is an error, and it is one stop.
	for _, text := range strings.Split(androidCrash, "\n")[1:] {
		h.emit(logs.ParseAndroid(text))
	}
	info(40, 80)
	h.emit(logs.Line{Text: "plain error elsewhere", Level: logs.Error})
	info(80, 120)

	h.press("g")
	h.wantScreen("info 0")
	h.press("e")
	h.wantScreen("AndroidRuntime: FATAL")
	h.wantNoScreen("info 0")

	// The next stop is the other error, not the rest of the crash report.
	h.press("e")
	h.wantScreen("plain error elsewhere")
	h.wantNoScreen("AndroidRuntime: FATAL")

	h.press("e")
	h.wantScreen("No error below this line")

	h.press("E")
	h.wantScreen("AndroidRuntime: FATAL")
	h.press("E")
	h.wantScreen("No error above this line")
}

func TestCrashLinesAreBoldRedWhereErrorsAreNot(t *testing.T) {
	crash, plain := lineStyle(logs.Line{Text: "x", Level: logs.Error, Crash: true}), lineStyle(logs.Line{Text: "x", Level: logs.Error})
	if !crash.GetBold() || plain.GetBold() {
		t.Errorf("a crash is bold (%v) and an error is not (%v)", crash.GetBold(), plain.GetBold())
	}
	if crash.GetForeground() != plain.GetForeground() {
		t.Error("a crash stays red")
	}
}

func TestPSavesAScreenshotInTheCurrentFolder(t *testing.T) {
	h := start(t, pixel)
	h.press("p")
	h.wantCalls("screenshot Pixel_9a lazymobile-Pixel-9a-", 1)
	h.wantScreen("Saved lazymobile-Pixel-9a-", ".png from Pixel 9a")

	h.backend.fail["screenshot"] = apperr.New(apperr.InvalidState, "x.png already exists")
	h.press("p")
	h.wantScreen("Could not finish: taking a screenshot of Pixel 9a", "already exists")
}

func TestTOpensALinkAfterAskingForIt(t *testing.T) {
	h := start(t, iphone)
	h.press("t")
	h.wantScreen("Open a link on iPhone 17", "myapp://home?id=1")
	h.typeText("not a link")
	h.press("enter")
	h.wantScreen("Not a whole link")
	h.wantCalls("link", 0)

	h.press("ctrl+u")
	h.typeText("myapp://home?id=1&x=2")
	h.press("enter")
	h.wantCalls("link SIM-1 myapp://home?id=1&x=2", 1)
	h.wantScreen("Opened a link on iPhone 17")
}

func TestScreenshotsAndLinksNeedARunningDeviceThatIsNotAnIPhone(t *testing.T) {
	h := start(t, ipad, usbPhone)
	h.press("p")
	h.wantScreen("iPad Air is not running")
	h.press("t")
	h.wantScreen("iPad Air is not running")

	h.selectDevice(usbPhone)
	h.press("p")
	h.wantScreen("Test iPhone is an iPhone: Apple's tools cannot do this from here")
	h.press("t")
	h.wantScreen("Test iPhone is an iPhone")
	h.wantCalls("screenshot", 0)
	h.wantCalls("link", 0)
	if h.m.modal != nil {
		t.Error("a window opened for an iPhone")
	}
}

func TestMSwitchesBetweenDarkAndLightMode(t *testing.T) {
	h := start(t, iphone)
	h.backend.mode = "light"
	h.press("m")
	h.wantCalls("set-appearance SIM-1 dark", 1)
	h.wantScreen("Switched dark and light mode on iPhone 17")

	h.backend.mode = "dark"
	h.press("m")
	h.wantCalls("set-appearance SIM-1 light", 1)

	// A device that follows the time of day is put in dark mode.
	h.backend.mode = "auto"
	h.press("m")
	h.wantCalls("set-appearance SIM-1 dark", 2)

	h.backend.fail["set-appearance"] = apperr.New(apperr.CommandFailed, "Dark mode needs Android 10 or newer.")
	h.press("m")
	h.wantScreen("Could not finish: switching dark and light mode on iPhone 17", "Android 10")
}

func TestMNeedsARunningDeviceThatIsNotAnIPhone(t *testing.T) {
	h := start(t, ipad, usbPhone)
	h.press("m")
	h.wantScreen("iPad Air is not running")
	h.selectDevice(usbPhone)
	h.press("m")
	h.wantScreen("Test iPhone is an iPhone")
	h.wantCalls("appearance", 0)
}

func TestFAsksWhereAndPutsTheDeviceThere(t *testing.T) {
	h := start(t, iphone, pixel)
	h.press("f")
	h.wantScreen("Where is iPhone 17?", "Type clear to take the fake location off")
	h.typeText("nowhere")
	h.press("enter")
	h.wantScreen("Write latitude,longitude")
	h.wantCalls("location", 0)

	h.press("ctrl+u")
	h.typeText("37.7749,-122.4194")
	h.press("enter")
	h.wantCalls("location SIM-1 37.7749,-122.4194", 1)
	h.wantScreen("Set the location of iPhone 17")

	h.press("f", "ctrl+u")
	h.typeText("clear")
	h.press("enter")
	h.wantCalls("clear-location SIM-1", 1)

	// An emulator cannot forget a location, so it is not offered clear.
	h.selectDevice(pixel)
	h.press("f")
	h.wantScreen("Where is Pixel 9a?")
	h.wantNoScreen("Type clear")
	h.typeText("clear")
	h.press("enter")
	h.wantScreen("Write latitude,longitude")
	h.wantCalls("clear-location Pixel_9a", 0)
}

func TestCAndShiftCTidyAndRestoreTheStatusBar(t *testing.T) {
	h := start(t, iphone)
	h.press("c")
	h.wantCalls("statusbar SIM-1 clean=true", 1)
	h.wantScreen("Tidied the status bar of iPhone 17")
	h.press("C")
	h.wantCalls("statusbar SIM-1 clean=false", 1)
	h.wantScreen("Restored the status bar of iPhone 17")
}

func TestLocationAndStatusBarKeysLeaveRealPhonesAloneAndNeedARunningDevice(t *testing.T) {
	h := start(t, ipad, usbPhone, wifiPhon)
	h.press("f")
	h.wantScreen("iPad Air is not running")
	for _, d := range []device.Device{usbPhone, wifiPhon} {
		h.selectDevice(d)
		h.press("f")
		h.wantScreen(d.Name + " is a real device: its location and status bar are left alone")
		h.press("c")
		h.wantScreen(d.Name + " is a real device")
	}
	h.wantCalls("location", 0)
	h.wantCalls("statusbar", 0)
	if h.m.modal != nil {
		t.Error("a window opened for a real device")
	}
}

func TestAnAppsPermissionsAreGrantedAndRevokedFromTheAppMenu(t *testing.T) {
	h := start(t, iphone)
	h.backend.installed = []device.App{{ID: "com.example.app"}}
	h.press("o", "enter")
	h.wantScreen("com.example.app on iPhone 17", "Grant a permission…", "Revoke a permission…")
	h.wantNoScreen("Clear its data")

	h.press("down", "down", "down", "enter")
	h.wantScreen("Grant which permission of com.example.app?", "photos", "microphone")
	h.wantNoScreen("camera")
	h.typeText("/")
	h.typeText("photos")
	h.press("enter")
	h.wantCalls("permission SIM-1 com.example.app photos grant=true", 1)
	h.wantScreen("Granted photos to com.example.app on iPhone 17")

	h.press("o", "enter", "down", "down", "down", "down", "enter")
	h.wantScreen("Revoke which permission of com.example.app?")
	h.press("enter")
	h.wantCalls("permission SIM-1 com.example.app location grant=false", 1)
	h.wantScreen("Revoked location from com.example.app on iPhone 17")
}

func TestClearingAnEmulatorsAppDataAsksFirst(t *testing.T) {
	h := start(t, pixel)
	h.backend.installed = []device.App{{ID: "com.example.app"}}
	h.press("o", "enter", "down", "down", "down", "down", "down", "enter")
	h.wantScreen("Clear the data of com.example.app?", "everything com.example.app saved on Pixel 9a", "cannot be undone")
	h.wantCalls("cleardata", 0)

	// Enter must not confirm something that cannot be undone, and no leaves it alone.
	h.press("enter")
	h.wantCalls("cleardata", 0)
	h.press("n")
	h.wantNoScreen("Clear the data of")
	h.wantCalls("cleardata", 0)

	h.press("o", "enter", "down", "down", "down", "down", "down", "enter", "y")
	h.wantCalls("cleardata Pixel_9a com.example.app", 1)
	h.wantScreen("Cleared the data of com.example.app on Pixel 9a")
}

func TestRealPhonesAreOfferedNoPermissionOrDataActions(t *testing.T) {
	h := start(t, usbPhone, wifiPhon)
	h.backend.installed = []device.App{{ID: "com.example.app"}}
	for _, d := range []device.Device{usbPhone, wifiPhon} {
		h.selectDevice(d)
		h.press("o", "enter")
		h.wantScreen("com.example.app on " + d.Name)
		h.wantNoScreen("permission")
		h.wantNoScreen("Clear its data")
		h.press("esc")
	}
}

func TestInstallOffersRememberedAndFoundBuildsAndATypedPath(t *testing.T) {
	dir := t.TempDir()
	apk := filepath.Join(dir, "app", "build", "outputs", "apk", "debug", "app-debug.apk")
	old := filepath.Join(t.TempDir(), "old.apk")
	for _, path := range []string{apk, old} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	memory := Memory{Store: state.Store{Path: filepath.Join(t.TempDir(), "state.json")}, Folder: dir}
	_ = memory.Store.Update(func(f *state.File) {
		f.RememberInstall(device.Android, old)
		f.RememberInstall(device.Android, "/gone/missing.apk")
	})
	h := startWith(t, memory, pixel)

	h.press("i")
	h.wantScreen("Install on Pixel 9a", "old.apk", "app/build/outputs/apk/debug/app-debug.apk", "Type a path…")
	h.wantNoScreen("missing.apk")
	h.press("down", "enter")
	h.wantCalls("install Pixel_9a "+apk, 1)
	h.wantScreen("Installed app-debug.apk on Pixel 9a")
	if got := memory.Store.Load().Installs[device.Android]; len(got) == 0 || got[0] != apk {
		t.Errorf("remembered %v", got)
	}

	h.press("i", "down", "down", "enter")
	h.wantScreen("The path of an .apk file.")
	h.typeText("/nowhere.apk")
	h.press("enter")
	h.wantScreen("There is nothing at that path.")
	h.wantCalls("nowhere", 0)
}

func TestInstallOnAnIPhoneTakesAnAppFolderAndRefusesAStoppedDevice(t *testing.T) {
	h := start(t, usbPhone)
	h.press("i", "enter")
	h.wantScreen("The path of an .app folder.")
	h.typeText("/tmp/thing.apk")
	h.press("enter")
	h.wantScreen("It must end in .app.")
	h.wantCalls("install", 0)

	stopped := pixel
	stopped.State = device.Shutdown
	g := start(t, stopped)
	g.press("i")
	g.wantScreen("is not running")
	g.wantNoScreen("Install on")
}

func TestDOffersTheDevHelpersOnAnAndroidDevice(t *testing.T) {
	h := start(t, pixel)
	h.press("D")
	h.wantScreen("Help the app on Pixel 9a", "Reach this computer on port 8081", "Open the developer menu", "Reload the app")
	h.press("j", "j", "enter")
	h.wantCalls("dev Pixel_9a reload 8081", 1)
	h.wantScreen("Reloaded the app on Pixel 9a")
}

func TestDRefusesASimulatorAndAStoppedDevice(t *testing.T) {
	h := start(t, iphone, ipad)
	h.press("D")
	h.wantScreen("iPhone 17 is not an Android device")
	h.selectDevice(ipad)
	h.press("D")
	h.wantScreen("iPad Air is not running")
	h.wantCalls("dev", 0)
	if h.m.modal != nil {
		t.Error("a window opened")
	}
}

func TestAMessageBreaksALongWordRatherThanCuttingIt(t *testing.T) {
	md := &modal{kind: modalMessage, body: "\tat android.app.ActivityThread.throwRemoteServiceException(ActivityThread.java:2377)"}
	lines := md.bodyLines(40)
	joined := strings.ReplaceAll(strings.Join(lines, ""), " ", "")
	if !strings.Contains(joined, "(ActivityThread.java:2377)") {
		t.Errorf("lines = %q, want all of the frame", lines)
	}
	if !strings.HasPrefix(lines[0], "     at") {
		t.Errorf("first line = %q, want the tab as an indent", lines[0])
	}
	for _, line := range lines {
		if ansi.StringWidth(line) > 39 {
			t.Errorf("line %q is wider than the window", line)
		}
	}
}

func TestABugReportIsSavedFromTheAppMenu(t *testing.T) {
	h := startWith(t, Memory{Version: "v9.9.9"}, iphone)
	h.backend.installed = []device.App{{ID: "com.example.app"}}
	h.backend.reported = mobile.Report{Included: []string{"screenshot.png", "log.txt", "summary.txt"}}
	name := mobile.ReportName(iphone, h.clock)
	// Launch, Restart, Stop, Grant, Revoke, the crash report, then the bug report.
	h.press("o", "enter", "down", "down", "down", "down", "down", "down")
	h.wantScreen("Save a bug report in this folder")
	h.press("enter")
	h.wantCalls("report SIM-1 com.example.app "+name+" v9.9.9", 1)
	h.wantScreen("Saved the bug report in " + name)
	h.wantNoScreen("without")
}

func TestABugReportSaysWhatItLeftOut(t *testing.T) {
	h := start(t, iphone)
	h.backend.installed = []device.App{{ID: "com.example.app"}}
	h.backend.reported = mobile.Report{
		Included: []string{"log.txt", "summary.txt"},
		Skipped:  []string{"screenshot: could not take it", "crash report: No crash found"},
	}
	h.press("o", "enter", "down", "down", "down", "down", "down", "down", "enter")
	h.wantScreen("without the screenshot, the crash report")

	h.backend.fail = map[string]error{"report": apperr.New(apperr.InvalidState, "the folder exists")}
	h.press("o", "enter", "down", "down", "down", "down", "down", "down", "enter")
	h.wantScreen("Could not save a bug report of com.example.app")
}

func TestAnIPhoneOffersNoBugReport(t *testing.T) {
	h := start(t, usbPhone)
	h.backend.installed = []device.App{{ID: "com.example.app"}}
	h.press("o", "enter")
	h.wantNoScreen("bug report")
}

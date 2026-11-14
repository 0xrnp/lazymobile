package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/0xrnp/lazymobile/internal/android"
	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/ios"
	"github.com/0xrnp/lazymobile/internal/logs"
	"github.com/0xrnp/lazymobile/internal/mobile"
)

var (
	iphone   = device.Device{ID: "SIM-1", Name: "iPhone 17", Platform: device.IOS, Kind: device.Simulator, State: device.Booted, OSVersion: "27.2", Source: "simctl"}
	ipad     = device.Device{ID: "SIM-2", Name: "iPad Air", Platform: device.IOS, Kind: device.Simulator, State: device.Shutdown, OSVersion: "27.2", Source: "simctl"}
	pixel    = device.Device{ID: "Pixel_9a", Name: "Pixel 9a", Platform: device.Android, Kind: device.Emulator, State: device.Booted, OSVersion: "16", Serial: "emulator-5554", Source: "emulator"}
	tablet   = device.Device{ID: "Tablet", Name: "Tablet", Platform: device.Android, Kind: device.Emulator, State: device.Shutdown, OSVersion: "15", Source: "emulator"}
	galaxy   = device.Device{ID: "R58M", Name: "Galaxy S23", Platform: device.Android, Kind: device.Physical, State: device.Unauthorized, Transport: "usb", Source: "adb", Detail: "Unlock the phone and accept the USB debugging prompt."}
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
	sessions []*fakeSession
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
	backend := &fakeBackend{devices: devices, fail: map[string]error{}}
	h := &harness{t: t, backend: backend, clock: time.Unix(1_800_000_000, 0)}
	h.m = New(context.Background(), backend, nil)
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
	case <-time.After(150 * time.Millisecond):
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
	"backspace": {Code: tea.KeyBackspace},
	"up":        {Code: tea.KeyUp},
	"down":      {Code: tea.KeyDown},
	"ctrl+c":    {Code: 'c', Mod: tea.ModCtrl},
	"ctrl+u":    {Code: 'u', Mod: tea.ModCtrl},
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

func TestRefreshIsQuickOnlyWhileSomethingIsChanging(t *testing.T) {
	h := start(t, ipad)
	tick := func(after time.Duration) {
		h.clock = h.clock.Add(after)
		_, cmd := h.m.Update(tickMsg{})
		h.run(cmd)
	}
	tick(3 * time.Second)
	h.wantCalls("list", 1)
	tick(8 * time.Second)
	h.wantCalls("list", 2)

	booting := ipad
	booting.State = device.Booting
	h.backend.devices = []device.Device{booting}
	h.press("r")
	h.wantCalls("list", 3)
	tick(3 * time.Second)
	h.wantCalls("list", 4)
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
	for h.m.log.level != logs.Error {
		h.press("L")
	}
	h.wantScreen("E network failed", "at com.example.Net.call", "error+")
	h.wantNoScreen("D debug line", "I network ok", "a line after an info line")

	for h.m.log.level != logs.NoLevel {
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
	if got := len(h.m.log.all); got != maxLogLines {
		t.Errorf("kept %d lines, want %d", got, maxLogLines)
	}
	if first := h.m.log.shown[0].Text; first != "line-5000" {
		t.Errorf("the oldest line kept is %s, want line-5000", first)
	}
	h.wantScreen("line-24999")
}

func TestANewStreamReplacesTheOldOne(t *testing.T) {
	h := start(t, pixel, iphone)
	h.openLogs(pixel, "")
	first := h.backend.handlers[0]
	h.emit(logs.Line{Text: "from the pixel"})

	h.press("tab")
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
	h.m.Update(logEndMsg{id: h.m.log.id, err: apperr.New(apperr.CommandFailed, "adb failed: device offline")})
	h.wantScreen("the last line", "adb failed: device offline", "ended")

	h.press("r")
	h.wantCalls("logs Pixel_9a", 2)
	h.wantNoScreen("device offline")
}

func TestSaveLog(t *testing.T) {
	h := start(t, pixel)
	h.openLogs(pixel, "")
	h.emit(logs.Line{Text: "D hidden", Level: logs.Debug}, logs.Line{Text: "E kept", Level: logs.Error})
	for h.m.log.level != logs.Error {
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

func TestHelp(t *testing.T) {
	h := start(t, iphone)
	h.press("?")
	h.wantScreen("cold boot", "minimum level")
	h.press("G")
	h.wantScreen("keep running after you quit")
	h.press("q")
	h.wantScreen("iPhone 17")
	if h.m.modal != nil {
		t.Error("help did not close")
	}
}

// A window opens over the dashboard, which stays in view behind it.
func TestTheDashboardStaysVisibleBehindAWindow(t *testing.T) {
	h := start(t, iphone, pixel)
	h.press("?")
	screen := h.screen()
	for _, want := range []string{"Keys", "cold boot", "Devices", "Logs"} {
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
	// The window is drawn over the panes, so one row holds the device behind and the help in front.
	both := false
	for _, line := range lines {
		both = both || strings.Contains(line, "iPhone 17") && strings.Contains(line, "arrows")
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
	h.press("?")
	h.wantScreen("j k: scroll", "0%")
	h.wantNoScreen("keep running after you quit")
	h.press("j")
	if h.m.modal == nil {
		t.Fatal("scrolling closed the window")
	}
	h.press("G")
	h.wantScreen("keep running after you quit", "100%")
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

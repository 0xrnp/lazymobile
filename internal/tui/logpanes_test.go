package tui

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/logs"
	"github.com/0xrnp/lazymobile/internal/state"
)

// openSecond opens a log of d, typing app. It goes beside the log that is open, or takes the place
// of the one that had the focus last when there are two.
func (h *harness) openSecond(d device.Device, app string) {
	h.t.Helper()
	h.press("esc")
	h.selectDevice(d)
	h.press("l")
	// With app ids used before, a list comes first, and "Type an app id" is its last item.
	if h.m.modal != nil && h.m.modal.kind == modalChoice {
		h.press("G", "enter")
	}
	h.press("ctrl+u")
	h.typeText(app)
	h.press("enter")
}

// emitTo sends lines to the n-th stream that was started.
func (h *harness) emitTo(n int, lines ...logs.Line) {
	h.t.Helper()
	h.backend.handlers[n].Lines(lines)
}

// twoLogs is a dashboard with a log of the emulator and one of the Wi-Fi phone open.
func twoLogs(t *testing.T) *harness {
	t.Helper()
	h := start(t, pixel, wifiPhon)
	h.openLogs(pixel, "app.one")
	h.openSecond(wifiPhon, "app.two")
	h.wantCalls("logs Pixel_9a", 1)
	h.wantCalls("logs 192.168.1.9:5555", 1)
	return h
}

// screenAt is the row and column where text starts on the screen.
func screenAt(t *testing.T, h *harness, text string) (row, col int) {
	t.Helper()
	for y, line := range strings.Split(h.screen(), "\n") {
		if i := strings.Index(line, text); i >= 0 {
			return y, utf8.RuneCountInString(line[:i])
		}
	}
	t.Fatalf("%q is not on the screen:\n%s", text, h.screen())
	return 0, 0
}

func TestASecondLogOpensBesideTheFirstAndKeepsItsOwnLines(t *testing.T) {
	h := twoLogs(t)
	h.emitTo(0, logs.Line{Text: "from-the-first", Level: logs.Info})
	h.emitTo(1, logs.Line{Text: "from-the-second", Level: logs.Info})
	h.wantScreen("Logs 1/2: Pixel 9a", "app.one", "from-the-first", "Logs 2/2: Pixel 8", "app.two", "from-the-second")
	if !h.m.log.opened || !h.m.second.opened || h.m.focus != paneLogs2 {
		t.Errorf("first %v, second %v, focus %v", h.m.log.opened, h.m.second.opened, h.m.focus)
	}
	if got := len(h.m.log.all); got != 1 {
		t.Errorf("the first log has %d lines, want its own 1", got)
	}
}

func TestTwoLogsAreSideBySideUntilVSaysOtherwise(t *testing.T) {
	h := twoLogs(t)
	row1, col1 := screenAt(t, h, "Logs 1/2: Pixel 9a")
	row2, col2 := screenAt(t, h, "Logs 2/2: Pixel 8")
	if row1 != row2 || col2 <= col1 {
		t.Errorf("side by side: first at (%d,%d), second at (%d,%d)", row1, col1, row2, col2)
	}

	h.press("v")
	h.wantScreen("Logs one above the other")
	row1, col1 = screenAt(t, h, "Logs 1/2: Pixel 9a")
	row2, col2 = screenAt(t, h, "Logs 2/2: Pixel 8")
	if row2 <= row1 || col1 != col2 {
		t.Errorf("stacked: first at (%d,%d), second at (%d,%d)", row1, col1, row2, col2)
	}

	// Then one at a time: the one in focus, beside the devices.
	h.press("v")
	h.wantScreen("One log at a time", "Devices", "Logs 2/2: Pixel 8")
	h.wantNoScreen("Logs 1/2: Pixel 9a")

	h.press("v")
	h.wantScreen("Logs side by side")
	row1, _ = screenAt(t, h, "Logs 1/2: Pixel 9a")
	row2, _ = screenAt(t, h, "Logs 2/2: Pixel 8")
	if row1 != row2 {
		t.Errorf("side by side again: first at row %d, second at row %d", row1, row2)
	}
}

func TestOneLogAtATimeKeepsTheDevicesAndTabFlipsBetweenTheLogs(t *testing.T) {
	h := twoLogs(t)
	h.emitTo(0, logs.Line{Text: "from-the-first", Level: logs.Info})
	h.emitTo(1, logs.Line{Text: "from-the-second", Level: logs.Info})
	h.press("v", "v")
	h.wantScreen("tab other log", "esc devices", "Devices", "from-the-second")
	h.wantNoScreen("from-the-first")

	// Tab shows the other log, and the devices stay on the screen.
	h.press("tab")
	if h.m.focus != paneLogs {
		t.Fatalf("focus = %v, want the first log", h.m.focus)
	}
	h.wantScreen("Devices", "Logs 1/2: Pixel 9a", "from-the-first")
	h.wantNoScreen("from-the-second")
	h.press("tab")
	h.wantScreen("Logs 2/2: Pixel 8", "from-the-second")

	// Esc goes to the devices, which still show the log that was last in focus.
	h.press("esc")
	if h.m.focus != paneDevices {
		t.Fatalf("focus = %v", h.m.focus)
	}
	h.wantScreen("Logs 2/2: Pixel 8")
	h.wantNoScreen("Logs 1/2: Pixel 9a")
	// Tab from the devices goes back to a log.
	h.press("tab")
	if h.focusIsNotALog() {
		t.Errorf("focus = %v, want a log", h.m.focus)
	}
}

func TestZoomInOneAtATimeStillFlipsAndGoesBack(t *testing.T) {
	h := twoLogs(t)
	h.press("v", "v", "z")
	h.wantNoScreen("Devices")
	h.press("tab")
	h.wantScreen("Logs 1/2: Pixel 9a")
	h.press("z")
	h.wantScreen("Devices", "Logs 1/2: Pixel 9a")
	h.wantNoScreen("Logs 2/2: Pixel 8")
}

func TestTheLayoutComesBackOnTheNextStart(t *testing.T) {
	for _, layout := range []arrangement{arrangeSide, arrangeStacked, arrangeOne} {
		memory := memoryIn(t)
		first := startWith(t, memory, pixel, wifiPhon)
		first.openLogs(pixel, "app.one")
		first.openSecond(wifiPhon, "app.two")
		for first.m.arrange != layout {
			first.press("v")
		}
		first.press("q")

		h := startWith(t, memory, pixel, wifiPhon)
		if h.m.arrange != layout || !h.m.second.opened {
			t.Errorf("layout %q came back as %q, second open %v", layout, h.m.arrange, h.m.second.opened)
		}
	}
}

func (h *harness) focusIsNotALog() bool { return !h.m.focusOnLog() }

func TestVBeforeASecondLogSaysHowToOpenOne(t *testing.T) {
	h := start(t, pixel)
	h.press("v")
	h.wantScreen("Press l on another device to open a second log")
}

func TestEachLogPaneKeepsItsOwnFilterAndLevel(t *testing.T) {
	h := twoLogs(t)
	h.press("/")
	h.typeText("needle")
	h.press("enter")
	if h.m.second.filter != "needle" || h.m.log.filter != "" {
		t.Errorf("filters: first %q, second %q", h.m.log.filter, h.m.second.filter)
	}

	before := h.m.log.level
	h.press("L")
	if h.m.log.level != before || !h.m.second.levelChosen || h.m.log.levelChosen {
		t.Errorf("the level of the second changed the first: %v, chosen %v and %v", h.m.log.level, h.m.log.levelChosen, h.m.second.levelChosen)
	}

	h.press("tab", "tab")
	if h.m.focus != paneLogs {
		t.Fatalf("focus = %v, want the first log", h.m.focus)
	}
	h.press("w")
	if !h.m.log.wrap || h.m.second.wrap {
		t.Errorf("wrap: first %v, second %v", h.m.log.wrap, h.m.second.wrap)
	}
}

func TestTabVisitsBothLogs(t *testing.T) {
	h := twoLogs(t)
	var seen []pane
	for range 3 {
		h.press("tab")
		seen = append(seen, h.m.focus)
	}
	want := []pane{paneDevices, paneLogs, paneLogs2}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("tab went to %v, want %v", seen, want)
		}
	}
}

func TestLOpensBesideTheLogThatIsOpenAndTheTitlesSayWhichIsWhich(t *testing.T) {
	h := start(t, pixel, wifiPhon)
	h.openLogs(pixel, "app.one")
	// One log is just "Logs".
	h.wantScreen("Logs: Pixel 9a")
	h.wantNoScreen("1/2")

	h.openSecond(wifiPhon, "app.two")
	h.wantScreen("Logs 1/2: Pixel 9a", "Logs 2/2: Pixel 8")
	if !h.m.log.opened || !h.m.second.opened || h.m.focus != paneLogs2 {
		t.Errorf("first %v, second %v, focus %v", h.m.log.opened, h.m.second.opened, h.m.focus)
	}
	// Back to one log, and the numbers go.
	h.press("X")
	h.wantScreen("Logs: Pixel 9a")
	h.wantNoScreen("1/2")
}

func TestCapitalLInTheDevicesDoesNothing(t *testing.T) {
	h := start(t, pixel)
	h.selectDevice(pixel)
	h.press("L")
	if h.m.modal != nil || h.m.log.opened {
		t.Errorf("L opened something: modal %v, log %v", h.m.modal != nil, h.m.log.opened)
	}
}

func TestLOpensTheSecondAgainInPlaceOfTheOneThatIsThere(t *testing.T) {
	h := twoLogs(t)
	// Both panes are taken, so a third log replaces the one that had the focus last.
	h.openSecond(pixel, "app.three")
	h.wantCalls("logs Pixel_9a mode=logcat app=app.three", 1)
	if !h.backend.sessions[1].isStopped() {
		t.Error("the log that was replaced is still running")
	}
	if h.m.second.spec.App != "app.three" || h.m.log.spec.App != "app.one" {
		t.Errorf("first %q, second %q", h.m.log.spec.App, h.m.second.spec.App)
	}
	// A late line of the replaced stream goes nowhere.
	h.emitTo(1, logs.Line{Text: "from-the-replaced"})
	if got := len(h.m.second.all); got != 0 {
		t.Errorf("a replaced stream still writes: %d lines", got)
	}
}

func TestLOpensInTheLogThatHadTheFocusLast(t *testing.T) {
	h := twoLogs(t)
	// The focus goes to the devices, then to the first log, then back to the devices.
	h.press("tab", "tab", "esc")
	h.selectDevice(pixel)
	h.press("l", "ctrl+u")
	h.typeText("app.three")
	h.press("enter")
	if h.m.log.spec.App != "app.three" || h.m.second.spec.App != "app.two" {
		t.Errorf("first %q, second %q", h.m.log.spec.App, h.m.second.spec.App)
	}
}

func TestXClosesTheSecondLog(t *testing.T) {
	h := twoLogs(t)
	h.press("X")
	if h.m.second.opened || !h.m.log.opened || h.m.focus != paneLogs {
		t.Errorf("first %v, second %v, focus %v", h.m.log.opened, h.m.second.opened, h.m.focus)
	}
	if !h.backend.sessions[1].isStopped() || h.backend.sessions[0].isStopped() {
		t.Error("the wrong log was stopped")
	}
	h.wantNoScreen("Logs 2/2: Pixel 8")
	h.emitTo(1, logs.Line{Text: "from-the-closed"})
	h.wantNoScreen("from-the-closed")
}

func TestClosingTheFirstLogMovesTheSecondUp(t *testing.T) {
	h := twoLogs(t)
	h.emitTo(1, logs.Line{Text: "from-the-second", Level: logs.Info})
	h.press("tab", "tab")
	if h.m.focus != paneLogs {
		t.Fatalf("focus = %v", h.m.focus)
	}
	h.press("X")
	if !h.m.log.opened || h.m.second.opened || h.m.focus != paneLogs {
		t.Fatalf("first %v, second %v, focus %v", h.m.log.opened, h.m.second.opened, h.m.focus)
	}
	if h.m.log.spec.Device.Key() != wifiPhon.Key() {
		t.Errorf("the first log is of %s, want the second one", h.m.log.spec.Device.Name)
	}
	h.wantScreen("Logs: Pixel 8", "from-the-second")
	if !h.backend.sessions[0].isStopped() || h.backend.sessions[1].isStopped() {
		t.Error("the wrong log was stopped")
	}
	// The moved stream still reaches its pane.
	h.emitTo(1, logs.Line{Text: "after-the-move", Level: logs.Info})
	h.wantScreen("after-the-move")

	h.press("X")
	if h.m.log.opened || h.m.focus != paneDevices {
		t.Errorf("closing the last log: opened %v, focus %v", h.m.log.opened, h.m.focus)
	}
}

func TestQuittingStopsBothLogs(t *testing.T) {
	h := twoLogs(t)
	h.run(h.m.quit())
	for i, s := range h.backend.sessions {
		if !s.isStopped() {
			t.Errorf("log %d is still running after quit", i)
		}
	}
}

func TestNarrowScreensShowOnlyTheLogInFocus(t *testing.T) {
	h := twoLogs(t)
	h.m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	h.wantScreen("Logs 2/2: Pixel 8")
	h.wantNoScreen("Logs 1/2: Pixel 9a")
	h.press("tab", "tab")
	h.wantScreen("Logs 1/2: Pixel 9a")
	h.wantNoScreen("Logs 2/2: Pixel 8")
}

func TestZoomMakesTheLogInFocusFullScreenAndTabFlipsBetweenTheLogs(t *testing.T) {
	h := twoLogs(t)
	h.wantScreen("z full screen", "tab next pane")
	h.press("z")
	h.wantScreen("Logs 2/2: Pixel 8", "z back", "tab other log")
	h.wantNoScreen("Logs 1/2: Pixel 9a")
	h.wantNoScreen("Devices")

	// Tab goes to the other log, not out to the devices.
	h.press("tab")
	if h.m.focus != paneLogs {
		t.Fatalf("focus = %v, want the first log", h.m.focus)
	}
	h.wantScreen("Logs 1/2: Pixel 9a")
	h.wantNoScreen("Logs 2/2: Pixel 8")
	h.press("tab")
	h.wantScreen("Logs 2/2: Pixel 8")

	h.press("z")
	h.wantScreen("Devices", "Logs 1/2: Pixel 9a", "Logs 2/2: Pixel 8")
}

func TestEscLeavesTheFullScreenAndGoesToTheDevices(t *testing.T) {
	h := twoLogs(t)
	h.press("z", "esc")
	if h.m.zoomed || h.m.focus != paneDevices {
		t.Errorf("zoomed %v, focus %v", h.m.zoomed, h.m.focus)
	}
	h.wantScreen("Devices", "Logs 1/2: Pixel 9a", "Logs 2/2: Pixel 8")
}

func TestAZoomedSingleLogSaysHowToGoBack(t *testing.T) {
	h := start(t, pixel)
	h.openLogs(pixel, "app.one")
	h.wantScreen("z full screen")
	h.press("z")
	h.wantScreen("z back")
	h.wantNoScreen("tab other log")
}

func TestTwoLogsThatWouldBeTooSmallShowOnlyTheOneInFocus(t *testing.T) {
	h := twoLogs(t)
	// One above the other needs room for both.
	h.press("v")
	h.m.Update(tea.WindowSizeMsg{Width: 120, Height: 12})
	h.wantScreen("Logs 2/2: Pixel 8")
	h.wantNoScreen("Logs 1/2: Pixel 9a")

	// Side by side needs 30 columns each. Beside a devices pane of 48, 100 columns leave 52.
	h.m.arrange = arrangeSide
	h.m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	h.wantScreen("Logs 2/2: Pixel 8")
	h.wantNoScreen("Logs 1/2: Pixel 9a")
	h.m.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	h.wantScreen("Logs 1/2: Pixel 9a", "Logs 2/2: Pixel 8")
}

func TestScreenFillsTheTerminalWithTwoLogs(t *testing.T) {
	for _, side := range []bool{false, true} {
		for _, size := range [][2]int{{120, 30}, {101, 25}, {200, 40}, {100, 13}} {
			h := twoLogs(t)
			h.m.arrange = map[bool]arrangement{true: arrangeSide, false: arrangeStacked}[side]
			h.m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			lines := strings.Split(h.screen(), "\n")
			if len(lines) != size[1] {
				t.Errorf("side %v at %v: %d rows", side, size, len(lines))
			}
			for _, line := range lines {
				if w := utf8.RuneCountInString(line); w > size[0] {
					t.Errorf("side %v at %v: a line is %d wide: %q", side, size, w, line)
				}
			}
		}
	}
}

func TestClickingALogFocusesIt(t *testing.T) {
	h := twoLogs(t)
	row1, col1 := screenAt(t, h, "Logs 1/2: Pixel 9a")
	h.m.Update(tea.MouseClickMsg{X: col1 + 3, Y: row1 + 2, Button: tea.MouseLeft})
	if h.m.focus != paneLogs {
		t.Errorf("focus = %v after clicking the first log", h.m.focus)
	}
	row2, col2 := screenAt(t, h, "Logs 2/2: Pixel 8")
	h.m.Update(tea.MouseClickMsg{X: col2 + 3, Y: row2 + 2, Button: tea.MouseLeft})
	if h.m.focus != paneLogs2 {
		t.Errorf("focus = %v after clicking the second log", h.m.focus)
	}
}

func TestTheWheelScrollsTheLogUnderIt(t *testing.T) {
	h := twoLogs(t)
	var lines []logs.Line
	for i := range 200 {
		lines = append(lines, logs.Line{Text: fmt.Sprintf("line-%d", i), Level: logs.Info})
	}
	h.emitTo(0, lines...)
	h.emitTo(1, lines...)
	row, col := screenAt(t, h, "Logs 1/2: Pixel 9a")
	h.m.Update(tea.MouseWheelMsg{X: col + 3, Y: row + 2, Button: tea.MouseWheelUp})
	if h.m.log.follow || !h.m.second.follow {
		t.Errorf("follow: first %v, second %v", h.m.log.follow, h.m.second.follow)
	}
}

func TestBothLogsComeBackOnTheNextStartWithTheirOwnSettings(t *testing.T) {
	memory := memoryIn(t)
	first := startWith(t, memory, pixel, wifiPhon)
	first.openLogs(pixel, "app.one")
	first.openSecond(wifiPhon, "app.two")
	first.press("w", "L")
	first.m.second.filter = "needle"
	first.m.log.filter = "other"
	first.press("v")
	first.press("q")

	h := startWith(t, memory, pixel, wifiPhon)
	h.wantCalls("logs Pixel_9a mode=logcat app=app.one", 1)
	h.wantCalls("logs 192.168.1.9:5555 mode=logcat app=app.two", 1)
	if !h.m.log.opened || !h.m.second.opened || h.m.arrange != arrangeStacked {
		t.Fatalf("first %v, second %v, arrangement %q", h.m.log.opened, h.m.second.opened, h.m.arrange)
	}
	if h.m.log.spec.App != "app.one" || h.m.second.spec.App != "app.two" {
		t.Errorf("first %q, second %q", h.m.log.spec.App, h.m.second.spec.App)
	}
	if h.m.log.filter != "other" || h.m.log.wrap || h.m.log.levelChosen {
		t.Errorf("first: filter %q wrap %v chosen %v", h.m.log.filter, h.m.log.wrap, h.m.log.levelChosen)
	}
	if h.m.second.filter != "needle" || !h.m.second.wrap || !h.m.second.levelChosen {
		t.Errorf("second: filter %q wrap %v chosen %v", h.m.second.filter, h.m.second.wrap, h.m.second.levelChosen)
	}
	if h.m.focus != paneLogs {
		t.Errorf("focus = %v, want the first log", h.m.focus)
	}
}

func TestAStateFileFromBeforeTwoLogsStillRestores(t *testing.T) {
	memory := memoryIn(t)
	err := memory.Store.Update(func(f *state.File) {
		f.Views = map[string]state.View{memory.Folder: {
			Device: pixel.Key(), Filter: "old", Wrap: true,
			Log: &state.Log{Device: pixel.Key(), Mode: logs.Logcat, App: "app.one"},
		}}
	})
	if err != nil {
		t.Fatal(err)
	}
	h := startWith(t, memory, pixel, wifiPhon)
	h.wantCalls("logs Pixel_9a mode=logcat app=app.one", 1)
	if h.m.second.opened || h.m.arrange != arrangeSide || h.m.log.filter != "old" || !h.m.log.wrap {
		t.Errorf("second %v, arrangement %q, filter %q, wrap %v", h.m.second.opened, h.m.arrange, h.m.log.filter, h.m.log.wrap)
	}
}

func TestASecondLogWhoseFirstDeviceStoppedMovesIntoTheFirstPane(t *testing.T) {
	memory := memoryIn(t)
	first := startWith(t, memory, pixel, wifiPhon)
	first.openLogs(pixel, "app.one")
	first.openSecond(wifiPhon, "app.two")
	first.press("q")

	stopped := pixel
	stopped.State = device.Shutdown
	h := startWith(t, memory, stopped, wifiPhon)
	h.wantCalls("logs Pixel_9a", 0)
	h.wantCalls("logs 192.168.1.9:5555 mode=logcat app=app.two", 1)
	if !h.m.log.opened || h.m.second.opened || h.m.log.spec.Device.Key() != wifiPhon.Key() {
		t.Errorf("first %v, second %v", h.m.log.opened, h.m.second.opened)
	}
}

// A log that launches an app is never started on its own, since that would launch the app again.
// It still comes back as a pane, waiting for r, so the two logs the user had stay as they were.
func TestTwoLogsComeBackEvenWhenOneLaunchesAnApp(t *testing.T) {
	memory := memoryIn(t)
	first := startWith(t, memory, pixel, usbPhone)
	first.openLogs(pixel, "app.one")
	first.press("esc")
	first.selectDevice(usbPhone)
	// The first of the phone's two logs launches an app.
	first.press("l", "enter", "ctrl+u")
	first.typeText("app.two")
	first.press("enter")
	first.wantCalls("logs UDID-1 mode=console app=app.two", 1)
	first.press("q")

	h := startWith(t, memory, pixel, usbPhone)
	h.wantCalls("logs Pixel_9a mode=logcat app=app.one", 1)
	h.wantCalls("logs UDID-1", 0)
	if !h.m.log.opened || !h.m.second.opened || !h.m.second.parked {
		t.Fatalf("first %v, second %v, parked %v", h.m.log.opened, h.m.second.opened, h.m.second.parked)
	}
	h.wantScreen("Logs 1/2: Pixel 9a", "Logs 2/2: Test iPhone", "Press r to launch it", "is waiting: press r")

	// Leaving again without starting it keeps it for the time after.
	h.press("q")
	again := startWith(t, memory, pixel, usbPhone)
	again.wantCalls("logs UDID-1", 0)
	if !again.m.second.parked {
		t.Fatal("the log that was waiting to be launched was forgotten")
	}

	// r in that pane launches it.
	again.press("tab")
	if again.m.focus != paneLogs2 {
		t.Fatalf("focus = %v", again.m.focus)
	}
	again.press("r")
	again.wantCalls("logs UDID-1 mode=console app=app.two", 1)
	if again.m.second.parked {
		t.Error("still waiting after it was launched")
	}
	again.wantNoScreen("Press r to launch it")
	again.wantNoScreen("is waiting: press r")
}

func TestAConsoleLogInTheFirstPaneComesBackToo(t *testing.T) {
	memory := memoryIn(t)
	first := startWith(t, memory, usbPhone, pixel)
	first.selectDevice(usbPhone)
	first.press("l", "enter", "ctrl+u")
	first.typeText("app.two")
	first.press("enter")
	first.openSecond(pixel, "app.one")
	first.press("q")

	h := startWith(t, memory, usbPhone, pixel)
	h.wantCalls("logs UDID-1", 0)
	h.wantCalls("logs Pixel_9a mode=logcat app=app.one", 1)
	if !h.m.log.parked || h.m.log.spec.Device.Key() != usbPhone.Key() || h.m.second.spec.Device.Key() != pixel.Key() {
		t.Errorf("first parked %v on %s, second on %s", h.m.log.parked, h.m.log.spec.Device.Name, h.m.second.spec.Device.Name)
	}
}

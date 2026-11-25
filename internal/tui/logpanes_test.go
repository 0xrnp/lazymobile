package tui

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/logs"
	"github.com/0xrnp/lazymobile/internal/state"
)

// openLog opens a log of d, typing app. It goes in a pane of its own after the ones that are open.
// The focus goes back to the devices first, since l is something else while a log has it.
func (h *harness) openLog(d device.Device, app string) {
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

// logsOpen is a dashboard with n logs open, of the emulator and the Wi-Fi phone in turn: the app
// ids are app.1, app.2 and so on, so each pane has its own title.
func logsOpen(t *testing.T, n int) *harness {
	t.Helper()
	h := start(t, pixel, wifiPhon)
	for i := 1; i <= n; i++ {
		d := []device.Device{pixel, wifiPhon}[(i-1)%2]
		if i == 1 {
			h.openLogs(d, "app.1")
			continue
		}
		h.openLog(d, fmt.Sprintf("app.%d", i))
	}
	if got := h.m.logCount(); got != n {
		t.Fatalf("%d logs are open, want %d", got, n)
	}
	return h
}

func twoLogs(t *testing.T) *harness { return logsOpen(t, 2) }

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

func (h *harness) wantFocus(index int) {
	h.t.Helper()
	if h.m.focus != paneLogs || h.m.logIdx != index {
		h.t.Fatalf("focus is %v, log %d; want log %d", h.m.focus, h.m.logIdx, index)
	}
}

func TestLOpensAnotherLogBesideTheOnesThatAreOpenAndTheTitlesSayWhichIsWhich(t *testing.T) {
	h := start(t, pixel, wifiPhon)
	h.openLogs(pixel, "app.1")
	// One log is just "Logs".
	h.wantScreen("Logs: Pixel 9a")
	h.wantNoScreen("1/2")

	h.openLog(wifiPhon, "app.2")
	h.wantScreen("Logs 1/2: Pixel 9a", "Logs 2/2: Pixel 8")
	h.wantFocus(1)

	h.openLog(pixel, "app.3")
	h.wantScreen("Logs 1/3: Pixel 9a", "Logs 2/3: Pixel 8", "Logs 3/3: Pixel 9a")
	h.wantFocus(2)
	if h.m.logCount() != 3 {
		t.Errorf("%d logs", h.m.logCount())
	}
}

func TestEachLogKeepsItsOwnLines(t *testing.T) {
	h := logsOpen(t, 3)
	for i := range 3 {
		h.emitTo(i, logs.Line{Text: fmt.Sprintf("from-log-%d", i+1), Level: logs.Info})
	}
	h.wantScreen("from-log-1", "from-log-2", "from-log-3")
	for i, v := range h.m.logPanes {
		if len(v.all) != 1 {
			t.Errorf("log %d has %d lines, want its own 1", i+1, len(v.all))
		}
	}
}

func TestThreeLogsFillTheAreaInARowOfTwoAndOneThatIsWide(t *testing.T) {
	h := logsOpen(t, 3)
	row1, col1 := screenAt(t, h, "Logs 1/3")
	row2, col2 := screenAt(t, h, "Logs 2/3")
	row3, col3 := screenAt(t, h, "Logs 3/3")
	if row1 != row2 || col2 <= col1 || row3 <= row1 || col3 != col1 {
		t.Errorf("first (%d,%d), second (%d,%d), third (%d,%d)", row1, col1, row2, col2, row3, col3)
	}
	// The last row has one log, which has the whole width: its border reaches the screen's edge.
	boxes := h.m.layout().logs
	if boxes[2].width != boxes[0].width+boxes[1].width {
		t.Errorf("widths %d, %d and %d: the third should span both", boxes[0].width, boxes[1].width, boxes[2].width)
	}
}

func TestFourLogsAreAGridOfTwoByTwo(t *testing.T) {
	h := logsOpen(t, 4)
	boxes := h.m.layout().logs
	if boxes[0].y != boxes[1].y || boxes[2].y != boxes[3].y || boxes[2].y <= boxes[0].y || boxes[0].x != boxes[2].x || boxes[1].x != boxes[3].x {
		t.Errorf("boxes %+v", boxes)
	}
	h.wantScreen("Logs 1/4", "Logs 2/4", "Logs 3/4", "Logs 4/4")
}

func TestSixLogsAreAGridOfTwoColumnsAndThreeRows(t *testing.T) {
	h := logsOpen(t, 6)
	boxes := h.m.layout().logs
	if boxes[0].y != boxes[1].y || boxes[2].y != boxes[3].y || boxes[4].y != boxes[5].y || boxes[4].y <= boxes[2].y {
		t.Errorf("boxes %+v", boxes)
	}
	for i := 1; i <= 6; i++ {
		h.wantScreen(fmt.Sprintf("Logs %d/6", i))
	}
}

func TestManyLogsThatCannotAllFitShowTheOneInFocusAndSayHowToReachTheOthers(t *testing.T) {
	h := logsOpen(t, 9)
	// At 120 by 30 nine logs would be too small, so the one in focus has the area.
	h.wantScreen("Logs 9/9", "a all logs")
	h.wantNoScreen("Logs 1/9")
	h.press("1")
	h.wantFocus(0)
	h.wantScreen("Logs 1/9")
	h.wantNoScreen("Logs 9/9")

	// A wide, tall screen has room for all of them.
	h.m.Update(tea.WindowSizeMsg{Width: 260, Height: 50})
	for i := 1; i <= 9; i++ {
		h.wantScreen(fmt.Sprintf("Logs %d/9", i))
	}
}

func TestTheLayoutCyclesThroughSideStackedAndOneAtATime(t *testing.T) {
	h := twoLogs(t)
	row1, col1 := screenAt(t, h, "Logs 1/2: Pixel 9a")
	row2, col2 := screenAt(t, h, "Logs 2/2: Pixel 8")
	if row1 != row2 || col2 <= col1 {
		t.Errorf("side by side: first at (%d,%d), second at (%d,%d)", row1, col1, row2, col2)
	}

	h.press("v")
	h.wantScreen("Logs stacked")
	row1, col1 = screenAt(t, h, "Logs 1/2: Pixel 9a")
	row2, col2 = screenAt(t, h, "Logs 2/2: Pixel 8")
	if row2 <= row1 || col1 != col2 {
		t.Errorf("stacked: first at (%d,%d), second at (%d,%d)", row1, col1, row2, col2)
	}

	h.press("v")
	h.wantScreen("One log at a time", "Devices", "Logs 2/2: Pixel 8")
	h.wantNoScreen("Logs 1/2: Pixel 9a")

	h.press("v")
	h.wantScreen("Logs side by side")
}

func TestVBeforeASecondLogSaysHowToOpenOne(t *testing.T) {
	h := start(t, pixel)
	h.press("v")
	h.wantScreen("Press l on another device to open another log")
}

func TestEachLogPaneKeepsItsOwnFilterLevelAndWrap(t *testing.T) {
	h := logsOpen(t, 3)
	h.press("/")
	h.typeText("needle")
	h.press("enter")
	if h.m.logPanes[2].filter != "needle" || h.m.logPanes[0].filter != "" || h.m.logPanes[1].filter != "" {
		t.Errorf("filters: %q %q %q", h.m.logPanes[0].filter, h.m.logPanes[1].filter, h.m.logPanes[2].filter)
	}
	before := h.m.logPanes[0].level
	h.press("L")
	if h.m.logPanes[0].level != before || !h.m.logPanes[2].levelChosen || h.m.logPanes[0].levelChosen {
		t.Errorf("the level of the third changed another one")
	}
	h.press("1")
	h.press("w")
	if !h.m.logPanes[0].wrap || h.m.logPanes[1].wrap || h.m.logPanes[2].wrap {
		t.Errorf("wrap: %v %v %v", h.m.logPanes[0].wrap, h.m.logPanes[1].wrap, h.m.logPanes[2].wrap)
	}
}

func TestTabVisitsEveryLogAndTheDevices(t *testing.T) {
	h := logsOpen(t, 3)
	var seen []string
	for range 5 {
		h.press("tab")
		if h.m.focus == paneLogs {
			seen = append(seen, fmt.Sprintf("log%d", h.m.logIdx+1))
		} else {
			seen = append(seen, "devices")
		}
	}
	want := []string{"devices", "log1", "log2", "log3", "devices"}
	if strings.Join(seen, " ") != strings.Join(want, " ") {
		t.Errorf("tab went %v, want %v", seen, want)
	}
	h.press("shift+tab")
	h.wantFocus(2)
}

func TestNumbersJumpToThatLog(t *testing.T) {
	h := logsOpen(t, 4)
	h.press("2")
	h.wantFocus(1)
	h.press("4")
	h.wantFocus(3)
	h.press("5")
	h.wantScreen("There is no log 5: 4 open")
	h.wantFocus(3)
	// Also from the devices.
	h.press("esc", "3")
	h.wantFocus(2)
}

func TestTheListShowsEveryLogAndSwitchesToTheOneChosen(t *testing.T) {
	h := logsOpen(t, 3)
	h.press("esc", "a")
	h.wantScreen("Logs (3 open)", "1  Pixel 9a · app.1", "2  Pixel 8 · app.2", "3  Pixel 9a · app.3")
	// The highlighted one is the log that last had the focus, which is the third.
	h.press("up", "enter")
	h.wantFocus(1)
	h.wantNoScreen("Logs (3 open)")
}

func TestTheListClosesLogsWithoutLeavingIt(t *testing.T) {
	h := logsOpen(t, 3)
	h.press("a", "x")
	// The highlighted one is the log in focus, which is the third.
	h.wantScreen("Logs (2 open)")
	h.wantNoScreen("app.3   ")
	if h.m.logCount() != 2 || !h.backend.sessions[2].isStopped() {
		t.Fatalf("%d logs are open, third stopped %v", h.m.logCount(), h.backend.sessions[2].isStopped())
	}
	h.press("up", "x")
	h.wantScreen("Logs (1 open)")
	h.press("x")
	h.wantNoScreen("Logs (")
	if h.m.logCount() != 0 || h.m.modal != nil {
		t.Errorf("%d logs open, modal %v", h.m.logCount(), h.m.modal != nil)
	}
	for i, s := range h.backend.sessions {
		if !s.isStopped() {
			t.Errorf("log %d is still running", i+1)
		}
	}
}

func TestTheListWithNoLogOpenSaysSo(t *testing.T) {
	h := start(t, pixel)
	h.press("a")
	h.wantScreen("No log is open")
	if h.m.modal != nil {
		t.Error("a window opened with nothing to list")
	}
}

func TestXClosesTheFocusedLogAndTheFocusStaysOnItsNeighbour(t *testing.T) {
	h := logsOpen(t, 4)
	h.press("2", "X")
	if h.m.logCount() != 3 {
		t.Fatalf("%d logs are open", h.m.logCount())
	}
	if !h.backend.sessions[1].isStopped() || h.backend.sessions[0].isStopped() || h.backend.sessions[2].isStopped() {
		t.Error("the wrong log was stopped")
	}
	// Log 3 moved up into the place of log 2, and has the focus.
	h.wantFocus(1)
	if h.m.logPanes[1].spec.App != "app.3" {
		t.Errorf("the second log is %q, want app.3", h.m.logPanes[1].spec.App)
	}
	// A moved stream still reaches its pane, and a closed one reaches nothing.
	h.emitTo(2, logs.Line{Text: "after-the-move", Level: logs.Info})
	h.emitTo(1, logs.Line{Text: "from-the-closed", Level: logs.Info})
	h.wantScreen("after-the-move")
	h.wantNoScreen("from-the-closed")

	// The last one closed hands the focus to the one before.
	h.press("3", "X")
	h.wantFocus(1)
	h.press("1", "X", "X")
	if h.m.logCount() != 0 || h.m.focus != paneDevices {
		t.Errorf("%d logs open, focus %v", h.m.logCount(), h.m.focus)
	}
	// The first pane stays, empty, and the next log opens in it.
	h.openLog(pixel, "app.9")
	if h.m.logCount() != 1 || len(h.m.logPanes) != 1 {
		t.Errorf("%d logs, %d panes", h.m.logCount(), len(h.m.logPanes))
	}
}

func TestQuittingStopsEveryLog(t *testing.T) {
	h := logsOpen(t, 4)
	h.run(h.m.quit())
	for i, s := range h.backend.sessions {
		if !s.isStopped() {
			t.Errorf("log %d is still running after quit", i+1)
		}
	}
}

func TestNarrowScreensShowOnlyTheLogInFocus(t *testing.T) {
	h := logsOpen(t, 3)
	h.m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	h.wantScreen("Logs 3/3")
	h.wantNoScreen("Logs 1/3")
	h.press("1")
	h.wantScreen("Logs 1/3")
	h.wantNoScreen("Logs 3/3")
}

func TestOneLogAtATimeKeepsTheDevicesAndTabGoesFromLogToLog(t *testing.T) {
	h := logsOpen(t, 3)
	for i := range 3 {
		h.emitTo(i, logs.Line{Text: fmt.Sprintf("from-log-%d", i+1), Level: logs.Info})
	}
	h.press("v", "v")
	h.wantScreen("tab next log", "esc devices", "Devices", "from-log-3")
	h.wantNoScreen("from-log-1")

	h.press("tab")
	h.wantFocus(0)
	h.wantScreen("Devices", "from-log-1")
	h.wantNoScreen("from-log-3")
	h.press("shift+tab")
	h.wantFocus(2)

	h.press("esc")
	if h.m.focus != paneDevices {
		t.Fatalf("focus = %v", h.m.focus)
	}
	h.wantScreen("Logs 3/3")
	h.wantNoScreen("Logs 1/3")
}

func TestZoomMakesTheLogInFocusFullScreenAndTabGoesFromLogToLog(t *testing.T) {
	h := logsOpen(t, 3)
	h.wantScreen("a all logs", "z full screen")
	h.press("z")
	h.wantScreen("Logs 3/3", "z back", "tab next log")
	h.wantNoScreen("Devices")
	h.press("tab")
	h.wantFocus(0)
	h.wantScreen("Logs 1/3")
	h.press("z")
	h.wantScreen("Devices", "Logs 1/3", "Logs 2/3", "Logs 3/3")
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
	h.openLogs(pixel, "app.1")
	h.wantScreen("z full screen")
	h.press("z")
	h.wantScreen("z back")
	h.wantNoScreen("tab next log")
}

func TestAnyNumberOfLogsFitsOrFallsBackWithoutOverflowingTheScreen(t *testing.T) {
	for n := 1; n <= 9; n++ {
		h := logsOpen(t, n)
		for _, arrange := range []arrangement{arrangeSide, arrangeStacked, arrangeOne} {
			for _, size := range [][2]int{{80, 24}, {100, 24}, {120, 30}, {160, 40}, {101, 12}, {260, 50}} {
				h.m.arrange = arrange
				h.m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				lines := strings.Split(h.screen(), "\n")
				if len(lines) != size[1] {
					t.Errorf("%d logs, %q, %v: %d rows", n, arrange, size, len(lines))
				}
				for _, line := range lines {
					if w := utf8.RuneCountInString(line); w > size[0] {
						t.Errorf("%d logs, %q, %v: a line is %d wide: %q", n, arrange, size, w, line)
					}
				}
			}
		}
	}
}

func TestClickingALogFocusesIt(t *testing.T) {
	h := logsOpen(t, 3)
	for i := 1; i <= 3; i++ {
		row, col := screenAt(t, h, fmt.Sprintf("Logs %d/3", i))
		h.m.Update(tea.MouseClickMsg{X: col + 3, Y: row + 2, Button: tea.MouseLeft})
		h.wantFocus(i - 1)
	}
}

func TestTheWheelScrollsTheLogUnderIt(t *testing.T) {
	h := logsOpen(t, 3)
	var lines []logs.Line
	for i := range 200 {
		lines = append(lines, logs.Line{Text: fmt.Sprintf("line-%d", i), Level: logs.Info})
	}
	for i := range 3 {
		h.emitTo(i, lines...)
	}
	row, col := screenAt(t, h, "Logs 2/3")
	h.m.Update(tea.MouseWheelMsg{X: col + 3, Y: row + 2, Button: tea.MouseWheelUp})
	if h.m.logPanes[0].follow == false || h.m.logPanes[1].follow || h.m.logPanes[2].follow == false {
		t.Errorf("follow: %v %v %v", h.m.logPanes[0].follow, h.m.logPanes[1].follow, h.m.logPanes[2].follow)
	}
}

func TestManyLogsComeBackOnTheNextStartWithTheirOwnSettings(t *testing.T) {
	memory := memoryIn(t)
	first := startWith(t, memory, pixel, wifiPhon)
	first.openLogs(pixel, "app.1")
	first.openLog(wifiPhon, "app.2")
	first.openLog(pixel, "app.3")
	first.press("1", "w")
	first.press("3", "L")
	first.m.logPanes[1].filter = "needle"
	first.m.logPanes[0].filter = "other"
	first.press("v")
	first.press("q")

	h := startWith(t, memory, pixel, wifiPhon)
	h.wantCalls("logs Pixel_9a mode=logcat app=app.1", 1)
	h.wantCalls("logs 192.168.1.9:5555 mode=logcat app=app.2", 1)
	h.wantCalls("logs Pixel_9a mode=logcat app=app.3", 1)
	if h.m.logCount() != 3 || h.m.arrange != arrangeStacked {
		t.Fatalf("%d logs, arrangement %q", h.m.logCount(), h.m.arrange)
	}
	for i, want := range []string{"app.1", "app.2", "app.3"} {
		if got := h.m.logPanes[i].spec.App; got != want {
			t.Errorf("log %d is %q, want %q", i+1, got, want)
		}
	}
	if v := h.m.logPanes[0]; v.filter != "other" || !v.wrap || v.levelChosen {
		t.Errorf("first: filter %q wrap %v chosen %v", v.filter, v.wrap, v.levelChosen)
	}
	if v := h.m.logPanes[1]; v.filter != "needle" || v.wrap || v.levelChosen {
		t.Errorf("second: filter %q wrap %v chosen %v", v.filter, v.wrap, v.levelChosen)
	}
	if v := h.m.logPanes[2]; v.filter != "" || v.wrap || !v.levelChosen {
		t.Errorf("third: filter %q wrap %v chosen %v", v.filter, v.wrap, v.levelChosen)
	}
	h.wantFocus(0)
}

func TestAStateFileFromBeforeSeveralLogsStillRestores(t *testing.T) {
	memory := memoryIn(t)
	err := memory.Store.Update(func(f *state.File) {
		f.Views = map[string]state.View{memory.Folder: {
			Device: pixel.Key(), Filter: "old", Wrap: true,
			Log: &state.Log{Device: pixel.Key(), Mode: logs.Logcat, App: "app.1"},
		}}
	})
	if err != nil {
		t.Fatal(err)
	}
	h := startWith(t, memory, pixel, wifiPhon)
	h.wantCalls("logs Pixel_9a mode=logcat app=app.1", 1)
	if h.m.logCount() != 1 || h.m.arrange != arrangeSide || h.m.logPanes[0].filter != "old" || !h.m.logPanes[0].wrap {
		t.Errorf("%d logs, arrangement %q, filter %q, wrap %v", h.m.logCount(), h.m.arrange, h.m.logPanes[0].filter, h.m.logPanes[0].wrap)
	}
}

func TestLogsWhoseDeviceStoppedAreLeftOutAndTheRestMoveUp(t *testing.T) {
	memory := memoryIn(t)
	first := startWith(t, memory, pixel, wifiPhon)
	first.openLogs(pixel, "app.1")
	first.openLog(wifiPhon, "app.2")
	first.openLog(pixel, "app.3")
	first.press("q")

	stopped := pixel
	stopped.State = device.Shutdown
	h := startWith(t, memory, stopped, wifiPhon)
	h.wantCalls("logs Pixel_9a", 0)
	h.wantCalls("logs 192.168.1.9:5555 mode=logcat app=app.2", 1)
	if h.m.logCount() != 1 || h.m.first().spec.Device.Key() != wifiPhon.Key() {
		t.Errorf("%d logs open", h.m.logCount())
	}
}

// A log that launches an app is never started on its own, since that would launch the app again.
// It still comes back as a pane, waiting for r, so the logs the user had stay as they were.
func TestLogsComeBackEvenWhenOneLaunchesAnApp(t *testing.T) {
	memory := memoryIn(t)
	first := startWith(t, memory, pixel, usbPhone)
	first.openLogs(pixel, "app.1")
	first.press("esc")
	first.selectDevice(usbPhone)
	// The first of the phone's two logs launches an app.
	first.press("l", "enter", "ctrl+u")
	first.typeText("app.2")
	first.press("enter")
	first.wantCalls("logs UDID-1 mode=console app=app.2", 1)
	first.press("q")

	h := startWith(t, memory, pixel, usbPhone)
	h.wantCalls("logs Pixel_9a mode=logcat app=app.1", 1)
	h.wantCalls("logs UDID-1", 0)
	if h.m.logCount() != 2 || !h.m.logPanes[1].parked {
		t.Fatalf("%d logs, parked %v", h.m.logCount(), h.m.logPanes[1].parked)
	}
	h.wantScreen("Logs 1/2: Pixel 9a", "Logs 2/2: Test iPhone", "Press r to launch it", "is waiting: press r")

	// Leaving again without starting it keeps it for the time after.
	h.press("q")
	again := startWith(t, memory, pixel, usbPhone)
	again.wantCalls("logs UDID-1", 0)
	if !again.m.logPanes[1].parked {
		t.Fatal("the log that was waiting to be launched was forgotten")
	}

	// r in that pane launches it.
	again.press("2", "r")
	again.wantCalls("logs UDID-1 mode=console app=app.2", 1)
	if again.m.logPanes[1].parked {
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
	first.typeText("app.2")
	first.press("enter")
	first.openLog(pixel, "app.1")
	first.press("q")

	h := startWith(t, memory, usbPhone, pixel)
	h.wantCalls("logs UDID-1", 0)
	h.wantCalls("logs Pixel_9a mode=logcat app=app.1", 1)
	if !h.m.logPanes[0].parked || h.m.logPanes[0].spec.Device.Key() != usbPhone.Key() || h.m.logPanes[1].spec.Device.Key() != pixel.Key() {
		t.Errorf("first parked %v on %s, second on %s", h.m.logPanes[0].parked, h.m.logPanes[0].spec.Device.Name, h.m.logPanes[1].spec.Device.Name)
	}
}

// checkPanes fails when the log panes, the focus, the log tools or the screen disagree.
func (h *harness) checkPanes(step string) {
	h.t.Helper()
	m := h.m
	if len(m.logPanes) == 0 {
		h.t.Fatalf("%s: there is no log pane", step)
	}
	for i, v := range m.logPanes[1:] {
		if !v.opened {
			h.t.Fatalf("%s: pane %d is empty", step, i+2)
		}
	}
	if m.focus == paneLogs && (m.logIdx < 0 || m.logIdx >= len(m.logPanes)) {
		h.t.Fatalf("%s: focus is on log %d of %d", step, m.logIdx+1, len(m.logPanes))
	}
	// Every running log tool belongs to a pane. The other way round does not hold in a test: a
	// stopped tool's pane learns of it from a message that the harness does not deliver.
	running := 0
	for _, s := range h.backend.sessions {
		if !s.isStopped() {
			running++
		}
	}
	held := 0
	for _, v := range m.logPanes {
		if v.session != nil {
			held++
		}
	}
	if running > held {
		h.t.Fatalf("%s: %d log tools are running for %d panes with a stream", step, running, held)
	}
	lines := strings.Split(h.screen(), "\n")
	if len(lines) != m.height && m.width >= minWidth && m.height >= minHeight {
		h.t.Fatalf("%s: %d rows on a %d row screen", step, len(lines), m.height)
	}
	for _, line := range lines {
		if w := utf8.RuneCountInString(line); w > m.width {
			h.t.Fatalf("%s: a line is %d wide on a %d wide screen: %q", step, w, m.width, line)
		}
	}
}

func TestRandomUseKeepsTheLogsConsistent(t *testing.T) {
	keys := []string{"esc", "tab", "shift+tab", "v", "z", "X", "x", "r", "j", "k", "G", "g", "w", "p", "c", "+", "-", "L", "/", "S", "enter", "y", "?", "a", "1", "2", "3", "4", "9"}
	sizes := [][2]int{{80, 24}, {100, 24}, {120, 30}, {160, 40}, {101, 12}, {260, 50}}
	devices := []device.Device{pixel, wifiPhon}
	for seed := range 3 {
		rng := rand.New(rand.NewPCG(uint64(seed), 7))
		h := start(t, pixel, wifiPhon)
		for step := range 45 {
			name := fmt.Sprintf("seed %d step %d", seed, step)
			switch n := rng.IntN(10); {
			case n < 3:
				h.openLog(devices[rng.IntN(len(devices))], fmt.Sprintf("app.%d", rng.IntN(5)))
			case n == 3:
				size := sizes[rng.IntN(len(sizes))]
				h.m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			default:
				h.press(keys[rng.IntN(len(keys))])
			}
			// A window left open by a random key would swallow the next ones.
			for h.m.modal != nil {
				h.press("esc")
			}
			if h.m.focusOnLog() && step%7 == 0 && len(h.backend.handlers) > 0 {
				h.emitTo(rng.IntN(len(h.backend.handlers)), logs.Line{Text: "line", Level: logs.Info})
			}
			h.checkPanes(name)
		}
		h.run(h.m.quit())
		for i, s := range h.backend.sessions {
			if !s.isStopped() {
				t.Fatalf("seed %d: log %d is still running after quit", seed, i)
			}
		}
	}
}

func TestFilterLeavesOutAndMatchesRegularExpressions(t *testing.T) {
	h := start(t, pixel)
	h.openLogs(pixel, "")
	h.emit(
		logs.Line{Text: "net request id=41 ok"},
		logs.Line{Text: "net request id=42 failed"},
		logs.Line{Text: "net heartbeat id=43"},
		logs.Line{Text: "ui draw"},
	)

	h.press("/")
	h.typeText("net -heartbeat")
	h.press("enter")
	h.wantScreen("id=41 ok", "id=42 failed", "2 shown, 2 hidden")
	h.wantNoScreen("id=43", "ui draw")

	h.press("/", "ctrl+u")
	h.typeText(`/id=4[12]/ -ok`)
	h.press("enter")
	h.wantScreen("id=42 failed", "1 shown, 3 hidden")
	h.wantNoScreen("id=41 ok")
}

func TestFilterWithABadExpressionIsNotAccepted(t *testing.T) {
	h := start(t, pixel)
	h.openLogs(pixel, "")
	h.emit(logs.Line{Text: "one"}, logs.Line{Text: "two"})
	h.press("/")
	h.typeText("/(/")
	h.press("enter")
	h.wantScreen("not valid")
	if h.m.logPanes[0].filter != "" {
		t.Errorf("a filter that does not parse was applied: %q", h.m.logPanes[0].filter)
	}
	h.wantScreen("one", "two")
}

func TestASavedFilterThatDoesNotParseIsTakenAsText(t *testing.T) {
	v := &logView{follow: true, filter: "/open"}
	v.add([]logs.Line{{Text: "see /open here"}, {Text: "other"}})
	if len(v.shown) != 1 || v.shown[0].Text != "see /open here" {
		t.Errorf("shown = %v", v.shown)
	}
}

func TestSaveAsJSONWritesTheShownLinesWithTheLogsJSONFields(t *testing.T) {
	h := start(t, pixel)
	h.openLogs(pixel, "")
	h.emit(
		logs.Line{Text: "I hello", Level: logs.Info, PID: "42"},
		logs.Line{Text: "E boom", Level: logs.Error, Crash: true},
		logs.Line{Text: "I hidden by the filter", Level: logs.Info},
	)
	h.press("/")
	h.typeText("-hidden")
	h.press("enter")

	path := filepath.Join(t.TempDir(), "saved.json")
	// The filter hides a line, so which lines to save is asked first: what is shown is the first.
	h.press("S")
	h.wantScreen("Save which lines?", "Everything received, filters off (3 lines)")
	h.press("enter", "ctrl+u")
	h.typeText(path)
	h.press("enter")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got []logs.Line
	if err := json.Unmarshal(content, &got); err != nil {
		t.Fatalf("not JSON: %v: %s", err, content)
	}
	want := []logs.Line{{Text: "I hello", Level: logs.Info, PID: "42"}, {Text: "E boom", Level: logs.Error, Crash: true}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("saved %+v, want %+v", got, want)
	}
	h.wantScreen("Saved 2 lines")
}

func TestFilterPresetsAreSavedAppliedAndForgottenPerApp(t *testing.T) {
	h := startWith(t, memoryIn(t), pixel)
	h.openLogs(pixel, "com.example.app")
	h.emit(logs.Line{Text: "net ok"}, logs.Line{Text: "ui draw"})

	h.press("P", "enter")
	h.wantScreen("Nothing to save")
	h.press("esc")

	h.press("/")
	h.typeText("net -heartbeat")
	h.press("enter", "P", "enter")
	h.wantScreen("Saved the filter for com.example.app")

	h.press("/", "ctrl+u", "enter")
	h.wantScreen("net ok", "ui draw")
	h.press("P")
	h.wantScreen("Filters for com.example.app", "net -heartbeat", "Save the current filter")
	h.press("enter")
	h.wantScreen("1 shown, 1 hidden")
	if got := h.m.logPanes[0].filter; got != "net -heartbeat" {
		t.Errorf("filter = %q", got)
	}

	// Another app has none of them.
	h.m.logPanes[0].spec.App = "com.example.other"
	h.press("P")
	h.wantScreen("Filters for com.example.other")
	if items := h.m.modal.items; len(items) != 1 || items[0] != "Save the current filter" {
		t.Errorf("another app's rows = %v, want only the save row", items)
	}
	h.press("esc")

	h.m.logPanes[0].spec.App = "com.example.app"
	h.press("P", "x")
	if items := h.m.modal.items; len(items) != 1 {
		t.Errorf("rows after forgetting = %v", items)
	}
	if got := h.m.memory.Store.Load().Presets; len(got) != 0 {
		t.Errorf("presets after forgetting = %v", got)
	}
}

func TestSavingOffersTheErrorsAndTheLinesFromTheSelectedOne(t *testing.T) {
	h := start(t, pixel)
	h.openLogs(pixel, "com.example.app")
	h.emit(
		logs.Line{Text: "I one", Level: logs.Info}, logs.Line{Text: "E two", Level: logs.Error},
		logs.Line{Text: "I three", Level: logs.Info}, logs.Line{Text: "I four", Level: logs.Info},
	)
	dir := t.TempDir()
	save := func(choice int, name string) string {
		h.press("S")
		for range choice {
			h.press("down")
		}
		h.press("enter", "ctrl+u")
		h.typeText(filepath.Join(dir, name))
		h.press("enter")
		content, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(content)
	}
	// All of it is shown and nothing else is selected, so the first choice is errors and crashes.
	h.press("S")
	h.wantScreen("Errors and crashes only (1 lines)")
	h.wantNoScreen("Everything received")
	h.press("esc")
	if got := save(1, "errors.csv"); !strings.Contains(got, "1,error,,false,E two") || strings.Contains(got, "one") {
		t.Errorf("errors saved %q", got)
	}
	// Selecting the second line (leaving follow) offers the lines from there.
	h.press("g", "j")
	h.press("S")
	h.wantScreen("From the selected line to the newest (3 lines)")
	h.press("esc")
	if got := save(2, "from.md"); !strings.Contains(got, "E two") || !strings.Contains(got, "I four") || strings.Contains(got, "I one") || !strings.Contains(got, "### Log of com.example.app on Pixel 9a") {
		t.Errorf("from saved %q", got)
	}
	// Saving over a file that is there is refused, and the file is kept.
	h.press("S", "enter", "ctrl+u")
	h.typeText(filepath.Join(dir, "from.md"))
	h.press("enter")
	h.wantScreen("already exists")
}

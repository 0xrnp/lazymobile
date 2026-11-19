package tui

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/state"
)

// fakeTool is a tool in the run view: it records what it is sent and shows a fixed screen.
type fakeTool struct {
	mu         sync.Mutex
	keys       []string
	pasted     []string
	cols, rows int
	resized    int
	back       int
	scrollback int
	stopped    bool
	changed    chan struct{}
	done       chan struct{}
	err        error
}

func newFakeTool(cols, rows int) *fakeTool {
	return &fakeTool{cols: cols, rows: rows, scrollback: 100, changed: make(chan struct{}, 1), done: make(chan struct{})}
}

func (f *fakeTool) Resize(cols, rows int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cols, f.rows = cols, rows
	f.resized++
}

func (f *fakeTool) SendKey(k uv.KeyEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys = append(f.keys, k.String())
}

func (f *fakeTool) Paste(text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pasted = append(f.pasted, text)
}

func (f *fakeTool) Lines(back int) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.back = back
	lines := []string{"Flutter run key commands.", "r Hot reload."}
	if back > 0 {
		lines = []string{"earlier output"}
	}
	for len(lines) < f.rows {
		lines = append(lines, "")
	}
	return lines
}

func (f *fakeTool) ScrollbackLen() int       { return f.scrollback }
func (f *fakeTool) Cursor() (int, int, bool) { return 0, 2, true }
func (f *fakeTool) Changed() <-chan struct{} { return f.changed }
func (f *fakeTool) Done() <-chan struct{}    { return f.done }
func (f *fakeTool) Err() error               { return f.err }
func (f *fakeTool) Wait()                    { <-f.done }

func (f *fakeTool) Stop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.stopped {
		f.stopped = true
		close(f.done)
	}
}

func (f *fakeTool) sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.keys...)
}

// runHarness is a dashboard opened by lazymobile run --here, running a fake flutter run on d.
type runHarness struct {
	*harness
	tools []*fakeTool
	// fail makes the next start fail.
	fail error
}

func startRun(t *testing.T, memory Memory, d device.Device, devices ...device.Device) *runHarness {
	t.Helper()
	r := &runHarness{}
	spec := RunSpec{Dir: "/work/app", Argv: []string{"flutter", "run", "-d", d.TargetID()}, Title: "flutter run", Device: d,
		Start: func(dir string, argv []string, cols, rows int) (ToolSession, error) {
			if r.fail != nil {
				err := r.fail
				r.fail = nil
				return nil, err
			}
			tool := newFakeTool(cols, rows)
			r.tools = append(r.tools, tool)
			return tool, nil
		}}
	backend := &fakeBackend{devices: devices, fail: map[string]error{}}
	h := &harness{t: t, backend: backend, clock: clockStart}
	h.m = New(context.Background(), backend, nil)
	h.m.memory = memory
	h.m.memory.Run = &spec
	h.m.attachRun(spec)
	h.m.now = func() time.Time { return h.clock }
	h.m.send = func(msg tea.Msg) { h.m.Update(msg) }
	r.harness = h
	h.m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	h.run(h.m.refresh())
	return r
}

func (r *runHarness) tool() *fakeTool {
	r.t.Helper()
	if len(r.tools) == 0 {
		r.t.Fatal("the tool was not started")
	}
	return r.tools[len(r.tools)-1]
}

// key sends a key the way the terminal reports it.
func (r *runHarness) key(msg tea.KeyPressMsg) {
	r.t.Helper()
	_, cmd := r.m.Update(msg)
	r.run(cmd)
}

var (
	ctrlBracket = tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl}
	ctrlO       = tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl}
	shiftPgUp   = tea.KeyPressMsg{Code: tea.KeyPgUp, Mod: tea.ModShift}
)

func TestTheRunViewStartsTheToolInItsPaneWithTheLogBeside(t *testing.T) {
	r := startRun(t, memoryIn(t), pixel, pixel)
	tool := r.tool()
	// The tool's terminal is the inside of its pane: 60% of 120 columns, less the borders, and the
	// screen less the borders, the note and the hints.
	if tool.cols != 70 || tool.rows != 26 {
		t.Errorf("the tool got %dx%d", tool.cols, tool.rows)
	}
	r.wantScreen("flutter run · Pixel 9a · running", "Flutter run key commands.", "The log of the app on Pixel 9a shows here.",
		"ctrl+o log", "ctrl+] dashboard")
	r.wantNoScreen("Devices")
}

func TestKeysGoToTheToolAndDoNothingInTheDashboard(t *testing.T) {
	r := startRun(t, memoryIn(t), pixel, pixel)
	// r is hot reload, ? is the tool's help, q and ctrl+c are the tool's to quit with.
	r.press("r", "?", "q", "ctrl+c", "enter", "j")
	if got := strings.Join(r.tool().sent(), " "); got != "r ? q ctrl+c enter j" {
		t.Errorf("the tool got %q", got)
	}
	if r.m.modal != nil || r.tool().stopped {
		t.Errorf("a key acted on the dashboard: modal %v, stopped %v", r.m.modal, r.tool().stopped)
	}
	r.press("ctrl+u")
	r.m.Update(tea.PasteMsg{Content: "hello"})
	if len(r.tool().pasted) != 1 || r.tool().pasted[0] != "hello" {
		t.Errorf("pasted %v", r.tool().pasted)
	}
}

func TestCtrlBracketSwitchesToTheDashboardAndBackWhileTheToolRuns(t *testing.T) {
	r := startRun(t, memoryIn(t), pixel, pixel)
	tool := r.tool()
	r.key(ctrlBracket)
	r.wantScreen("Devices", "Pixel 9a", "ctrl+] back to flutter run", "flutter run keeps running")
	// Keys are the dashboard's now: ? opens its menu, with the run's keys listed too.
	r.press("?")
	if r.m.modal == nil || r.m.modal.kind != modalKeys {
		t.Fatal("? did not open the keys menu")
	}
	r.wantScreen("Run")
	r.press("esc")
	if len(tool.sent()) != 0 {
		t.Errorf("the tool got %v while the dashboard showed", tool.sent())
	}
	// Coming back shows the same tool, not a new one, at the same size.
	r.key(ctrlBracket)
	r.wantScreen("flutter run · Pixel 9a · running")
	if len(r.tools) != 1 || tool.resized != 0 {
		t.Errorf("%d tools started, resized %d times", len(r.tools), tool.resized)
	}
	r.press("r")
	if got := tool.sent(); len(got) != 1 || got[0] != "r" {
		t.Errorf("after coming back the tool got %v", got)
	}
}

func TestCtrlOGivesTheKeysToTheLogAndLOpensTheFoldersApp(t *testing.T) {
	memory := memoryIn(t)
	if err := memory.Store.Update(func(f *state.File) {
		f.RememberFolderApp(memory.Folder, device.Android, "com.example.app")
	}); err != nil {
		t.Fatal(err)
	}
	r := startRun(t, memory, pixel, pixel)
	r.key(ctrlO)
	r.wantScreen("l pick the app", "esc back to flutter run")
	r.press("l")
	r.wantCalls("logs Pixel_9a mode=logcat app=com.example.app", 1)
	r.wantNoScreen("The log of the app on Pixel 9a shows here.")
	// The log's keys work: / opens the filter.
	r.press("/")
	if r.m.modal == nil || r.m.modal.kind != modalPrompt {
		t.Error("/ did not open the filter")
	}
	r.press("esc")
	r.press("esc")
	if !r.m.run.onTool {
		t.Error("esc did not give the keys back to the tool")
	}
	r.press("r")
	if got := r.tool().sent(); len(got) != 1 || got[0] != "r" {
		t.Errorf("the tool got %v", got)
	}
}

func TestTheRunViewFollowsTheRunAndOpensTheAppLogByItself(t *testing.T) {
	memory := memoryIn(t)
	if err := memory.Store.Update(func(f *state.File) {
		f.RememberFolderApp(memory.Folder, device.Android, "com.example.app")
		f.RememberRunStarted(memory.Folder, pixel.Key(), clockStart.Add(-time.Second))
	}); err != nil {
		t.Fatal(err)
	}
	r := startRun(t, memory, pixel, pixel, iphone)
	r.run(r.m.followRun())
	r.wantCalls("logs Pixel_9a mode=logcat app=com.example.app", 1)
	r.wantScreen("flutter run · Pixel 9a", "Logs: Pixel 9a")
	// The keys stay with the tool.
	if !r.m.run.onTool {
		t.Error("following the run took the keys from the tool")
	}
}

func TestWhenTheToolEndsItsScreenStaysAndEnterRunsItAgain(t *testing.T) {
	r := startRun(t, memoryIn(t), pixel, pixel)
	first := r.tool()
	first.err = exitErr(t, 1)
	r.m.Update(toolEndMsg{id: r.m.run.id, err: first.err})
	r.wantScreen("exited with code 1", "Flutter run key commands.", "enter run again", "q quit")
	// Keys are lazymobile's now: r is not sent to a tool that has ended.
	r.press("r")
	if len(first.sent()) != 0 {
		t.Errorf("an ended tool got %v", first.sent())
	}
	r.press("enter")
	if len(r.tools) != 2 {
		t.Fatalf("enter started %d tools", len(r.tools))
	}
	r.wantScreen("flutter run · Pixel 9a · running")
	// A message from the first run is not taken for the second.
	r.m.Update(toolEndMsg{id: r.m.run.id - 1})
	if r.m.run.ended {
		t.Error("the old run's end ended the new one")
	}
}

func exitErr(t *testing.T, code int) error {
	t.Helper()
	err := exec.Command("sh", "-c", "exit "+string(rune('0'+code))).Run()
	if err == nil {
		t.Fatal("no exit error")
	}
	return err
}

func TestAToolThatCannotStartSaysWhyAndEnterTriesAgain(t *testing.T) {
	rr := startRun(t, memoryIn(t), pixel, pixel)
	// Start again with a failure, as when the tool is not installed.
	rr.fail = errors.New(`exec: "flutter": executable file not found in $PATH`)
	rr.m.Update(toolEndMsg{id: rr.m.run.id})
	rr.press("enter")
	rr.wantScreen("could not start", "executable file not found", "enter tries again")
	rr.press("enter")
	rr.wantScreen("flutter run · Pixel 9a · running")
}

func TestQuittingWhileTheToolRunsAsksAndStopsIt(t *testing.T) {
	r := startRun(t, memoryIn(t), pixel, pixel)
	r.key(ctrlBracket)
	r.press("q")
	if r.m.modal == nil || r.m.modal.kind != modalConfirm {
		t.Fatal("q did not ask")
	}
	r.wantScreen("Stop flutter run and quit?")
	r.press("n")
	if r.tool().stopped {
		t.Fatal("no stopped the tool")
	}
	r.press("q")
	_, cmd := r.m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if cmd == nil {
		t.Fatal("yes did nothing")
	}
	if msg := cmd(); msg == nil {
		t.Error("quitting did not end the dashboard")
	}
	if !r.tool().stopped {
		t.Error("the tool was not stopped")
	}
}

func TestQuittingAfterTheToolEndedDoesNotAsk(t *testing.T) {
	r := startRun(t, memoryIn(t), pixel, pixel)
	r.m.Update(toolEndMsg{id: r.m.run.id})
	_, cmd := r.m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if r.m.modal != nil || cmd == nil {
		t.Errorf("modal %v, cmd %v", r.m.modal, cmd)
	}
}

func TestTheToolIsToldItsNewSizeAndANarrowScreenShowsOnePane(t *testing.T) {
	r := startRun(t, memoryIn(t), pixel, pixel)
	tool := r.tool()
	r.m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	if tool.cols != 94 || tool.rows != 36 {
		t.Errorf("after the resize the tool has %dx%d", tool.cols, tool.rows)
	}
	r.m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	r.wantScreen("flutter run · Pixel 9a")
	r.wantNoScreen("The log of the app")
	r.key(ctrlO)
	r.wantScreen("The log of the app on Pixel 9a shows here.")
	r.wantNoScreen("flutter run · Pixel 9a")
	// The tool keeps its size while its pane is hidden, so it does not redraw for nothing.
	if tool.cols != 78 {
		t.Errorf("hiding the pane resized the tool to %d columns", tool.cols)
	}
}

func TestScrollingBackAndTypingReturnsToTheEnd(t *testing.T) {
	r := startRun(t, memoryIn(t), pixel, pixel)
	r.key(shiftPgUp)
	r.wantScreen("earlier output", "13 rows back")
	if len(r.tool().sent()) != 0 {
		t.Errorf("shift+pgup went to the tool: %v", r.tool().sent())
	}
	r.press("r")
	r.wantScreen("Flutter run key commands.", "running")
	r.m.Update(tea.MouseWheelMsg{X: 10, Y: 5, Button: tea.MouseWheelUp})
	if r.m.run.back != 3 {
		t.Errorf("the wheel scrolled to %d", r.m.run.back)
	}
	// The wheel over the log does not scroll the tool.
	r.m.Update(tea.MouseWheelMsg{X: 100, Y: 5, Button: tea.MouseWheelUp})
	if r.m.run.back != 3 {
		t.Errorf("the wheel over the log scrolled the tool to %d", r.m.run.back)
	}
}

func TestAClickGivesTheKeysToThePaneClicked(t *testing.T) {
	r := startRun(t, memoryIn(t), pixel, pixel)
	r.m.Update(tea.MouseClickMsg{X: 100, Y: 5, Button: tea.MouseLeft})
	if r.m.run.onTool {
		t.Error("a click on the log left the keys with the tool")
	}
	r.m.Update(tea.MouseClickMsg{X: 10, Y: 5, Button: tea.MouseLeft})
	if !r.m.run.onTool {
		t.Error("a click on the tool did not give it the keys")
	}
}

func TestTheCursorIsTheTools(t *testing.T) {
	r := startRun(t, memoryIn(t), pixel, pixel)
	v := r.m.View()
	if v.Cursor == nil || v.Cursor.X != 1 || v.Cursor.Y != 3 {
		t.Errorf("cursor %+v", v.Cursor)
	}
	r.key(ctrlO)
	if r.m.View().Cursor != nil {
		t.Error("the cursor shows while the log has the keys")
	}
}

func TestAPlainDashboardHasNoRunKeys(t *testing.T) {
	h := start(t, pixel)
	_, cmd := h.m.Update(ctrlBracket)
	h.run(cmd)
	h.wantScreen("Devices")
	h.wantNoScreen("back to")
	if why := keyByID("run_switch").unavailable(h.m); why == "" {
		t.Error("the run keys are offered without a run")
	}
}

func TestTheFollowNoteInTheRunViewSaysHowToReachTheLog(t *testing.T) {
	memory := memoryIn(t)
	if err := memory.Store.Update(func(f *state.File) {
		f.RememberRunStarted(memory.Folder, pixel.Key(), clockStart.Add(-time.Second))
	}); err != nil {
		t.Fatal(err)
	}
	r := startRun(t, memory, pixel, pixel)
	r.run(r.m.followRun())
	r.wantScreen("Following the run on Pixel 9a: ctrl+o, then l, to pick the app")
}

// chooseInMenu opens the keys menu and runs the action with this id, as enter on it would.
func (r *runHarness) chooseInMenu(id string) {
	r.t.Helper()
	r.m.modal = r.m.keysModal()
	r.run(r.m.runKey(keyByID(id)))
}

func TestRunActionsFromTheKeysMenuActFromTheirPane(t *testing.T) {
	memory := memoryIn(t)
	if err := memory.Store.Update(func(f *state.File) {
		f.RememberFolderApp(memory.Folder, device.Android, "com.example.app")
	}); err != nil {
		t.Fatal(err)
	}
	r := startRun(t, memory, pixel, pixel)
	// Picking the app from the menu while the tool has the keys opens the log.
	r.chooseInMenu("run_pick")
	r.wantCalls("logs Pixel_9a mode=logcat app=com.example.app", 1)
	// Running the tool again from the menu while the log has the keys restarts the tool, rather
	// than opening the log's line window.
	r.m.Update(toolEndMsg{id: r.m.run.id})
	r.chooseInMenu("run_again")
	if len(r.tools) != 2 || r.m.modal != nil {
		t.Errorf("%d tools started, modal %v", len(r.tools), r.m.modal)
	}
	// A dashboard action shows the dashboard to act on it.
	r.chooseInMenu("new")
	if r.m.runShown() {
		t.Error("a dashboard action ran behind the run view")
	}
}

// paneWidths are the widths of the tool's pane and the log's pane on the screen now.
func (r *runHarness) paneWidths() (int, int) {
	l := r.m.layout()
	return l.tool.width, l.runLog.width
}

func TestTheLineBetweenThePanesMovesFromTheLogAndIsRemembered(t *testing.T) {
	memory := memoryIn(t)
	r := startRun(t, memory, pixel, pixel)
	if tool, log := r.paneWidths(); tool != 72 || log != 48 {
		t.Fatalf("panes %d and %d", tool, log)
	}
	// While the tool has the keys, + is the tool's.
	r.press("+")
	if got := r.tool().sent(); len(got) != 1 || got[0] != "+" {
		t.Errorf("the tool got %q", got)
	}
	r.key(ctrlO)
	r.press("+", "+")
	if tool, log := r.paneWidths(); tool != 64 || log != 56 {
		t.Errorf("after + +: panes %d and %d", tool, log)
	}
	// The tool is told the inside of its narrower pane.
	if tool := r.tool(); tool.cols != 62 {
		t.Errorf("the tool has %d columns", tool.cols)
	}
	r.press("-")
	if tool, log := r.paneWidths(); tool != 68 || log != 52 {
		t.Errorf("after -: panes %d and %d", tool, log)
	}
	// However far it moves, both panes stay usable.
	r.press("-", "-", "-", "-", "-", "-", "-", "-", "-")
	if _, log := r.paneWidths(); log != minPaneWidth {
		t.Errorf("the log is %d wide", log)
	}

	// The next run in the folder opens with the line where it was left.
	again := startRun(t, memory, pixel, pixel)
	if tool, log := again.paneWidths(); tool != 90 || log != 30 {
		t.Errorf("the next run: panes %d and %d", tool, log)
	}
}

func TestDraggingTheLineMovesItAndTellsTheToolOnce(t *testing.T) {
	r := startRun(t, memoryIn(t), pixel, pixel)
	resized := r.tool().resized
	// The line is the tool's right border and the log's left one.
	r.m.Update(tea.MouseClickMsg{X: 72, Y: 5, Button: tea.MouseLeft})
	for _, x := range []int{70, 65, 60} {
		r.m.Update(tea.MouseMotionMsg{X: x, Y: 5, Button: tea.MouseLeft})
	}
	if tool, log := r.paneWidths(); tool != 60 || log != 60 {
		t.Errorf("while dragging: panes %d and %d", tool, log)
	}
	if r.tool().resized != resized {
		t.Error("the tool was resized during the drag")
	}
	_, cmd := r.m.Update(tea.MouseReleaseMsg{X: 60, Y: 5, Button: tea.MouseLeft})
	r.run(cmd)
	if tool := r.tool(); tool.resized != resized+1 || tool.cols != 58 {
		t.Errorf("after the drag the tool was resized %d times to %d columns", tool.resized-resized, tool.cols)
	}
	// A click inside a pane still gives it the keys, and moves nothing.
	r.m.Update(tea.MouseClickMsg{X: 90, Y: 5, Button: tea.MouseLeft})
	r.m.Update(tea.MouseMotionMsg{X: 100, Y: 5, Button: tea.MouseLeft})
	if tool, _ := r.paneWidths(); tool != 60 || r.m.run.onTool {
		t.Errorf("a click in the log: tool pane %d, onTool %v", tool, r.m.run.onTool)
	}
}

func TestZShowsTheLogAtFullWidthWithoutResizingTheTool(t *testing.T) {
	r := startRun(t, memoryIn(t), pixel, pixel)
	cols := r.tool().cols
	r.key(ctrlO)
	r.press("z")
	if tool, log := r.paneWidths(); tool != 0 || log != 120 {
		t.Errorf("zoomed: panes %d and %d", tool, log)
	}
	if r.tool().cols != cols {
		t.Errorf("the tool was resized to %d columns", r.tool().cols)
	}
	// Giving the keys back to the tool shows both again.
	r.press("esc")
	if tool, log := r.paneWidths(); tool != 72 || log != 48 {
		t.Errorf("after esc: panes %d and %d", tool, log)
	}
}

func TestTheLineDoesNotMoveWhenOnlyOnePaneShows(t *testing.T) {
	r := startRun(t, memoryIn(t), pixel, pixel)
	r.m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	r.key(ctrlO)
	r.press("+")
	r.wantScreen("Only one pane is shown")
	if tool, log := r.paneWidths(); tool != 0 || log != 80 {
		t.Errorf("panes %d and %d", tool, log)
	}
}

func TestTheLineMovesWithTheMouseFromEitherBorderAndOnlyFromThePanes(t *testing.T) {
	r := startRun(t, memoryIn(t), pixel, pixel)
	// Taken by the tool's right border, the line moves one column for one column of the mouse.
	r.m.Update(tea.MouseClickMsg{X: 71, Y: 5, Button: tea.MouseLeft})
	r.m.Update(tea.MouseMotionMsg{X: 70, Y: 5, Button: tea.MouseLeft})
	if tool, _ := r.paneWidths(); tool != 71 {
		t.Errorf("the tool pane is %d wide", tool)
	}
	// The release never came, as when the button is let go outside the window. The next press
	// inside a pane does not drag the line.
	r.m.Update(tea.MouseClickMsg{X: 20, Y: 5, Button: tea.MouseLeft})
	r.m.Update(tea.MouseMotionMsg{X: 40, Y: 5, Button: tea.MouseLeft})
	if tool, _ := r.paneWidths(); tool != 71 {
		t.Errorf("a press in the tool moved the line: the tool pane is %d wide", tool)
	}
	// A press on the hint rows under the line does not take it.
	r.m.Update(tea.MouseClickMsg{X: 71, Y: 29, Button: tea.MouseLeft})
	r.m.Update(tea.MouseMotionMsg{X: 50, Y: 29, Button: tea.MouseLeft})
	if tool, _ := r.paneWidths(); tool != 71 {
		t.Errorf("a press on the hints moved the line: the tool pane is %d wide", tool)
	}
}

func TestRunningAgainFromTheMenuShowsBothPanes(t *testing.T) {
	r := startRun(t, memoryIn(t), pixel, pixel)
	r.key(ctrlO)
	r.press("z")
	r.m.Update(toolEndMsg{id: r.m.run.id})
	r.chooseInMenu("run_again")
	r.key(ctrlO)
	if tool, log := r.paneWidths(); tool != 72 || log != 48 {
		t.Errorf("panes %d and %d", tool, log)
	}
}

func TestTheHintsSayHowToResizeFromEitherPane(t *testing.T) {
	r := startRun(t, memoryIn(t), pixel, pixel)
	r.wantScreen("ctrl+o log, then + - to resize")
	r.key(ctrlO)
	r.wantScreen("+ - resize (or drag the line)", "z full width")
}

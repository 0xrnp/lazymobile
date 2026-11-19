package tui

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/logs"
	"github.com/0xrnp/lazymobile/internal/state"
)

var ctrlP = tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl}

func (h *harness) wantNote(part string) {
	h.t.Helper()
	if !strings.Contains(h.m.note, part) {
		h.t.Errorf("the note is %q, want it to say %q", h.m.note, part)
	}
}

// calls is every call the backend got, one per line.
func (h *harness) calls() string {
	h.backend.mu.Lock()
	defer h.backend.mu.Unlock()
	return strings.Join(h.backend.calls, "\n")
}

func TestQuickMenuOnTheDashboardCopiesAScreenshot(t *testing.T) {
	h := start(t, pixel)
	h.press("ctrl+p")
	h.wantScreen("Quick actions · Pixel 9a", "Screenshot: save it here and copy it", "Record the screen",
		"Copy the app's log for AI", "Save a bug report", "or its letter")
	h.press("s")
	if h.m.modal != nil {
		t.Error("the menu stayed open")
	}
	h.wantCalls("screenshot Pixel_9a ", 1)
	// The picture that was saved is the one copied.
	calls := h.calls()
	saved := strings.Fields(strings.SplitN(strings.SplitN(calls, "screenshot Pixel_9a ", 2)[1], "\n", 2)[0])[0]
	if !strings.Contains(calls, "copyfile "+saved) || !strings.HasSuffix(saved, ".png") {
		t.Errorf("saved %q, calls:\n%s", saved, calls)
	}
	h.wantNote("Copied a screenshot, saved as " + saved)
}

func TestQuickMenuNeedsADeviceAndOneThatRuns(t *testing.T) {
	h := start(t)
	h.press("ctrl+p")
	h.wantNote("Select a device first")
	h = start(t, device.Device{Name: "Off", ID: "Off", Platform: device.Android, Kind: device.Emulator, Source: "emulator", State: device.Shutdown})
	_, cmd := h.m.Update(ctrlP)
	h.run(cmd)
	h.press("s")
	h.wantNote("is not running")
	h.wantCalls("screenshot", 0)
}

func TestQuickMenuInTheRunViewActsOnTheRunsDeviceFromEitherPane(t *testing.T) {
	r := startRun(t, memoryIn(t), pixel, pixel)
	r.key(ctrlP)
	r.wantScreen("Quick actions · Pixel 9a")
	// ctrl+p is lazymobile's: the tool never saw it.
	if got := r.tool().sent(); len(got) != 0 {
		t.Errorf("the tool got %q", got)
	}
	r.press("esc")
	if r.m.modal != nil {
		t.Fatal("esc did not close the menu")
	}
	// From the log's side too, and a letter picks the item.
	r.key(ctrlO)
	r.key(ctrlP)
	r.press("s")
	r.wantCalls("screenshot Pixel_9a ", 1)
	r.wantCalls("copyfile ", 1)
	// Back on the tool, the keys are still the tool's.
	r.key(ctrlO)
	r.press("s")
	if got := r.tool().sent(); len(got) != 1 || got[0] != "s" {
		t.Errorf("the tool got %q", got)
	}
}

func TestQuickMenuHintIsOnTheFooterWithTheResizeKeys(t *testing.T) {
	r := startRun(t, memoryIn(t), pixel, pixel)
	r.wantScreen("ctrl+p quick")
	r.key(ctrlO)
	r.wantScreen("ctrl+p quick", "+ - resize")
	h := start(t, pixel)
	h.wantScreen("ctrl+p quick")
}

// startRecording opens the quick menu and presses r. The recording blocks until it is stopped, so
// its command runs on its own, and the message it ends with arrives on the returned channel.
func (h *harness) startRecording() <-chan tea.Msg {
	h.t.Helper()
	h.press("ctrl+p")
	_, cmd := h.m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if cmd == nil {
		h.t.Fatalf("r started nothing. The note: %q", h.m.note)
	}
	ended := make(chan tea.Msg, 1)
	go func() { ended <- cmd() }()
	deadline := time.Now().Add(2 * time.Second)
	for h.backend.called("record start") == 0 {
		if time.Now().After(deadline) {
			h.t.Fatal("the recording did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	return ended
}

func TestRecordingStartsStopsAndCopiesTheVideo(t *testing.T) {
	h := start(t, pixel)
	ended := h.startRecording()
	h.wantNote("Recording Pixel 9a")
	h.wantScreen("● REC 0:00")
	h.clock = h.clock.Add(75 * time.Second)
	h.wantScreen("● REC 1:15")
	if h.backend.called("copyfile") != 0 {
		t.Error("the video was copied before it was stopped")
	}

	// The menu says the next press stops it, and a second recording cannot start.
	h.press("ctrl+p")
	h.wantScreen("Stop recording Pixel 9a, save and copy the video")
	h.press("r")
	h.wantNote("Saving the recording of Pixel 9a")
	var msg tea.Msg
	select {
	case msg = <-ended:
	case <-time.After(2 * time.Second):
		t.Fatal("the recording did not end after it was stopped")
	}
	_, cmd := h.m.Update(msg)
	h.run(cmd)
	h.wantCalls("record stop", 1)
	h.wantCalls("record start", 1)
	if h.m.recording != nil {
		t.Error("the recording is still marked")
	}
	if got := h.calls(); !strings.Contains(got, "copyfile ") || !strings.Contains(got, ".mp4") {
		t.Errorf("the video was not copied:\n%s", got)
	}
	h.wantNote("Copied the video of Pixel 9a")
	h.wantScreen("ctrl+p quick")
	if strings.Contains(h.screen(), "REC") {
		t.Error("REC still shows")
	}
}

func TestARecordingThatFailsSaysSo(t *testing.T) {
	h := start(t, pixel)
	h.backend.fail["record start"] = errNoGo
	h.press("ctrl+p")
	_, cmd := h.m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	msg := cmd()
	_, _ = h.m.Update(msg)
	if h.m.recording != nil {
		t.Error("a recording that failed is still marked")
	}
	h.wantScreen("Could not record Pixel 9a")
}

func TestQuittingWhileRecordingAsksAndSavesTheVideoFirst(t *testing.T) {
	h := start(t, pixel)
	ended := h.startRecording()
	h.press("q")
	h.wantScreen("Stop the recording and quit?", "The recording of Pixel 9a is saved before lazymobile quits")
	_, quit := h.m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if quit == nil {
		t.Fatal("y did not quit")
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- quit() }()
	select {
	case msg := <-done:
		if _, ok := msg.(tea.QuitMsg); !ok {
			t.Errorf("the quit command returned %T", msg)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("lazymobile did not quit")
	}
	// The video was saved before it quit.
	h.wantCalls("record stop", 1)
	<-ended
}

func TestQuittingWithNothingRunningDoesNotAsk(t *testing.T) {
	h := start(t, pixel)
	_, cmd := h.m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if h.m.modal != nil || cmd == nil {
		t.Errorf("modal %v, command %v", h.m.modal, cmd)
	}
}

func TestCopyingTheLogForAIGivesTheNewestLinesWithTheirSource(t *testing.T) {
	h := start(t, pixel)
	h.openLogs(pixel, "com.example.app")
	var lines []logs.Line
	for i := 1; i <= 250; i++ {
		lines = append(lines, logs.Line{Text: "line " + strings.Repeat("x", 1) + itoa(i), Level: logs.Info})
	}
	lines = append(lines, logs.Line{Text: "FATAL EXCEPTION: main", Level: logs.Fatal, Crash: true})
	h.emit(lines...)
	h.press("esc")
	h.press("ctrl+p")
	h.press("c")
	h.wantCalls("copytext", 1)
	got := h.calls()
	for _, want := range []string{"The last 200 lines of the log of com.example.app on Pixel 9a", "FATAL EXCEPTION: main", "line x250"} {
		if !strings.Contains(got, want) {
			t.Errorf("the copied text lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "line x40|") || strings.Contains(got, "line x51|") {
		t.Error("lines older than the newest 200 were copied")
	}
	h.wantNote("Copied the last 200 lines of the log of com.example.app")
}

func TestCopyingTheLogForAIWithoutOneOpenSaysWhatToDo(t *testing.T) {
	h := start(t, pixel)
	h.press("ctrl+p")
	h.press("c")
	h.wantNote("No log of Pixel 9a is open")
	h.wantCalls("copytext", 0)
}

func TestCopyingTheLogForAIFallsBackToTheTerminalWithoutAClipboard(t *testing.T) {
	h := start(t, pixel)
	h.openLogs(pixel, "com.example.app")
	h.emit(logs.Line{Text: "boom", Level: logs.Error})
	h.press("esc")
	h.backend.fail["copytext"] = errNoGo
	h.press("ctrl+p")
	_, cmd := h.m.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	msg := cmd()
	_, next := h.m.Update(msg)
	if next == nil {
		t.Error("the terminal was not asked to copy")
	}
	h.wantNote("No clipboard tool here")
}

func TestARunsBugReportFromTheQuickMenuUsesTheFoldersApp(t *testing.T) {
	memory := memoryIn(t)
	if err := memory.Store.Update(func(f *state.File) {
		f.RememberFolderApp(memory.Folder, device.Android, "com.example.app")
	}); err != nil {
		t.Fatal(err)
	}
	r := startRun(t, memory, pixel, pixel)
	r.key(ctrlP)
	r.press("b")
	r.wantCalls("report Pixel_9a com.example.app ", 1)
}

func TestQuickMenuLetterInTheListDoesNotPickTheWrongItem(t *testing.T) {
	h := start(t, pixel)
	h.press("ctrl+p")
	h.press("x")
	if h.m.modal == nil {
		t.Error("a letter that is no item closed the menu")
	}
	h.press("down", "down", "enter")
	// The third item is Copy the app's log, which says no log is open.
	h.wantNote("No log of Pixel 9a is open")
}

var errNoGo = errors.New("it did not work")

func itoa(n int) string { return strconv.Itoa(n) }

func TestTheRunViewKeepsTheWayBackToTheDashboardAtEightyColumns(t *testing.T) {
	r := startRun(t, memoryIn(t), pixel, pixel)
	r.m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	for _, onLog := range []bool{false, true} {
		if onLog {
			r.key(ctrlO)
		}
		hints := r.screen()
		hints = hints[strings.LastIndex(strings.TrimRight(hints, "\n"), "\n")+1:]
		// q is the tool's while it has the keys, so only the log's side offers quit.
		if !strings.Contains(hints, "ctrl+] dashboard") || onLog && !strings.Contains(hints, "q quit") {
			t.Errorf("the hints with the log focused %v: %q", onLog, hints)
		}
	}
}

func TestTheRecordingMarkKeepsItsColourBesideAnError(t *testing.T) {
	h := start(t, pixel)
	h.m.recording = &recording{device: pixel, started: h.clock}
	h.m.setNote("it did not work", true)
	var note string
	for _, line := range strings.Split(h.m.render(), "\n") {
		if strings.Contains(line, "it did not work") {
			note = line
		}
	}
	// The mark comes first, in its own colour, and the error is red by itself after it.
	mark, rest, _ := strings.Cut(note, red.Render("it did not work"))
	if !strings.Contains(mark, "● REC") || rest == note {
		t.Errorf("the note line: %q", note)
	}
}

func TestCopyingTheLogForAIGivesWhatThePaneShows(t *testing.T) {
	h := start(t, pixel)
	h.openLogs(pixel, "com.example.app")
	h.emit(logs.Line{Text: "noise one", Level: logs.Info}, logs.Line{Text: "the needle line", Level: logs.Error},
		logs.Line{Text: "noise two", Level: logs.Info})
	h.m.logPanes[h.m.logIdx].filter = "needle"
	h.m.logPanes[h.m.logIdx].rebuild()
	h.press("esc")
	h.press("ctrl+p")
	h.press("c")
	got := h.calls()
	if !strings.Contains(got, "the needle line") || strings.Contains(got, "noise") {
		t.Errorf("the copied text:\n%s", got)
	}
	h.wantNote("Copied the last 1 lines")

	// A filter that hides everything says so, rather than copying nothing.
	h.m.logPanes[h.m.logIdx].filter = "nothing matches this"
	h.m.logPanes[h.m.logIdx].rebuild()
	h.backend.calls = nil
	h.press("ctrl+p")
	h.press("c")
	h.wantNote("hides every line")
	h.wantCalls("copytext", 0)
}

func TestQuickMenuActsOnTheDeviceOfTheFocusedLogNotTheCursor(t *testing.T) {
	other := pixel
	other.ID, other.Name, other.Serial = "Pixel_8", "Pixel 8", "emulator-5556"
	h := start(t, pixel, other)
	h.openLogs(other, "com.example.other")
	h.emit(logs.Line{Text: "other line", Level: logs.Error})
	// The cursor goes back to the Android emulator while the iOS log keeps the focus.
	h.selectDevice(pixel)
	h.m.focus, h.m.logIdx = paneLogs, 0
	h.press("ctrl+p")
	h.wantScreen("Quick actions · Pixel 8")
	h.press("c")
	if got := h.calls(); !strings.Contains(got, "other line") || !strings.Contains(got, "Pixel 8") {
		t.Errorf("the copied text:\n%s", got)
	}
}

func TestAnotherQuitWhileOneIsPendingDoesNothing(t *testing.T) {
	h := start(t, pixel)
	ended := h.startRecording()
	h.press("q")
	_, first := h.m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if first == nil {
		t.Fatal("the first quit did nothing")
	}
	if second := h.m.quitNow(); second != nil {
		t.Error("a second quit started everything again")
	}
	go first()
	select {
	case <-ended:
	case <-time.After(3 * time.Second):
		t.Fatal("the recording was not stopped")
	}
}

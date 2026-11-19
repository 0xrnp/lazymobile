package main

// These tests run the lazymobile binary in a terminal of its own, as a person would, and read its
// screen. Fake adb, emulator and xcrun programs stand in for the devices, so they need no simulator
// or emulator and never touch the ones on this machine.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/0xrnp/lazymobile/internal/vterm"
)

var (
	buildOnce sync.Once
	binary    string
	buildErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if binary != "" {
		_ = os.RemoveAll(filepath.Dir(binary))
	}
	os.Exit(code)
}

// built is the lazymobile binary, built once for all the tests.
func built(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("runs the built binary")
	}
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "lazymobile-e2e")
		if err != nil {
			buildErr = err
			return
		}
		binary = filepath.Join(dir, "lazymobile")
		out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput()
		if err != nil {
			buildErr = fmt.Errorf("%v: %s", err, out)
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return binary
}

// world is a home folder with a fake Android SDK that has one running emulator, Pixel_Test, a fake
// xcrun with no simulators, and a project folder whose run action is a small interactive tool.
type world struct {
	t       *testing.T
	home    string
	project string
	env     []string
}

const fakeAdb = `#!/bin/sh
[ "$1" = "-s" ] && shift 2
case "$*" in
"devices -l") printf 'List of devices attached\nemulator-5554 device product:sdk model:Pixel_Test device:emu transport_id:1\n' ;;
"shell getprop ro.build.version.release") echo 16 ;;
"shell getprop sys.boot_completed") echo 1 ;;
"shell pidof com.example.app") echo 100 ;;
"emu avd name") printf 'Pixel_Test\nOK\n' ;;
logcat*) while :; do echo "11-19 10:00:00.000  100  100 I App: hello from the app"; sleep 1; done ;;
"exec-out screencap -p") printf '\211PNG\r\n\032\nfake screen' ;;
shell\ screenrecord*)
  echo $$ > "$FAKE_HOME/recorder.pid"
  trap 'exit 0' INT TERM
  while :; do sleep 0.1; done ;;
"shell pkill -2 screenrecord") [ -f "$FAKE_HOME/recorder.pid" ] && kill -INT "$(cat "$FAKE_HOME/recorder.pid")" 2>/dev/null ;;
pull*) shift; printf 'fake video' > "$2" ;;
esac
exit 0
`

const fakeEmulator = `#!/bin/sh
[ "$1" = "-list-avds" ] && echo Pixel_Test
exit 0
`

// fakeClipboard records what it was asked to copy: its name and arguments, then what it read. The
// same program stands in for pbcopy, osascript, wl-copy and xclip.
const fakeClipboard = `#!/bin/sh
{ echo "== $(basename "$0") $*"; [ -t 0 ] || cat; } >> "$FAKE_HOME/clipboard.log"
`

// fakeXcrun has no simulators and no phones. devicectl writes its answer to the --json-output file.
const fakeXcrun = `#!/bin/sh
case "$1 $2" in
"simctl list")
  # A test can give the simulators before and after delete unavailable in two files.
  if [ -f "$FAKE_HOME/sims-before.json" ] && [ ! -f "$FAKE_HOME/sims.deleted" ]; then cat "$FAKE_HOME/sims-before.json"
  elif [ -f "$FAKE_HOME/sims-after.json" ]; then cat "$FAKE_HOME/sims-after.json"
  else echo '{"devices":{}}'; fi ;;
"simctl delete") [ "$3" = unavailable ] && touch "$FAKE_HOME/sims.deleted" ;;
"devicectl list")
  while [ $# -gt 0 ]; do
    if [ "$1" = "--json-output" ]; then echo '{"result":{"devices":[]}}' > "$2"; fi
    shift
  done ;;
esac
exit 0
`

// fakeTool prints the device it was given and each key it reads, and records its process id.
const fakeTool = `#!/bin/sh
echo $$ > "$(dirname "$0")/tool.pid"
echo "fake tool on $1"
stty -icanon -echo
while :; do
  c=$(dd bs=1 count=1 2>/dev/null)
  echo "got key [$c]"
done
`

func newWorld(t *testing.T) *world {
	t.Helper()
	home := t.TempDir()
	w := &world{t: t, home: home, project: filepath.Join(home, "app")}
	sdk := filepath.Join(home, "sdk")
	bin := filepath.Join(home, "bin")
	w.write(filepath.Join(sdk, "platform-tools", "adb"), fakeAdb)
	w.write(filepath.Join(sdk, "emulator", "emulator"), fakeEmulator)
	w.write(filepath.Join(bin, "xcrun"), fakeXcrun)
	for _, name := range []string{"pbcopy", "osascript", "wl-copy", "xclip"} {
		w.write(filepath.Join(bin, name), fakeClipboard)
	}
	w.write(filepath.Join(w.project, "tool.sh"), fakeTool)
	w.write(filepath.Join(w.project, ".lazymobile.json"),
		`{"actions": [{"name": "run", "command": ["./tool.sh", "{device_id}"]}]}`)
	w.env = []string{
		"HOME=" + home,
		"FAKE_HOME=" + home,
		"WAYLAND_DISPLAY=wayland-test",
		"XDG_STATE_HOME=" + filepath.Join(home, "state"),
		"XDG_CONFIG_HOME=" + filepath.Join(home, "config"),
		"ANDROID_HOME=" + sdk,
		"PATH=" + bin + ":/usr/bin:/bin",
		"LAZYMOBILE_NO_UPDATE_CHECK=1",
		"LANG=en_US.UTF-8",
	}
	return w
}

func (w *world) write(path, content string) {
	w.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		w.t.Fatal(err)
	}
}

// toolPid is the process id of the fake tool, once it started.
func (w *world) toolPid() int {
	data, err := os.ReadFile(filepath.Join(w.project, "tool.pid"))
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return pid
}

// screen is lazymobile running in a terminal.
type screen struct {
	t *testing.T
	s *vterm.Session
}

// start runs lazymobile with args in the project folder, on a terminal of cols by rows.
func (w *world) start(cols, rows int, args ...string) *screen {
	w.t.Helper()
	s, err := vterm.Start(w.project, append([]string{built(w.t)}, args...), w.env, cols, rows)
	if err != nil {
		w.t.Fatal(err)
	}
	w.t.Cleanup(func() {
		s.Stop()
		s.Wait()
	})
	return &screen{t: w.t, s: s}
}

var styled = regexp.MustCompile(`\x1b\[[0-9;:]*m`)

// text is the screen as plain text.
func (sc *screen) text() string {
	return styled.ReplaceAllString(strings.Join(sc.s.Lines(0), "\n"), "")
}

// row is one row of the screen as plain text.
func (sc *screen) row(i int) string {
	lines := sc.s.Lines(0)
	if i < 0 {
		i += len(lines)
	}
	return styled.ReplaceAllString(lines[i], "")
}

// wait waits until the screen shows all of texts, and fails the test after within.
func (sc *screen) wait(within time.Duration, texts ...string) {
	sc.t.Helper()
	deadline := time.Now().Add(within)
	for {
		screen := sc.text()
		missing := ""
		for _, text := range texts {
			if !strings.Contains(screen, text) {
				missing = text
				break
			}
		}
		if missing == "" {
			return
		}
		if time.Now().After(deadline) {
			sc.t.Fatalf("%q did not show within %v. The screen:\n%s", missing, within, screen)
		}
		select {
		case <-sc.s.Changed():
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func (sc *screen) press(keys ...uv.KeyPressEvent) {
	for _, k := range keys {
		sc.s.SendKey(k)
	}
}

func char(r rune) uv.KeyPressEvent { return uv.KeyPressEvent{Code: r, Text: string(r)} }
func ctrl(r rune) uv.KeyPressEvent { return uv.KeyPressEvent{Code: r, Mod: uv.ModCtrl} }

var esc = uv.KeyPressEvent{Code: uv.KeyEscape}

// What a terminal sends for shift+left and shift+right. The test terminal does not encode them.
const (
	shiftLeft  = "\x1b[1;2D"
	shiftRight = "\x1b[1;2C"
)

// line is the column of the line between the run view's two panes, or -1 when one pane shows.
func (sc *screen) line() int {
	top := []rune(sc.row(0))
	for i := 0; i+1 < len(top); i++ {
		if top[i] == '╮' && top[i+1] == '╭' {
			return i + 1
		}
	}
	return -1
}

func gone(pid int) bool { return pid > 0 && syscall.Kill(pid, 0) != nil }

// runHere starts lazymobile run --here on the fake emulator and waits for the tool.
func (w *world) runHere(cols, rows int) *screen {
	w.t.Helper()
	sc := w.start(cols, rows, "run", "Pixel_Test", "--yes", "--here")
	sc.wait(10*time.Second, "fake tool on emulator-5554", "running")
	return sc
}

func TestE2ERunHereGivesKeysToTheToolAndSaysHowToMove(t *testing.T) {
	w := newWorld(t)
	built(t)
	began := time.Now()
	sc := w.runHere(140, 36)
	if took := time.Since(began); took > 5*time.Second {
		t.Errorf("the tool showed after %v", took)
	}
	sc.wait(2*time.Second, "shift+←→ resize", "ctrl+] dashboard")

	// Keys reach the tool, and how long one takes to show is a bottleneck check.
	began = time.Now()
	sc.press(char('r'))
	sc.wait(2*time.Second, "got key [r]")
	if took := time.Since(began); took > 500*time.Millisecond {
		t.Errorf("a key took %v to show", took)
	}
	sc.press(char('q'), char('+'), char('z'))
	sc.wait(2*time.Second, "got key [q]", "got key [+]", "got key [z]")

	// The log side says how to resize, and the keys do it.
	// The line moves from the tool's own pane, with a key that never reaches the tool.
	before := sc.line()
	sc.s.SendRaw([]byte(shiftLeft + shiftLeft))
	deadline := time.Now().Add(2 * time.Second)
	for sc.line() != before-8 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := sc.line(); got != before-8 {
		t.Errorf("after two shift+left the line is at %d, was %d", got, before)
	}
	sc.s.SendRaw([]byte(shiftRight + shiftRight))
	waitFor(t, 2*time.Second, "the line to come back", func() bool { return sc.line() == before })

	// The log side says how to resize too, and its own keys do it.
	sc.press(ctrl('o'))
	sc.wait(2*time.Second, "shift+←→ resize", "z full width")
	sc.press(char('+'), char('+'))
	deadline = time.Now().Add(2 * time.Second)
	for sc.line() != before-8 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := sc.line(); got != before-8 {
		t.Errorf("after + + the line is at %d, was %d", got, before)
	}
	sc.press(char('z'))
	waitFor(t, 2*time.Second, "the log at full width", func() bool { return sc.line() == -1 })
	sc.press(esc)
	waitFor(t, 2*time.Second, "both panes again", func() bool { return sc.line() == before-8 })
	sc.press(char('x'))
	sc.wait(2*time.Second, "got key [x]")
}

func TestE2EDraggingTheLineMovesIt(t *testing.T) {
	w := newWorld(t)
	sc := w.runHere(140, 36)
	from := sc.line()
	sc.s.SendMouse(uv.MouseClickEvent{X: from, Y: 5, Button: uv.MouseLeft})
	sc.s.SendMouse(uv.MouseMotionEvent{X: from - 10, Y: 5, Button: uv.MouseLeft})
	sc.s.SendMouse(uv.MouseReleaseEvent{X: from - 10, Y: 5, Button: uv.MouseLeft})
	waitFor(t, 2*time.Second, "the line to move", func() bool { return sc.line() == from-10 })
	// The tool still has the keys.
	sc.press(char('k'))
	sc.wait(2*time.Second, "got key [k]")
}

func TestE2ETheDashboardAndBackKeepTheToolRunning(t *testing.T) {
	w := newWorld(t)
	sc := w.runHere(140, 36)
	sc.press(ctrl(']'))
	sc.wait(2*time.Second, "Devices", "Pixel_Test", "keeps running")
	sc.press(ctrl(']'))
	sc.wait(2*time.Second, "fake tool on emulator-5554")
	sc.press(char('a'))
	sc.wait(2*time.Second, "got key [a]")
}

func TestE2EQuittingAsksAndLeavesNothingRunning(t *testing.T) {
	w := newWorld(t)
	sc := w.runHere(140, 36)
	pid := w.toolPid()
	if pid == 0 || gone(pid) {
		t.Fatalf("the tool is not running: pid %d", pid)
	}
	sc.press(ctrl(']'), char('q'))
	sc.wait(2*time.Second, "Stop tool.sh and quit?")
	sc.press(char('y'))
	select {
	case <-sc.s.Done():
	case <-time.After(3 * time.Second):
		t.Fatalf("lazymobile did not quit. The screen:\n%s", sc.text())
	}
	if err := sc.s.Err(); err != nil {
		t.Errorf("lazymobile ended with %v", err)
	}
	waitFor(t, time.Second, "the tool to be gone", func() bool { return gone(pid) })
}

func TestE2EClosingTheTerminalLeavesNothingRunning(t *testing.T) {
	w := newWorld(t)
	sc := w.runHere(140, 36)
	pid := w.toolPid()
	// As when the window is closed: the terminal hangs up.
	sc.s.Stop()
	waitFor(t, 3*time.Second, "lazymobile to end", func() bool {
		select {
		case <-sc.s.Done():
			return true
		default:
			return false
		}
	})
	waitFor(t, 3*time.Second, "the tool to be gone", func() bool { return gone(pid) })
}

func TestE2ETheSplitIsRememberedForTheNextRun(t *testing.T) {
	w := newWorld(t)
	sc := w.runHere(140, 36)
	moved := sc.line() - 12
	sc.press(ctrl('o'), char('+'), char('+'), char('+'))
	waitFor(t, 2*time.Second, "the line to move", func() bool { return sc.line() == moved })
	sc.press(char('q'))
	sc.wait(2*time.Second, "quit?")
	sc.press(char('y'))
	<-sc.s.Done()

	again := w.runHere(140, 36)
	waitFor(t, 2*time.Second, "the line where it was left", func() bool { return again.line() == moved })
}

func TestE2EAtEveryWidthTheHintsFitAndOnePaneShowsWhenNarrow(t *testing.T) {
	for _, cols := range []int{80, 120, 200} {
		t.Run(strconv.Itoa(cols), func(t *testing.T) {
			w := newWorld(t)
			sc := w.runHere(cols, 30)
			if hints := sc.row(-1); !strings.Contains(hints, "ctrl+o log") || !strings.Contains(hints, "ctrl+] dashboard") {
				t.Errorf("the hints: %q", hints)
			}
			if narrow := cols < 100; narrow != (sc.line() == -1) {
				t.Errorf("at %d columns the line is at %d", cols, sc.line())
			}
			// Resizing is offered only where there is a line to move.
			if resize := strings.Contains(sc.row(-1), "shift+←→ resize"); resize == (cols < 100) {
				t.Errorf("the tool's hints at %d columns: %q", cols, sc.row(-1))
			}
			sc.press(ctrl('o'))
			sc.wait(2*time.Second, "esc back to tool.sh")
			if resize := strings.Contains(sc.row(-1), "shift+←→ resize"); resize == (cols < 100) {
				t.Errorf("the log's hints at %d columns: %q", cols, sc.row(-1))
			}
		})
	}
}

func TestE2EALongPasteDoesNotFreezeTheScreen(t *testing.T) {
	w := newWorld(t)
	sc := w.runHere(140, 36)
	sc.s.Paste(strings.Repeat("stack trace line\n", 5000))
	began := time.Now()
	sc.press(ctrl(']'))
	sc.wait(3*time.Second, "Devices")
	if took := time.Since(began); took > time.Second {
		t.Errorf("the dashboard showed %v after a long paste", took)
	}
}

func waitFor(t *testing.T, within time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("waited %v for %s", within, what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// openQuick opens the quick menu. The device list loads in the background, and until it has the
// run's device the menu says so in the note and stays shut, so ctrl+p is pressed again.
func (sc *screen) openQuick() {
	sc.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		sc.press(ctrl('p'))
		until := time.Now().Add(400 * time.Millisecond)
		for time.Now().Before(until) {
			if strings.Contains(sc.text(), "Quick actions") {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	sc.t.Fatalf("the quick menu did not open. The screen:\n%s", sc.text())
}

// clipboard is what the fake clipboard programs were asked to copy.
func (w *world) clipboard() string {
	data, _ := os.ReadFile(filepath.Join(w.home, "clipboard.log"))
	return string(data)
}

// files are the files in the project folder with this extension.
func (w *world) files(ext string) []string {
	found, _ := filepath.Glob(filepath.Join(w.project, "*"+ext))
	return found
}

func TestE2EQuickMenuScreenshotIsSavedAndCopiedFromTheRunView(t *testing.T) {
	w := newWorld(t)
	sc := w.runHere(140, 36)
	sc.openQuick()
	sc.wait(2*time.Second, "Quick actions · Pixel Test", "Record the screen")
	// The tool never saw ctrl+p.
	if strings.Contains(sc.text(), "got key") {
		t.Errorf("the tool got a key:\n%s", sc.text())
	}
	began := time.Now()
	sc.press(char('s'))
	sc.wait(5*time.Second, "Copied a screenshot, saved as")
	if took := time.Since(began); took > 3*time.Second {
		t.Errorf("a screenshot took %v", took)
	}
	shots := w.files(".png")
	if len(shots) != 1 {
		t.Fatalf("screenshots saved: %v", shots)
	}
	if data, _ := os.ReadFile(shots[0]); !strings.HasPrefix(string(data), "\x89PNG") {
		t.Errorf("the saved file is not a PNG: %q", data)
	}
	// The picture is copied as a picture: by osascript with the file's path on macOS, and by wl-copy
	// as image/png elsewhere.
	clip := w.clipboard()
	if !strings.Contains(clip, "PNGf") && !strings.Contains(clip, "image/png") || !strings.Contains(clip, filepath.Base(shots[0])) && !strings.Contains(clip, "fake screen") {
		t.Errorf("the clipboard got:\n%s", clip)
	}
	// The tool still has the keys afterwards.
	sc.press(char('k'))
	sc.wait(2*time.Second, "got key [k]")
}

func TestE2EQuickMenuRecordsAndCopiesTheVideoAndQuitWaitsForIt(t *testing.T) {
	w := newWorld(t)
	sc := w.runHere(140, 36)
	sc.openQuick()
	sc.press(char('r'))
	sc.wait(5*time.Second, "● REC", "Recording Pixel Test")
	waitFor(t, 5*time.Second, "the recorder to start", func() bool {
		_, err := os.Stat(filepath.Join(w.home, "recorder.pid"))
		return err == nil
	})
	time.Sleep(1500 * time.Millisecond)
	sc.wait(2*time.Second, "● REC 0:0")

	sc.openQuick()
	sc.wait(2*time.Second, "Stop recording Pixel Test")
	sc.press(char('r'))
	sc.wait(10*time.Second, "Copied the video of Pixel Test")
	waitFor(t, 2*time.Second, "the REC mark to go", func() bool { return !strings.Contains(sc.text(), "● REC") })
	videos := w.files(".mp4")
	if len(videos) != 1 {
		t.Fatalf("videos saved: %v", videos)
	}
	if data, _ := os.ReadFile(videos[0]); string(data) != "fake video" {
		t.Errorf("the video: %q", data)
	}
	if clip := w.clipboard(); !strings.Contains(clip, filepath.Base(videos[0])) {
		t.Errorf("the clipboard got:\n%s", clip)
	}

	// Quitting while a second recording runs asks, and saves the video before it exits.
	sc.openQuick()
	sc.press(char('r'))
	sc.wait(5*time.Second, "● REC")
	sc.press(ctrl(']'), char('q'))
	sc.wait(2*time.Second, "Stop tool.sh and the recording, then quit?", "saved before lazymobile quits")
	sc.press(char('y'))
	select {
	case <-sc.s.Done():
	case <-time.After(15 * time.Second):
		t.Fatalf("lazymobile did not quit. The screen:\n%s", sc.text())
	}
	if got := len(w.files(".mp4")); got != 2 {
		t.Errorf("%d videos saved after quitting, want 2", got)
	}
}

func TestE2EQuickMenuCopiesTheLogForAI(t *testing.T) {
	w := newWorld(t)
	sc := w.runHere(140, 36)
	// Open the app's log: ctrl+o gives the keys to the log side, and l asks which app.
	sc.press(ctrl('o'))
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(sc.text(), "App on Pixel Test") {
		if time.Now().After(deadline) {
			t.Fatalf("l did not ask which app. The screen:\n%s", sc.text())
		}
		sc.press(char('l'))
		time.Sleep(400 * time.Millisecond)
	}
	sc.s.Paste("com.example.app")
	sc.press(uv.KeyPressEvent{Code: uv.KeyEnter})
	sc.wait(5*time.Second, "hello from the app")
	sc.openQuick()
	sc.press(char('c'))
	sc.wait(5*time.Second, "Copied the last")
	clip := w.clipboard()
	for _, want := range []string{"hello from the app", "com.example.app", "Pixel Test"} {
		if !strings.Contains(clip, want) {
			t.Errorf("the clipboard lacks %q:\n%s", want, clip)
		}
	}
}

func TestE2EHidingTheLogGivesTheToolTheWholeWidthAndTheFlagStartsThatWay(t *testing.T) {
	w := newWorld(t)
	sc := w.runHere(140, 36)
	if sc.line() == -1 {
		t.Fatal("the log should show to begin with")
	}
	sc.openQuick()
	sc.wait(2*time.Second, "Hide the log: the tool gets the whole width")
	sc.press(char('l'))
	waitFor(t, 2*time.Second, "the log to go", func() bool { return sc.line() == -1 && !strings.Contains(sc.text(), "Log ·") })
	// The tool has the whole width: it was told, and the keys still reach it.
	sc.press(char('k'))
	sc.wait(2*time.Second, "got key [k]")
	if strings.Contains(sc.row(-1), "shift+←→") {
		t.Errorf("a hint for a line that is not there: %q", sc.row(-1))
	}
	sc.press(ctrl('o'))
	sc.wait(2*time.Second, "Log · Pixel Test")
	sc.press(ctrl('o'))
	sc.wait(2*time.Second, "fake tool on emulator-5554")
	sc.press(ctrl(']'), char('q'))
	sc.wait(2*time.Second, "Stop tool.sh and quit?")
	sc.press(char('y'))
	<-sc.s.Done()

	// It is remembered, and the menu brings the log back.
	again := w.runHere(140, 36)
	if again.line() != -1 {
		t.Errorf("the next run shows the log: the line is at %d", again.line())
	}
	again.openQuick()
	again.wait(2*time.Second, "Show the log beside the tool")
	again.press(char('l'))
	waitFor(t, 2*time.Second, "the log to come back", func() bool { return again.line() != -1 })
	again.press(ctrl(']'), char('q'))
	again.wait(2*time.Second, "quit?")
	again.press(char('y'))
	<-again.s.Done()

	// The flag hides it for one run without changing what is remembered.
	flagged := w.start(140, 36, "run", "Pixel_Test", "--yes", "--here", "--no-log")
	flagged.wait(10*time.Second, "fake tool on emulator-5554", "running")
	if flagged.line() != -1 || strings.Contains(flagged.text(), "Log ·") {
		t.Errorf("--no-log shows the log: the line is at %d", flagged.line())
	}
}

func TestE2ETheDashboardShowsSimulatorSizesAndDeletesTheUnavailableOnes(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("simulators are only on a Mac")
	}
	w := newWorld(t)
	usable := `{"udid": "00000000-0000-4000-8000-0000000000A1", "name": "iPhone Test", "state": "Shutdown", "isAvailable": true, "dataPathSize": 7600000000}`
	orphans := `{"udid": "00000000-0000-4000-8000-0000000000D1", "name": "Old Phone", "state": "Shutdown", "isAvailable": false, "dataPathSize": 1854255104, "availabilityError": "runtime profile not found"},
	  {"udid": "00000000-0000-4000-8000-0000000000D2", "name": "Old Tablet", "state": "Shutdown", "isAvailable": false, "dataPathSize": 18337792}`
	w.write(filepath.Join(w.home, "sims-before.json"),
		`{"devices": {"com.apple.CoreSimulator.SimRuntime.iOS-27-2": [`+usable+`], "com.apple.CoreSimulator.SimRuntime.iOS-27-0": [`+orphans+`]}}`)
	w.write(filepath.Join(w.home, "sims-after.json"), `{"devices": {"com.apple.CoreSimulator.SimRuntime.iOS-27-2": [`+usable+`]}}`)

	sc := w.start(140, 36)
	sc.wait(10*time.Second, "iPhone Test", "7.6 GB", "2 simulators Xcode can no longer run use 1.9 GB", "Press U to delete them.")
	if strings.Contains(sc.text(), "Old Phone") {
		t.Errorf("an unavailable simulator is in the list:\n%s", sc.text())
	}

	// The question names them, and no deletes nothing.
	sc.press(char('U'))
	sc.wait(2*time.Second, "Old Phone, iOS 27.0, 1.9 GB", "Old Tablet, iOS 27.0, 18 MB", "cannot be had back")
	sc.press(char('n'))
	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(w.home, "sims.deleted")); err == nil {
		t.Fatal("something was deleted after n")
	}

	sc.press(char('U'), char('y'))
	sc.wait(10*time.Second, "Deleted 2 unavailable simulators, freed 1.9 GB")
	waitFor(t, 5*time.Second, "the hint to go", func() bool { return !strings.Contains(sc.text(), "Press U to delete them.") })
	if _, err := os.Stat(filepath.Join(w.home, "sims.deleted")); err != nil {
		t.Error("simctl delete unavailable never ran")
	}
	sc.wait(2*time.Second, "iPhone Test")
}

func TestE2EListAndDeleteUnavailableFromTheCommandLine(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("simulators are only on a Mac")
	}
	w := newWorld(t)
	w.write(filepath.Join(w.home, "sims-before.json"),
		`{"devices": {"com.apple.CoreSimulator.SimRuntime.iOS-27-0": [{"udid": "00000000-0000-4000-8000-0000000000D1", "name": "Old Phone", "state": "Shutdown", "isAvailable": false, "dataPathSize": 1854255104}]}}`)
	w.write(filepath.Join(w.home, "sims-after.json"), `{"devices": {}}`)
	run := func(args ...string) (string, error) {
		cmd := exec.Command(built(t), args...)
		cmd.Dir, cmd.Env = w.project, w.env
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	out, err := run("delete", "--unavailable")
	if err == nil || !strings.Contains(out, "removes 1 unavailable simulator (1.9 GB)") || !strings.Contains(out, "Old Phone, iOS 27.0, 1.9 GB") {
		t.Errorf("without --yes: err %v\n%s", err, out)
	}
	if _, statErr := os.Stat(filepath.Join(w.home, "sims.deleted")); statErr == nil {
		t.Fatal("something was deleted without --yes")
	}
	out, err = run("delete", "--unavailable", "--yes")
	if err != nil || strings.TrimSpace(out) != "Deleted 1 unavailable simulator, freed 1.9 GB" {
		t.Errorf("with --yes: err %v\n%s", err, out)
	}
	out, err = run("delete", "--unavailable", "--yes")
	if err != nil || !strings.Contains(out, "No unavailable simulators") {
		t.Errorf("second time: err %v\n%s", err, out)
	}
}

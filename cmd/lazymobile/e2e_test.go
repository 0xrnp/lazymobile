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
"emu avd name") printf 'Pixel_Test\nOK\n' ;;
logcat*) while :; do echo "11-19 10:00:00.000  100  100 I App: hello from the app"; sleep 1; done ;;
esac
exit 0
`

const fakeEmulator = `#!/bin/sh
[ "$1" = "-list-avds" ] && echo Pixel_Test
exit 0
`

// fakeXcrun has no simulators and no phones. devicectl writes its answer to the --json-output file.
const fakeXcrun = `#!/bin/sh
case "$1 $2" in
"simctl list") echo '{"devices":{}}' ;;
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
	w.write(filepath.Join(w.project, "tool.sh"), fakeTool)
	w.write(filepath.Join(w.project, ".lazymobile.json"),
		`{"actions": [{"name": "run", "command": ["./tool.sh", "{device_id}"]}]}`)
	w.env = []string{
		"HOME=" + home,
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
	sc.wait(2*time.Second, "ctrl+o log, then + - to resize", "ctrl+] dashboard")

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
	sc.press(ctrl('o'))
	sc.wait(2*time.Second, "+ - resize (or drag the line)", "z full width")
	before := sc.line()
	sc.press(char('+'), char('+'))
	deadline := time.Now().Add(2 * time.Second)
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
			if resize := strings.Contains(sc.row(-1), "+ - to resize"); resize == (cols < 100) {
				t.Errorf("the tool's hints at %d columns: %q", cols, sc.row(-1))
			}
			sc.press(ctrl('o'))
			sc.wait(2*time.Second, "esc back to tool.sh")
			if resize := strings.Contains(sc.row(-1), "+ - resize"); resize == (cols < 100) {
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

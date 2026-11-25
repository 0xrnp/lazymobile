package vterm

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
)

// screenHas waits for text to show on the screen.
func screenHas(t *testing.T, s *Session, text string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(strings.Join(s.Lines(0), "\n"), text) {
			return
		}
		select {
		case <-s.Changed():
		case <-time.After(50 * time.Millisecond):
		}
	}
	t.Fatalf("%q never showed. The screen:\n%s", text, strings.Join(s.Lines(0), "\n"))
}

func ended(t *testing.T, s *Session) {
	t.Helper()
	select {
	case <-s.Done():
	case <-time.After(StopGrace + 3*time.Second):
		t.Fatal("the program did not end")
	}
}

func start(t *testing.T, script string, cols, rows int) *Session {
	t.Helper()
	s, err := Start(t.TempDir(), []string{"sh", "-c", script}, os.Environ(), cols, rows)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.Stop()
		s.Wait()
	})
	return s
}

func TestTheProgramSeesATerminalAndGetsTheKeysTyped(t *testing.T) {
	// Like a tool waiting for single keys, as flutter run does for r.
	s := start(t, `[ -t 0 ] && [ -t 1 ] && echo "a terminal: $TERM"; stty -icanon -echo; c=$(dd bs=1 count=1 2>/dev/null); echo "key [$c]"`, 60, 10)
	screenHas(t, s, "a terminal: xterm-256color")
	s.SendKey(uv.KeyPressEvent{Code: 'r', Text: "r"})
	screenHas(t, s, "key [r]")
	ended(t, s)
	if err := s.Err(); err != nil {
		t.Errorf("err = %v", err)
	}
	// The output stays after the program ended, to read what it said last.
	screenHas(t, s, "key [r]")
}

func TestCtrlCReachesTheProgram(t *testing.T) {
	s := start(t, `trap 'echo interrupted; exit 3' INT; echo ready; while :; do sleep 0.1; done`, 40, 5)
	screenHas(t, s, "ready")
	s.SendKey(uv.KeyPressEvent{Code: 'c', Mod: uv.ModCtrl})
	screenHas(t, s, "interrupted")
	ended(t, s)
	var exit *exec.ExitError
	if err := s.Err(); !errors.As(err, &exit) || exit.ExitCode() != 3 {
		t.Errorf("err = %v, want exit code 3", err)
	}
}

func TestResizeTellsTheProgram(t *testing.T) {
	s := start(t, `stty size; trap 'stty size' WINCH; echo waiting; while :; do sleep 0.1; done`, 50, 8)
	screenHas(t, s, "8 50")
	screenHas(t, s, "waiting")
	s.Resize(70, 12)
	screenHas(t, s, "12 70")
	if got := len(s.Lines(0)); got != 12 {
		t.Errorf("%d rows after the resize, want 12", got)
	}
}

func TestStopEndsTheProgramAndWhatItStarted(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child")
	// The child ignores nothing and is not waited for, like a dev server a tool leaves running.
	s := start(t, `sleep 300 & echo $! > `+pidFile+`; echo started; wait`, 40, 5)
	screenHas(t, s, "started")
	s.Stop()
	ended(t, s)
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	deadline := time.Now().Add(3 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("the child %d is still running", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestStopKillsAProgramThatIgnoresTheInterrupt(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for the grace period")
	}
	s := start(t, `trap '' INT; echo stubborn; while :; do sleep 0.1; done`, 40, 5)
	screenHas(t, s, "stubborn")
	began := time.Now()
	s.Stop()
	ended(t, s)
	if took := time.Since(began); took < StopGrace-time.Second {
		t.Errorf("ended after %v, before the grace period", took)
	}
}

func TestScrollingBackShowsWhatLeftTheScreen(t *testing.T) {
	s := start(t, `i=1; while [ $i -le 30 ]; do echo "line $i"; i=$((i+1)); done; echo end; sleep 30`, 30, 5)
	screenHas(t, s, "end")
	if s.ScrollbackLen() < 20 {
		t.Fatalf("scrollback %d", s.ScrollbackLen())
	}
	// Five rows back, the screen shows only rows that scrolled off, so not the last one printed.
	if lines := s.Lines(5); len(lines) != 5 || !strings.Contains(lines[0], "line ") || strings.Contains(strings.Join(lines, " "), "end") {
		t.Errorf("five rows back: %q", lines)
	}
	if got := s.Lines(1_000_000); len(got) != 5 || !strings.Contains(got[0], "line 1") {
		t.Errorf("scrolled to the top: %q", got)
	}
}

func TestAProgramThatIsNotThereIsAnError(t *testing.T) {
	if _, err := Start(t.TempDir(), []string{"no-such-tool-lazymobile"}, os.Environ(), 20, 5); err == nil {
		t.Error("no error")
	}
	if _, err := Start(t.TempDir(), nil, nil, 20, 5); err == nil {
		t.Error("no error for an empty command")
	}
}

// within fails the test when f has not returned after d.
func within(t *testing.T, d time.Duration, what string, f func()) {
	t.Helper()
	finished := make(chan struct{})
	go func() {
		f()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(d):
		t.Fatalf("%s took more than %v", what, d)
	}
}

func TestAPasteIntoAProgramThatIsNotReadingFreezesNothing(t *testing.T) {
	// The program never reads its input, as a build does before it looks at keys.
	s := start(t, `echo ready; sleep 30`, 40, 5)
	screenHas(t, s, "ready")
	within(t, 2*time.Second, "a long paste", func() { s.Paste(strings.Repeat("stack trace line\n", 20000)) })
	within(t, 2*time.Second, "a key after it", func() { s.SendKey(uv.KeyPressEvent{Code: 'r', Text: "r"}) })
	within(t, 2*time.Second, "drawing the screen", func() { s.Lines(0) })
	within(t, 2*time.Second, "a resize", func() { s.Resize(50, 6) })
}

func TestTheSessionEndsWhenAChildKeepsTheTerminal(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child")
	// The child ignores the hang-up, so it keeps the terminal open after the program ends.
	s := start(t, `(trap '' HUP INT; sleep 30) & echo $! > `+pidFile+`; echo started`, 40, 5)
	t.Cleanup(func() {
		if data, err := os.ReadFile(pidFile); err == nil {
			if pid, _ := strconv.Atoi(strings.TrimSpace(string(data))); pid > 0 {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	screenHas(t, s, "started")
	within(t, drainWait+3*time.Second, "the end of the session", func() { <-s.Done() })
	// The output reader stops too, rather than waiting for the child's next output.
	within(t, 3*time.Second, "the output reader", func() { <-s.drained })
}

func TestStopKillsAChildThatOutlivesTheProgramAndIgnoresTheInterrupt(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for the grace period")
	}
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child")
	s := start(t, `(trap '' HUP INT; sleep 300) & echo $! > `+pidFile+`; echo started; wait`, 40, 5)
	screenHas(t, s, "started")
	s.Stop()
	within(t, StopGrace+3*time.Second, "Stop and Wait", s.Wait)
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	if syscall.Kill(pid, 0) == nil {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Errorf("the child %d was left running", pid)
	}
}

func TestTailGivesTheLastRowsAsPlainTextIncludingWhatScrolledOff(t *testing.T) {
	// 30 numbered lines on a 5-row screen: most of them scrolled off, and the first is in colour.
	s := start(t, `printf '\033[31mline 1\033[0m\n'; for i in $(seq 2 30); do echo "line $i"; done`, 40, 5)
	ended(t, s)
	screenHas(t, s, "line 30")
	tail := s.Tail(8)
	if len(tail) != 8 || tail[0] != "line 23" || tail[7] != "line 30" {
		t.Errorf("tail = %q", tail)
	}
	// All of it is there, with the colour taken off.
	all := s.Tail(100)
	if len(all) != 30 || all[0] != "line 1" {
		t.Errorf("%d rows, first %q", len(all), all[0])
	}
}

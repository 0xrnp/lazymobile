// Package vterm runs a program in a pseudo-terminal and keeps its screen, so the dashboard can show
// a tool such as `flutter run` or `expo start` in a pane and pass it the user's keys. The program
// sees a real terminal: colours, its own key handling and redraws in place work as they would
// outside lazymobile.
package vterm

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

const (
	// scrollbackLines is how much of the program's output is kept above the screen.
	scrollbackLines = 5000
	// StopGrace is how long a program has to end after an interrupt before it is killed.
	StopGrace = 5 * time.Second
	// drainWait is how long output is still read after the program ended. A program that left a
	// child holding the terminal open would otherwise keep the session from ending.
	drainWait = time.Second
	// killWait is how long Stop waits for what it killed to be gone.
	killWait = 2 * time.Second
	// maxPending is how much typed input waits for a program that is not reading it. More is dropped.
	maxPending = 1 << 20
)

// Session is a program running in a pseudo-terminal, and its screen.
type Session struct {
	cmd  *exec.Cmd
	ptmx *os.File

	// mu guards the emulator, which the output reader writes and the dashboard draws, and closed.
	mu     sync.Mutex
	emu    *vt.Emulator
	closed bool

	input *pending

	changed chan struct{}
	done    chan struct{}
	err     error
	stop    sync.Once
	// stopping is set by Stop, and reaped is closed once what it stopped is gone or killed.
	stopping bool
	reaped   chan struct{}
	// drained is closed when the output reader has stopped.
	drained chan struct{}
}

// Start runs argv in dir on a terminal of cols by rows, with env plus what a terminal sets.
func Start(dir string, argv []string, env []string, cols, rows int) (*Session, error) {
	if len(argv) == 0 {
		return nil, errors.New("no command to run")
	}
	cols, rows = max(cols, 1), max(rows, 1)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = append(append([]string(nil), env...), "TERM=xterm-256color", "COLORTERM=truecolor")
	// The program leads a session of its own, with the pseudo-terminal as its terminal, so ctrl+c
	// reaches it and everything it starts, and Stop can signal them all.
	started, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, err
	}
	ptmx, err := pollable(started)
	if err != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
		return nil, err
	}
	emu := vt.NewEmulator(cols, rows)
	emu.SetScrollbackSize(scrollbackLines)
	s := &Session{cmd: cmd, ptmx: ptmx, emu: emu, input: newPending(),
		changed: make(chan struct{}, 1), done: make(chan struct{}), reaped: make(chan struct{}), drained: make(chan struct{})}
	go s.readOutput()
	// What the emulator sends back, keys and the answers to the program's questions about the
	// terminal, is queued, and the queue is written to the program. The emulator writes it while
	// s.mu is held, so it must never wait for a program that is not reading its input: that would
	// stop the screen and every key, ctrl+c included.
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := emu.Read(buf)
			s.input.push(buf[:n])
			if err != nil {
				s.input.close()
				return
			}
		}
	}()
	go func() {
		for {
			data, ok := s.input.take()
			if !ok {
				return
			}
			if _, err := ptmx.Write(data); err != nil {
				return
			}
		}
	}()
	go s.wait()
	return s, nil
}

// pollable is a copy of the pseudo-terminal's main side that Go's poller watches, so closing it
// ends a Read that is waiting. The file pty gives is blocking, and closing it would wait for the next
// output of whatever still holds the terminal.
func pollable(f *os.File) (*os.File, error) {
	fd, err := syscall.Dup(int(f.Fd()))
	_ = f.Close()
	if err != nil {
		return nil, err
	}
	syscall.CloseOnExec(fd)
	if err := syscall.SetNonblock(fd, true); err != nil {
		_ = syscall.Close(fd)
		return nil, err
	}
	return os.NewFile(uintptr(fd), "/dev/ptmx"), nil
}

// setSize sets the terminal's size without File.Fd, which would put the file back in blocking mode.
func setSize(f *os.File, cols, rows int) error {
	conn, err := f.SyscallConn()
	if err != nil {
		return err
	}
	ws := pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}
	var errno syscall.Errno
	if err := conn.Control(func(fd uintptr) {
		_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(&ws)))
	}); err != nil {
		return err
	}
	if errno != 0 {
		return errno
	}
	return nil
}

// pending is input waiting to be written to the program.
type pending struct {
	mu     sync.Mutex
	data   []byte
	closed bool
	ready  chan struct{}
}

func newPending() *pending { return &pending{ready: make(chan struct{}, 1)} }

func (p *pending) push(data []byte) {
	if len(data) == 0 {
		return
	}
	p.mu.Lock()
	if len(p.data)+len(data) <= maxPending {
		p.data = append(p.data, data...)
	}
	p.mu.Unlock()
	p.signal()
}

func (p *pending) close() {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	p.signal()
}

func (p *pending) signal() {
	select {
	case p.ready <- struct{}{}:
	default:
	}
}

// take waits for input, and returns all there is. It reports false once closed and empty.
func (p *pending) take() ([]byte, bool) {
	for {
		p.mu.Lock()
		if len(p.data) > 0 {
			data := p.data
			p.data = nil
			p.mu.Unlock()
			return data, true
		}
		if p.closed {
			p.mu.Unlock()
			return nil, false
		}
		p.mu.Unlock()
		<-p.ready
	}
}

func (s *Session) readOutput() {
	defer close(s.drained)
	buf := make([]byte, 32*1024)
	for {
		n, err := s.ptmx.Read(buf)
		if n > 0 {
			s.mu.Lock()
			if !s.closed {
				_, _ = s.emu.Write(buf[:n])
			}
			s.mu.Unlock()
			s.notify()
		}
		if err != nil {
			return
		}
	}
}

func (s *Session) wait() {
	err := s.cmd.Wait()
	select {
	case <-s.drained:
	case <-time.After(drainWait):
	}
	// Closing the emulator's input ends the pump that copies it to the program, and frees a write
	// that waits for that pump. Emulator.Close would do it too, but it sets a field that Read checks
	// without a lock, and the pump is in Read.
	if pipe, ok := s.emu.InputPipe().(io.Closer); ok {
		_ = pipe.Close()
	}
	// Closing the terminal hangs up on anything the program left running on it.
	_ = s.ptmx.Close()
	s.mu.Lock()
	s.err, s.closed = err, true
	s.mu.Unlock()
	close(s.done)
	s.notify()
}

func (s *Session) notify() {
	select {
	case s.changed <- struct{}{}:
	default:
	}
}

// Changed receives when the screen may have changed. Changes that come close together are told once.
func (s *Session) Changed() <-chan struct{} { return s.changed }

// Done is closed when the program has ended.
func (s *Session) Done() <-chan struct{} { return s.done }

// Err is how the program ended, once Done is closed: nil, or an *exec.ExitError.
func (s *Session) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Wait waits for the program to end and, after Stop, for what it started to be gone or killed.
func (s *Session) Wait() {
	<-s.done
	s.mu.Lock()
	stopping := s.stopping
	s.mu.Unlock()
	if stopping {
		<-s.reaped
	}
}

// Stop interrupts the program and everything it started, as ctrl+c would, and kills whatever of
// them is left StopGrace later. It returns at once.
func (s *Session) Stop() {
	s.stop.Do(func() {
		s.mu.Lock()
		s.stopping = true
		s.mu.Unlock()
		group := -s.cmd.Process.Pid
		_ = syscall.Kill(group, syscall.SIGINT)
		go func() {
			defer close(s.reaped)
			// The group is the program and what it started. Its id is not given to another process
			// while any of them lives, so looking it up and killing it name the same processes.
			gone := func(within time.Duration) bool {
				deadline := time.Now().Add(within)
				for time.Now().Before(deadline) {
					if syscall.Kill(group, 0) != nil {
						return true
					}
					time.Sleep(50 * time.Millisecond)
				}
				return false
			}
			if gone(StopGrace) {
				return
			}
			_ = syscall.Kill(group, syscall.SIGKILL)
			// A kill takes effect a moment after it is sent, so Wait returns once they are gone.
			gone(killWait)
		}()
	})
}

// Resize tells the program its terminal is now cols by rows.
func (s *Session) Resize(cols, rows int) {
	cols, rows = max(cols, 1), max(rows, 1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.emu.Resize(cols, rows)
	_ = setSize(s.ptmx, cols, rows)
}

// SendKey types a key, encoded the way the program asked for.
func (s *Session) SendKey(k uv.KeyEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.emu.SendKey(k)
	}
}

// SendRaw sends bytes to the program as they are, for a key the emulator does not encode, such as
// an arrow with Shift, which a real terminal sends as ESC [ 1 ; 2 D.
func (s *Session) SendRaw(data []byte) {
	s.input.push(data)
}

// SendMouse sends a mouse event, encoded the way the program asked for. Nothing is sent when the
// program did not ask for the mouse.
func (s *Session) SendMouse(m uv.MouseEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.emu.SendMouse(m)
	}
}

// Paste sends text as a paste.
func (s *Session) Paste(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.emu.Paste(text)
	}
}

// Lines draws the screen, one string per row, with back rows of what scrolled off the top shown
// above it, as when scrolling back. A program on the alternate screen, such as an editor, has no
// scrollback.
func (s *Session) Lines(back int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := s.emu.Height()
	screen := strings.Split(s.emu.Render(), "\n")
	for len(screen) < rows {
		screen = append(screen, "")
	}
	screen = screen[:rows]
	if back <= 0 || s.emu.IsAltScreen() {
		return screen
	}
	sb := s.emu.Scrollback()
	n := sb.Len()
	back = min(back, n)
	out := make([]string, 0, rows)
	for i := n - back; i < n && len(out) < rows; i++ {
		out = append(out, sb.Line(i).Render())
	}
	for i := 0; len(out) < rows; i++ {
		out = append(out, screen[i])
	}
	return out
}

// ScrollbackLen is how many rows can be scrolled back.
func (s *Session) ScrollbackLen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.emu.IsAltScreen() {
		return 0
	}
	return s.emu.Scrollback().Len()
}

// Cursor is where the program's cursor is on its screen, and whether it shows.
func (s *Session) Cursor() (x, y int, visible bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.emu.CursorPosition()
	return p.X, p.Y, !s.emu.CursorHidden() && !s.closed
}

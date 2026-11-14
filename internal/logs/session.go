//go:build unix

package logs

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/run"
)

const (
	pidofPoll = 2 * time.Second
	// Lines are handed over in batches, so a busy log does not flood the receiver.
	batchEvery = 50 * time.Millisecond
	batchMax   = 500
	// Stay under bufio's limit handling by reading with a Reader, but still cut runaway lines.
	maxLineBytes = 64 * 1024
)

// IOSApps finds an app's process name on a simulator.
type IOSApps interface {
	AppExecutable(ctx context.Context, d device.Device, bundleID string) (string, error)
}

// Deps are the tools a session needs.
type Deps struct {
	Runner run.Runner
	IOS    IOSApps
	// ADB returns the path of adb.
	ADB func() (string, error)
	// LookPath finds a program on PATH. Default: exec.LookPath.
	LookPath func(string) (string, error)
}

// Spec says what to stream.
type Spec struct {
	Device device.Device
	Mode   Mode
	// App is an Android package or iOS bundle id. Optional except for Console.
	App string
	// Tail is how many earlier Android lines to show first. Default 1000.
	Tail int
}

// Handlers receive the stream. They are called from the session's goroutine, one at a time.
type Handlers struct {
	Lines  func([]Line)
	Status func(string)
}

// Session is a running log stream.
type Session struct {
	cancel context.CancelFunc
	done   chan struct{}
	err    error
}

// Start begins streaming and returns at once. Problems arrive through Wait.
func Start(ctx context.Context, deps Deps, spec Spec, h Handlers) *Session {
	ctx, cancel := context.WithCancel(ctx)
	s := &Session{cancel: cancel, done: make(chan struct{})}
	if h.Lines == nil {
		h.Lines = func([]Line) {}
	}
	if h.Status == nil {
		h.Status = func(string) {}
	}
	go func() {
		defer close(s.done)
		defer cancel()
		err := start(ctx, deps, spec, h)
		// A stream that ended because it was stopped is not a failure.
		if ctx.Err() == nil {
			s.err = err
		}
	}()
	return s
}

// Stop ends the stream. It is safe to call more than once.
func (s *Session) Stop() {
	s.cancel()
}

// Wait blocks until the stream has ended and every handler call has returned. It returns nil
// when the stream was stopped, and the reason otherwise.
func (s *Session) Wait() error {
	<-s.done
	return s.err
}

// Done is closed when the stream has ended.
func (s *Session) Done() <-chan struct{} {
	return s.done
}

func checkMode(d device.Device, mode Mode) error {
	for _, m := range Modes(d) {
		if m.ID == mode {
			return nil
		}
	}
	if !d.Ready() {
		return apperr.New(apperr.InvalidState, "%s is not running", d.Name).WithHint("Boot or connect it first.")
	}
	return apperr.New(apperr.Unsupported, "%s has no %s log", d.Name, mode)
}

func start(ctx context.Context, deps Deps, spec Spec, h Handlers) error {
	d := spec.Device
	if err := checkMode(d, spec.Mode); err != nil {
		return err
	}
	if spec.Mode == Console && spec.App == "" {
		return apperr.New(apperr.InvalidState, "Launching an app needs its bundle id")
	}
	if spec.App != "" && !ValidAppID(spec.App) {
		return apperr.New(apperr.InvalidState, "That does not look like an app id: %s", spec.App).
			WithHint("Use a package name or bundle id, such as com.example.app.")
	}

	if d.Platform == device.Android {
		return startAndroid(ctx, deps, spec, h)
	}

	if spec.Mode == Console {
		argv := []string{"xcrun", "devicectl", "device", "process", "launch", "--device", d.ID, "--console", "--terminate-existing", spec.App}
		if d.Kind == device.Simulator {
			// A PTY keeps the app's stdout line-buffered, so print() output arrives at once.
			argv = []string{"xcrun", "simctl", "launch", "--console-pty", "--terminate-running-process", d.ID, spec.App}
		}
		h.Status("Launching " + spec.App + ". Its output appears below.")
		return stream(ctx, argv, ParsePlain, nil, h)
	}

	if d.Kind == device.Physical {
		look := deps.LookPath
		if look == nil {
			look = exec.LookPath
		}
		if _, err := look("idevicesyslog"); err != nil {
			return apperr.New(apperr.ToolMissing, "idevicesyslog was not found").WithHint(
				"Install libimobiledevice (brew install libimobiledevice) for the live system log. "+
					"For your own app, the app console needs nothing extra. "+
					"To save past logs instead: sudo log collect --device-udid %s --last 10m", d.ID)
		}
		return stream(ctx, []string{"idevicesyslog", "-u", d.ID}, ParsePlain, nil, h)
	}

	// Simulator system log. Without an app it is very busy, so debug messages only come with one.
	argv := []string{"xcrun", "simctl", "spawn", d.ID, "log", "stream", "--style", "compact"}
	if spec.App == "" {
		return stream(ctx, argv, ParseIOS, nil, h)
	}
	name, err := deps.IOS.AppExecutable(ctx, d, spec.App)
	if err != nil {
		return err
	}
	if !safeProcessName.MatchString(name) {
		return apperr.New(apperr.Unsupported, "Unusual app executable name: %s", name)
	}
	h.Status(fmt.Sprintf("Showing process %q (%s), including debug messages", name, spec.App))
	argv = append(argv, "--level", "debug", "--predicate", fmt.Sprintf(`process == "%s"`, name))
	return stream(ctx, argv, ParseIOS, nil, h)
}

func startAndroid(ctx context.Context, deps Deps, spec Spec, h Handlers) error {
	adb, err := deps.ADB()
	if err != nil {
		return err
	}
	serial := spec.Device.TargetID()
	tail := spec.Tail
	if tail == 0 {
		tail = 1000
	}
	argv := []string{adb, "-s", serial, "logcat", "-v", "threadtime", "-T", strconv.Itoa(tail)}
	if spec.App == "" {
		return stream(ctx, argv, ParseAndroid, nil, h)
	}

	filter := NewAppFilter(spec.App)
	pidof := func() {
		res := deps.Runner.Run(ctx, []string{adb, "-s", serial, "shell", "pidof", spec.App}, run.Options{Timeout: 5 * time.Second})
		if res.Code == 0 && !res.Missing && res.Err == nil && !res.TimedOut {
			filter.AddPIDs(strings.Fields(res.Stdout))
		}
	}
	// Ask first, so the earlier lines of an app that is already running are not dropped for
	// arriving before its pid is known.
	pidof()
	if ctx.Err() != nil {
		return ctx.Err()
	}

	pollCtx, stopPoll := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(pidofPoll)
		defer ticker.Stop()
		for {
			select {
			case <-pollCtx.Done():
				return
			case <-ticker.C:
				pidof()
			}
		}
	}()
	err = stream(ctx, argv, ParseAndroid, filter, h)
	stopPoll()
	wg.Wait()
	return err
}

type rawLine struct {
	text   string
	stderr bool
}

// stream runs argv until it ends or ctx is cancelled, and hands its lines to h in batches.
func stream(ctx context.Context, argv []string, parse func(string) Line, filter *AppFilter, h Handlers) error {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	run.Group(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return apperr.New(apperr.CommandFailed, "Could not start the log stream: %v", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return apperr.New(apperr.CommandFailed, "Could not start the log stream: %v", err)
	}
	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
			return &apperr.Error{Code: apperr.ToolMissing, Message: argv[0] + " was not found", Argv: argv}
		}
		return &apperr.Error{Code: apperr.CommandFailed, Message: "Could not start the log stream: " + err.Error(), Argv: argv}
	}

	// Both readers send here. They are the only senders, and the goroutine below closes the
	// channel once both have returned.
	lines := make(chan rawLine)
	var readers sync.WaitGroup
	read := func(r io.Reader, isStderr bool) {
		defer readers.Done()
		reader := bufio.NewReaderSize(r, 32*1024)
		for {
			text, err := readLine(reader)
			if text != "" || err == nil {
				select {
				case lines <- rawLine{text, isStderr}:
				case <-ctx.Done():
					return
				}
			}
			if err != nil {
				return
			}
		}
	}
	readers.Add(2)
	go read(stdout, false)
	go read(stderr, true)
	go func() {
		readers.Wait()
		close(lines)
	}()

	// Error output is shown like any other line, and its end also explains a failure.
	var recentErrors []string
	batch := make([]Line, 0, batchMax)
	flush := func() {
		if len(batch) > 0 {
			h.Lines(batch)
			batch = make([]Line, 0, batchMax)
		}
	}
	ticker := time.NewTicker(batchEvery)
	defer ticker.Stop()
	for open := true; open; {
		select {
		case raw, more := <-lines:
			if !more {
				open = false
				break
			}
			if raw.stderr {
				recentErrors = append(recentErrors, raw.text)
				if len(recentErrors) > 20 {
					recentErrors = recentErrors[len(recentErrors)-20:]
				}
			}
			if line := parse(raw.text); filter == nil || filter.Accept(line) {
				batch = append(batch, line)
				if len(batch) >= batchMax {
					flush()
				}
			}
		case <-ticker.C:
			flush()
		}
	}
	flush()

	// The pipes are fully read, so Wait can close them.
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	res := run.Result{Stderr: strings.Join(recentErrors, "\n")}
	var exit *exec.ExitError
	switch {
	case errors.As(waitErr, &exit):
		res.Code = exit.ExitCode()
	case waitErr != nil:
		res.Err = waitErr
	}
	if err := apperr.FromResult(res, argv, ""); err != nil {
		return err
	}
	return &apperr.Error{Code: apperr.CommandFailed, Message: "The log stream ended", Argv: argv}
}

// readLine reads one line without its line ending. A line longer than maxLineBytes is cut and
// the rest of it is skipped.
func readLine(r *bufio.Reader) (string, error) {
	var b strings.Builder
	for {
		chunk, err := r.ReadSlice('\n')
		if room := maxLineBytes - b.Len(); room > 0 {
			if len(chunk) > room {
				chunk = chunk[:room]
			}
			b.Write(chunk)
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		return strings.TrimRight(b.String(), "\r\n"), err
	}
}

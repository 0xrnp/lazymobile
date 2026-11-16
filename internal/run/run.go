//go:build unix

// Package run starts external tools. Commands are always argument lists, never shell strings.
package run

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// Result is how a command ended. Missing and Err mean it never ran.
type Result struct {
	Stdout   string
	Stderr   string
	Code     int
	TimedOut bool
	// Missing is true when the program was not found.
	Missing bool
	// Err is set when the program was found but could not be started.
	Err error
}

// Options change how one command runs.
type Options struct {
	// Timeout stops the command and its children. Zero means no limit beyond ctx.
	Timeout time.Duration
	// Env adds "KEY=value" entries to the current environment.
	Env []string
	// Stdin is written to the command's standard input.
	Stdin string
	// Dir is the working directory. Empty means the current one.
	Dir string
	// Interrupt stops the command, at the end of Timeout or when ctx ends, with an interrupt in
	// place of a termination. A recorder needs it to finish its file, and is given time to.
	Interrupt bool
}

// Runner runs a command to completion. Backends take one so tests can answer for the tools.
type Runner interface {
	Run(ctx context.Context, argv []string, opts Options) Result
}

// Exec runs real programs.
type Exec struct{}

// Run implements Runner.
func (Exec) Run(ctx context.Context, argv []string, opts Options) Result {
	if len(argv) == 0 {
		return Result{Err: errors.New("empty command")}
	}
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if opts.Stdin != "" {
		cmd.Stdin = strings.NewReader(opts.Stdin)
	}
	if len(opts.Env) > 0 {
		cmd.Env = append(os.Environ(), opts.Env...)
	}
	cmd.Dir = opts.Dir
	Group(cmd)
	if opts.Interrupt {
		cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGINT) }
		cmd.WaitDelay = 30 * time.Second
	}

	err := cmd.Run()
	res := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	if err == nil {
		return res
	}
	if ctx.Err() != nil {
		res.TimedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
		res.Code = -1
		if !res.TimedOut {
			res.Err = ctx.Err()
		}
		return res
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		res.Code = exit.ExitCode()
		return res
	}
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
		res.Missing = true
		return res
	}
	res.Err = err
	return res
}

// Group puts cmd in its own process group and makes cancellation stop the whole group. Tools
// such as xcrun and adb start children, which would otherwise outlive a cancelled command.
func Group(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
	// After SIGTERM, wait this long for output to close before the pipes are abandoned.
	cmd.WaitDelay = 3 * time.Second
}

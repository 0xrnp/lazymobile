//go:build unix

package run

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// Detached is what happened to a program started with Detach.
type Detached struct {
	// Exited is true when it stopped within the grace period, which means it failed to start.
	Exited bool
	Code   int
	// LogTail is the end of its output, set when it exited.
	LogTail string
}

// Detach starts a program that keeps running after this one exits, such as an emulator. Its
// output goes to logPath. It waits grace to see whether the program stays up. The error is set
// only when the program could not be started; Missing reports that it was not found.
func Detach(argv []string, logPath string, grace time.Duration) (result Detached, missing bool, err error) {
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return Detached{}, false, err
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr = log, log
	// A new session, so closing the terminal or stopping this program does not stop it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	err = cmd.Start()
	log.Close()
	if err != nil {
		return Detached{}, errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist), err
	}

	// This goroutine reaps the child. It ends when the child does, or with this program; the one
	// buffered slot means it never blocks on a receiver that stopped waiting.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-timer.C:
		return Detached{}, false, nil
	case waitErr := <-done:
		result = Detached{Exited: true, LogTail: tail(logPath, 4000)}
		var exit *exec.ExitError
		if errors.As(waitErr, &exit) {
			result.Code = exit.ExitCode()
		}
		return result, false, nil
	}
}

func tail(path string, max int) string {
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if len(content) > max {
		content = content[len(content)-max:]
	}
	return strings.TrimSpace(string(content))
}

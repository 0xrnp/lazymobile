// Package apperr is the error every backend reports: a stable code, a message for the user, and
// when possible a hint that says what to do next.
package apperr

import (
	"errors"
	"fmt"
	"strings"

	"github.com/0xrnp/lazymobile/internal/run"
)

// Code classifies a failure. Callers branch on it; the message is only for people.
type Code string

const (
	ToolMissing   Code = "tool_missing"
	Timeout       Code = "timeout"
	CommandFailed Code = "command_failed"
	ParseFailed   Code = "parse_failed"
	Unsupported   Code = "unsupported"
	NotFound      Code = "not_found"
	InvalidState  Code = "invalid_state"
	// Usage is a command typed wrongly, such as a missing argument or an unknown flag.
	Usage Code = "usage"
)

// Error is a failure with enough context to show and to act on.
type Error struct {
	Code     Code     `json:"code"`
	Message  string   `json:"message"`
	Hint     string   `json:"hint,omitempty"`
	Argv     []string `json:"argv,omitempty"`
	ExitCode int      `json:"exit_code,omitempty"`
	Stderr   string   `json:"stderr,omitempty"`
}

func (e *Error) Error() string {
	if e.Hint == "" {
		return e.Message
	}
	return e.Message + "\n" + e.Hint
}

// New builds an error with a code and message.
func New(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// WithHint returns the same error with a hint set.
func (e *Error) WithHint(format string, args ...any) *Error {
	e.Hint = fmt.Sprintf(format, args...)
	return e
}

// CodeOf is the code of err, or "" when err is not an *Error.
func CodeOf(err error) Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// FromResult turns a finished command into an error, or nil when it succeeded. missingHint says
// how to install the tool, and is used when the command could not be started.
func FromResult(res run.Result, argv []string, missingHint string) error {
	name := "command"
	if len(argv) > 0 {
		name = baseName(argv[0])
	}
	switch {
	case res.Missing:
		return &Error{Code: ToolMissing, Message: name + " was not found", Hint: missingHint, Argv: argv}
	case res.Err != nil:
		return &Error{Code: CommandFailed, Message: name + " could not be started: " + res.Err.Error(), Argv: argv}
	case res.TimedOut:
		return &Error{Code: Timeout, Message: name + " did not answer in time", Argv: argv, Stderr: res.Stderr}
	case res.Code != 0:
		return &Error{
			Code:     CommandFailed,
			Message:  fmt.Sprintf("%s failed: %s", name, firstLine(res.Stderr, res.Stdout, fmt.Sprintf("exit code %d", res.Code))),
			Argv:     argv,
			ExitCode: res.Code,
			Stderr:   res.Stderr,
		}
	}
	return nil
}

func baseName(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}

// firstLine is the first non-blank line of the first text that has one.
func firstLine(texts ...string) string {
	for _, text := range texts {
		for _, line := range strings.Split(text, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				return line
			}
		}
	}
	return ""
}

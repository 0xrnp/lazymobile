package apperr

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/0xrnp/lazymobile/internal/run"
)

func TestFromResultGivesEachFailureItsCode(t *testing.T) {
	argv := []string{"/usr/bin/adb", "devices"}
	tests := []struct {
		name string
		res  run.Result
		code Code
		text string
	}{
		{"a tool that is not installed", run.Result{Missing: true}, ToolMissing, "adb was not found"},
		{"a tool that cannot start", run.Result{Err: errors.New("permission denied")}, CommandFailed, "adb could not be started: permission denied"},
		{"a tool that does not answer", run.Result{TimedOut: true}, Timeout, "adb did not answer in time"},
		{"a tool that fails", run.Result{Code: 1, Stderr: "\n  error: no devices\nmore\n"}, CommandFailed, "adb failed: error: no devices"},
		{"a failure with only output on stdout", run.Result{Code: 2, Stdout: "Failure [X]"}, CommandFailed, "adb failed: Failure [X]"},
		{"a failure that says nothing", run.Result{Code: 3}, CommandFailed, "adb failed: exit code 3"},
	}
	for _, tt := range tests {
		err := FromResult(tt.res, argv, "Install the SDK.")
		if CodeOf(err) != tt.code || !strings.Contains(err.Error(), tt.text) {
			t.Errorf("%s: error = %v, want %s containing %q", tt.name, err, tt.code, tt.text)
		}
	}
}

func TestFromResultOfASuccessIsNil(t *testing.T) {
	if err := FromResult(run.Result{Stdout: "ok"}, []string{"adb"}, ""); err != nil {
		t.Errorf("error = %v", err)
	}
}

func TestFromResultKeepsWhatWasRunAndWhatItSaid(t *testing.T) {
	argv := []string{"xcrun", "simctl", "boot", "X"}
	err := FromResult(run.Result{Code: 149, Stderr: "Unable to boot"}, argv, "")
	var e *Error
	if !errors.As(err, &e) || e.ExitCode != 149 || e.Stderr != "Unable to boot" || strings.Join(e.Argv, " ") != "xcrun simctl boot X" {
		t.Errorf("error = %+v", err)
	}
	// Only a missing tool has a hint to give, which is how to install it.
	missing := FromResult(run.Result{Missing: true}, argv, "Install Xcode from the App Store.")
	if !errors.As(missing, &e) || e.Hint != "Install Xcode from the App Store." {
		t.Errorf("missing tool = %+v", missing)
	}
	var failed *Error
	if errors.As(FromResult(run.Result{Code: 1}, argv, "Install Xcode."), &failed) && failed.Hint != "" {
		t.Errorf("a failure got the install hint: %q", failed.Hint)
	}
}

func TestErrorTextShowsTheHintOnTheLineAfter(t *testing.T) {
	err := New(NotFound, "No device matches %q", "pixel").WithHint("Run `lazymobile list`.")
	if got, want := err.Error(), "No device matches \"pixel\"\nRun `lazymobile list`."; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if got := New(Timeout, "slow").Error(); got != "slow" {
		t.Errorf("without a hint, Error() = %q", got)
	}
}

func TestCodeOfFindsTheCodeThroughWrapping(t *testing.T) {
	err := fmt.Errorf("while booting: %w", New(InvalidState, "not running"))
	if CodeOf(err) != InvalidState {
		t.Errorf("wrapped: %q", CodeOf(err))
	}
	if CodeOf(errors.New("plain")) != "" || CodeOf(nil) != "" {
		t.Error("an error that is not ours has no code")
	}
}

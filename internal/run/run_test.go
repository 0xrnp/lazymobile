//go:build unix

package run

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunReportsOutputAndExitCode(t *testing.T) {
	res := Exec{}.Run(context.Background(), []string{"sh", "-c", "echo out; echo err >&2; exit 3"}, Options{})
	if res.Stdout != "out\n" || res.Stderr != "err\n" || res.Code != 3 || res.Missing || res.TimedOut {
		t.Errorf("result = %+v", res)
	}
}

func TestRunReportsAMissingProgram(t *testing.T) {
	res := Exec{}.Run(context.Background(), []string{"lazymobile-no-such-tool"}, Options{})
	if !res.Missing {
		t.Errorf("result = %+v, want Missing", res)
	}
}

func TestRunPassesStdinAndEnv(t *testing.T) {
	res := Exec{}.Run(context.Background(), []string{"sh", "-c", `read line; echo "$line $EXTRA"`},
		Options{Stdin: "hello\n", Env: []string{"EXTRA=there"}})
	if strings.TrimSpace(res.Stdout) != "hello there" {
		t.Errorf("stdout = %q", res.Stdout)
	}
}

// A timed out command must not leave its children running: xcrun and adb both start some.
func TestTimeoutStopsTheWholeProcessGroup(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "child-survived")
	script := `(sleep 1; touch "` + marker + `") & wait`
	start := time.Now()
	res := Exec{}.Run(context.Background(), []string{"sh", "-c", script}, Options{Timeout: 200 * time.Millisecond})
	if !res.TimedOut {
		t.Fatalf("result = %+v, want TimedOut", res)
	}
	if elapsed := time.Since(start); elapsed > 900*time.Millisecond {
		t.Errorf("took %v, the timeout did not stop it", elapsed)
	}
	time.Sleep(1200 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Error("the child process outlived the timeout")
	}
}

func TestDetachReportsAProgramThatExitsAtOnce(t *testing.T) {
	log := filepath.Join(t.TempDir(), "out.log")
	result, missing, err := Detach([]string{"sh", "-c", "echo PANIC: no image; exit 1"}, log, 2*time.Second)
	if err != nil || missing {
		t.Fatalf("err = %v, missing = %v", err, missing)
	}
	if !result.Exited || result.Code != 1 || !strings.Contains(result.LogTail, "PANIC: no image") {
		t.Errorf("result = %+v", result)
	}
}

func TestDetachLeavesALongRunningProgramAlone(t *testing.T) {
	log := filepath.Join(t.TempDir(), "out.log")
	result, missing, err := Detach([]string{"sleep", "2"}, log, 100*time.Millisecond)
	if err != nil || missing || result.Exited {
		t.Errorf("result = %+v, missing = %v, err = %v", result, missing, err)
	}
}

func TestDetachReportsAMissingProgram(t *testing.T) {
	_, missing, err := Detach([]string{"lazymobile-no-such-tool"}, filepath.Join(t.TempDir(), "out.log"), time.Second)
	if err == nil || !missing {
		t.Errorf("missing = %v, err = %v", missing, err)
	}
}

func TestInterruptEndsACommandThatCleansUpOnSIGINT(t *testing.T) {
	script := `trap 'echo finished; exit 0' INT; while true; do sleep 0.1; done`
	res := Exec{}.Run(context.Background(), []string{"sh", "-c", script}, Options{Timeout: 500 * time.Millisecond, Interrupt: true})
	if !res.TimedOut || !strings.Contains(res.Stdout, "finished") {
		t.Errorf("an interrupted command should run its cleanup: %+v", res)
	}
}

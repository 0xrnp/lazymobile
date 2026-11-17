package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/0xrnp/lazymobile/internal/actions"
	"github.com/0xrnp/lazymobile/internal/device"
)

func withActions(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	dir := t.TempDir()
	e.app.Actions = func() ([]actions.Action, error) {
		return []actions.Action{
			{Name: "Say", Command: []string{"echo", "on", "{device_id}"}, Dir: dir},
			{Name: "Fail", Command: []string{"sh", "-c", "exit 3"}, Dir: dir},
			{Name: "Missing", Command: []string{"lazymobile-no-such-program"}, Dir: dir},
			{Name: "Reset", Command: []string{"echo", "{app}"}, Dir: dir},
			{Name: "Droid", Platform: device.Android, Command: []string{"echo", "x"}, Dir: dir},
		}, nil
	}
	return e
}

func TestActionShowsTheCommandAndNeedsYes(t *testing.T) {
	e := withActions(t)
	code := e.run("action", "iPhone 16 Pro", "Say")
	if code != ExitUsage || !strings.Contains(e.errs.String(), "echo on 00000000-0000-4000-8000-0000000000A2") ||
		!strings.Contains(e.errs.String(), "--yes") {
		t.Errorf("code %d, stderr %q", code, e.errs.String())
	}
	if e.out.Len() != 0 {
		t.Errorf("the command ran without --yes: %q", e.out.String())
	}
}

func TestActionRunsWithTheDeviceFilledIn(t *testing.T) {
	e := withActions(t)
	if code := e.run("action", "iPhone 16 Pro", "Say", "--yes"); code != ExitOK ||
		!strings.Contains(e.out.String(), "on 00000000-0000-4000-8000-0000000000A2") {
		t.Errorf("code %d, stdout %q, stderr %q", code, e.out.String(), e.errs.String())
	}
}

func TestActionJSONKeepsTheCommandOutOffStdout(t *testing.T) {
	e := withActions(t)
	if code := e.run("action", "iPhone 16 Pro", "Say", "--yes", "--json"); code != ExitOK {
		t.Fatalf("code %d, stderr %q", code, e.errs.String())
	}
	var got result
	if err := json.Unmarshal(e.out.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not one JSON object: %v: %q", err, e.out.String())
	}
	if got.Name != "Say" || len(got.Command) != 3 {
		t.Errorf("got %+v", got)
	}
}

func TestActionFailures(t *testing.T) {
	e := withActions(t)
	if code := e.run("action", "iPhone 16 Pro", "Fail", "--yes"); code != ExitFailure || !strings.Contains(e.errs.String(), "code 3") {
		t.Errorf("a failing command: code %d, stderr %q", code, e.errs.String())
	}
	e.errs.Reset()
	if code := e.run("action", "iPhone 16 Pro", "Missing", "--yes"); code != ExitFailure || !strings.Contains(e.errs.String(), "not installed") {
		t.Errorf("a missing program: code %d, stderr %q", code, e.errs.String())
	}
	e.errs.Reset()
	if code := e.run("action", "iPhone 16 Pro", "Nope", "--yes"); code != ExitFailure || !strings.Contains(e.errs.String(), "no action is named") {
		t.Errorf("an unknown name: code %d, stderr %q", code, e.errs.String())
	}
	e.errs.Reset()
	if code := e.run("action", "iPhone 16 Pro", "Reset", "--yes"); code == ExitOK || !strings.Contains(e.errs.String(), "--app") {
		t.Errorf("a missing app: code %d, stderr %q", code, e.errs.String())
	}
	e.errs.Reset()
	if code := e.run("action", "iPhone 16 Pro", "Droid", "--yes"); code == ExitOK || !strings.Contains(e.errs.String(), "is for android") {
		t.Errorf("the wrong platform: code %d, stderr %q", code, e.errs.String())
	}
}

func TestActionList(t *testing.T) {
	e := withActions(t)
	if code := e.run("action", "--list"); code != ExitOK || !strings.Contains(e.out.String(), "echo on {device_id}") ||
		!strings.Contains(e.out.String(), "[android only]") {
		t.Errorf("code %d, stdout %q", code, e.out.String())
	}
	e = newEnv(t)
	if code := e.run("action", "--list"); code != ExitOK || !strings.Contains(e.out.String(), "No actions") {
		t.Errorf("no file: code %d, stdout %q", code, e.out.String())
	}
	e = newEnv(t)
	if code := e.run("action", "--list", "--json"); code != ExitOK || strings.TrimSpace(e.out.String()) != "[]" {
		t.Errorf("no file as JSON: code %d, stdout %q", code, e.out.String())
	}
}

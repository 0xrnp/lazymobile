package cli

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/0xrnp/lazymobile/internal/doctor"
)

func withFixes(t *testing.T) (*env, *[][]string, *int) {
	t.Helper()
	e := newEnv(t)
	e.report = doctor.Report{Problems: []doctor.Problem{
		{Code: "xcode_license", Platform: "ios", Severity: doctor.Unavailable, Message: "The Xcode license has not been accepted",
			Fix: []string{"sudo", "xcodebuild", "-license", "accept"}},
		{Code: "idevicesyslog_missing", Platform: "ios", Severity: doctor.Optional, Message: "needs idevicesyslog",
			Fix: []string{"brew", "install", "libimobiledevice"}},
		{Code: "java_missing", Platform: "android", Severity: doctor.Optional, Message: "No JDK"},
	}}
	var ran [][]string
	checks := 0
	e.app.Doctor = func(context.Context) doctor.Report { checks++; return e.report }
	e.app.RunFix = func(_ context.Context, argv []string) error { ran = append(ran, argv); return nil }
	return e, &ran, &checks
}

func TestDoctorMentionsFixOnlyWhenThereIsOne(t *testing.T) {
	e, ran, _ := withFixes(t)
	if code := e.run("doctor"); code != ExitOK || !strings.Contains(e.out.String(), "doctor --fix") || len(*ran) != 0 {
		t.Errorf("code %d, ran %v, out %q", code, *ran, e.out.String())
	}
	e.report.Problems = e.report.Problems[2:]
	e.run("doctor")
	if strings.Contains(e.out.String(), "doctor --fix") {
		t.Errorf("mentioned --fix with nothing to fix: %q", e.out.String())
	}
}

func TestFixOutsideATerminalOnlyPrintsTheCommands(t *testing.T) {
	e, ran, _ := withFixes(t)
	if code := e.run("doctor", "--fix"); code != ExitOK {
		t.Fatalf("code %d", code)
	}
	if len(*ran) != 0 {
		t.Errorf("ran %v without a terminal to ask in", *ran)
	}
	for _, want := range []string{"sudo xcodebuild -license accept", "brew install libimobiledevice", "Add --yes"} {
		if !strings.Contains(e.out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, e.out.String())
		}
	}
}

func TestFixAsksBeforeEachCommand(t *testing.T) {
	e, ran, checks := withFixes(t)
	e.app.Interactive = true
	e.app.Stdin = strings.NewReader("n\ny\n")
	if code := e.run("doctor", "--fix"); code != ExitOK {
		t.Fatalf("code %d", code)
	}
	if want := [][]string{{"brew", "install", "libimobiledevice"}}; !reflect.DeepEqual(*ran, want) {
		t.Errorf("ran %v, want only the one answered y", *ran)
	}
	if !strings.Contains(e.out.String(), "Skipped.") || !strings.Contains(e.out.String(), "Checking again") || *checks != 2 {
		t.Errorf("checks %d, output:\n%s", *checks, e.out.String())
	}
}

func TestFixWithNothingAnsweredRunsNothing(t *testing.T) {
	e, ran, checks := withFixes(t)
	e.app.Interactive = true
	e.app.Stdin = strings.NewReader("")
	e.run("doctor", "--fix")
	if len(*ran) != 0 || *checks != 1 {
		t.Errorf("ran %v, checked %d times", *ran, *checks)
	}
}

func TestFixYesRunsEverythingAndKeepsGoingAfterAFailure(t *testing.T) {
	e, ran, _ := withFixes(t)
	e.app.RunFix = func(_ context.Context, argv []string) error {
		*ran = append(*ran, argv)
		return errors.New("exit status 1")
	}
	if code := e.run("doctor", "--fix", "--yes"); code != ExitOK || len(*ran) != 2 || !strings.Contains(e.out.String(), "It failed: exit status 1") {
		t.Errorf("code %d, ran %v, out %q", code, *ran, e.out.String())
	}
}

func TestFixFlagMistakes(t *testing.T) {
	e, _, _ := withFixes(t)
	if code := e.run("doctor", "--fix", "--json"); code != ExitUsage {
		t.Errorf("--fix with --json: code %d", code)
	}
	if code := e.run("doctor", "--yes"); code != ExitUsage {
		t.Errorf("--yes alone: code %d", code)
	}
}

func TestJSONReportCarriesTheFixCommand(t *testing.T) {
	e, _, _ := withFixes(t)
	if code := e.run("doctor", "--json"); code != ExitOK || !strings.Contains(e.out.String(), `"brew"`) {
		t.Errorf("code %d, out %q", code, e.out.String())
	}
}

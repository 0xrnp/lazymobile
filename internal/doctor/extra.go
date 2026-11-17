package doctor

import (
	"context"
	"os/exec"
)

// EAS is the Expo project's eas command, as far as doctor needs to know it.
type EAS interface {
	// ConfiguredCommand is the eas command from .lazymobile.json, or nil. Such a command can come
	// from a repository someone else wrote, so doctor never runs it.
	ConfiguredCommand() []string
	Available() bool
	LoginCommand() []string
	// LoggedIn is false, with no error, when nobody is logged in.
	LoggedIn(ctx context.Context) (bool, error)
}

// Extra is what doctor checks beyond the platform tools. It depends on what is connected and on
// the folder lazymobile was started in.
type Extra struct {
	// IPhoneConnected is whether a physical iPhone or iPad is listed.
	IPhoneConnected bool
	// EAS is nil when the folder is not an Expo project.
	EAS EAS
	// LookPath finds a program on PATH. Nil means the real one.
	LookPath func(string) (string, error)
}

func (x Extra) has(program string) bool {
	look := x.LookPath
	if look == nil {
		look = exec.LookPath
	}
	_, err := look(program)
	return err == nil
}

// CheckExtra looks at idevicesyslog when an iPhone is connected, and at the Expo login inside an
// Expo project.
func CheckExtra(ctx context.Context, x Extra) Report {
	var report Report
	if x.IPhoneConnected {
		if x.has("idevicesyslog") {
			report.Found = append(report.Found, Found{"idevicesyslog", "installed, for the system log of an iPhone"})
		} else {
			var fix []string
			if x.has("brew") {
				fix = []string{"brew", "install", "libimobiledevice"}
			}
			report.Problems = append(report.Problems, Problem{Code: "idevicesyslog_missing", Platform: "ios", Severity: Optional,
				Message: "An iPhone is connected, and its live system log needs idevicesyslog",
				Hint:    "Install it with: brew install libimobiledevice", Fix: fix})
		}
	}
	if x.EAS == nil {
		return report
	}
	problem := func(code string, severity Severity, message, hint string, fix []string) {
		report.Problems = append(report.Problems, Problem{Code: code, Platform: "expo", Severity: severity, Message: message, Hint: hint, Fix: fix})
	}
	switch {
	case len(x.EAS.ConfiguredCommand()) > 0:
		report.Found = append(report.Found, Found{"eas", "its command is set in .lazymobile.json, so doctor did not run it"})
	case !x.EAS.Available():
		var fix []string
		if x.has("npm") {
			fix = []string{"npm", "install", "--global", "eas-cli"}
		}
		problem("eas_missing", Optional, "This is an Expo project, and eas was not found",
			"Install it with: npm install --global eas-cli", fix)
	default:
		loggedIn, err := x.EAS.LoggedIn(ctx)
		switch {
		case err != nil:
			problem("eas_check_failed", Optional, "Could not check the Expo login", err.Error(), nil)
		case !loggedIn:
			problem("eas_not_logged_in", Optional, "You are not logged in to Expo, so the builds pane cannot load",
				"Run: eas login", x.EAS.LoginCommand())
		default:
			report.Found = append(report.Found, Found{"eas", "logged in to Expo"})
		}
	}
	return report
}

// Add puts what other found and its problems after this report's.
func (r *Report) Add(other Report) {
	r.Found = append(r.Found, other.Found...)
	r.Problems = append(r.Problems, other.Problems...)
}

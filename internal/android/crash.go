package android

import (
	"context"
	"regexp"
	"strings"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
)

// dropboxCrashTags are the dropbox entries that hold a crash: of an app the user installed (data_)
// or one that came with the system, and of Java or Kotlin code or of native code.
var dropboxCrashTags = []string{"data_app_crash", "data_app_native_crash", "system_app_crash", "system_app_native_crash"}

// LatestCrash returns the most recent crash of an app on a running emulator or phone. It reads
// logcat's crash buffer first: it has every recent crash, but is emptied when the device restarts.
// Then it reads dropbox, which survives a restart but leaves out a crash that repeats quickly,
// which is how an app in development usually crashes.
func (c *Client) LatestCrash(ctx context.Context, d device.Device, pkg string) (device.CrashReport, error) {
	adb, err := c.runningDevice(d)
	if err != nil {
		return device.CrashReport{}, err
	}
	argv := []string{adb, "-s", d.TargetID(), "logcat", "-b", "crash", "-d", "-v", "threadtime"}
	res := c.run(ctx, argv, actionTimeout)
	if err := apperr.FromResult(res, argv, installHint["adb"]); err != nil {
		return device.CrashReport{}, err
	}
	if report, ok := LatestCrashInLogcat(res.Stdout, pkg); ok {
		return report, nil
	}
	var latest device.CrashReport
	for _, tag := range dropboxCrashTags {
		argv := []string{adb, "-s", d.TargetID(), "shell", "dumpsys", "dropbox", "--print", tag}
		res := c.run(ctx, argv, actionTimeout)
		if err := apperr.FromResult(res, argv, installHint["adb"]); err != nil {
			return device.CrashReport{}, err
		}
		// The dates are written year first, so the later one sorts last.
		if report, ok := LatestCrashInDropbox(res.Stdout, pkg); ok && report.When > latest.When {
			latest = report
		}
	}
	if latest.Text == "" {
		return device.CrashReport{}, apperr.New(apperr.NotFound, "No crash of %s found on %s", pkg, d.Name).
			WithHint("Android keeps recent crashes until the device restarts, and some older ones after that.")
	}
	return latest, nil
}

// logcatLine is a line of `logcat -v threadtime`: date, time, pid, tid, level, tag, then the message.
var logcatLine = regexp.MustCompile(`^(\d\d-\d\d \d\d:\d\d:\d\d\.\d+)\s+(\d+)\s+\d+\s+[VDIWEF]\s+(.*?)\s*: (.*)$`)

var (
	javaProcess   = regexp.MustCompile(`^Process: (\S+?), PID: \d+`)
	nativeProcess = regexp.MustCompile(`>>> (\S+) <<<`)
)

// LatestCrashInLogcat finds the last crash of pkg in logcat's crash buffer. A Java or Kotlin crash
// is AndroidRuntime's "FATAL EXCEPTION" and the lines after it from the same process. A native
// crash is the report crash_dump writes under the DEBUG tag, from its row of asterisks on.
func LatestCrashInLogcat(text, pkg string) (device.CrashReport, bool) {
	type crash struct {
		app, when string
		lines     []string
	}
	var crashes []*crash
	// open is the crash each tag and process is writing, so two crashes written at the same time
	// keep all their lines.
	open := map[string]*crash{}
	for _, raw := range strings.Split(text, "\n") {
		m := logcatLine.FindStringSubmatch(strings.TrimRight(raw, "\r"))
		if m == nil {
			continue
		}
		when, pid, tag, message := m[1], m[2], m[3], m[4]
		key := tag + " " + pid
		if tag == "AndroidRuntime" && strings.HasPrefix(message, "FATAL EXCEPTION") || tag == "DEBUG" && strings.HasPrefix(message, "*** ***") {
			open[key] = &crash{when: when}
			crashes = append(crashes, open[key])
		}
		current := open[key]
		if current == nil {
			continue
		}
		if current.app == "" {
			if p := javaProcess.FindStringSubmatch(message); p != nil {
				current.app = p[1]
			} else if p := nativeProcess.FindStringSubmatch(message); p != nil {
				current.app = p[1]
			}
		}
		current.lines = append(current.lines, message)
	}
	for i := len(crashes) - 1; i >= 0; i-- {
		if c := crashes[i]; c.app == pkg {
			return device.CrashReport{App: pkg, When: c.when, Text: strings.Join(c.lines, "\n")}, true
		}
	}
	return device.CrashReport{}, false
}

// dropboxHeader starts an entry of `dumpsys dropbox --print`: its date and time, then its tag.
var dropboxHeader = regexp.MustCompile(`^(\d{4}-\d\d-\d\d \d\d:\d\d:\d\d) \S+ \(`)

// LatestCrashInDropbox finds the last entry of `dumpsys dropbox --print <tag>` whose process is pkg.
// Each entry starts after a line of equals signs, with its date and tag, then "Process: <name>".
func LatestCrashInDropbox(text, pkg string) (device.CrashReport, bool) {
	var found device.CrashReport
	for _, entry := range strings.Split(text, "========================================") {
		lines := strings.Split(strings.Trim(strings.ReplaceAll(entry, "\r", ""), "\n"), "\n")
		if len(lines) < 2 {
			continue
		}
		m := dropboxHeader.FindStringSubmatch(lines[0])
		if m == nil {
			continue
		}
		mine := false
		for _, line := range lines[1:] {
			if name, ok := strings.CutPrefix(line, "Process: "); ok {
				mine = strings.TrimSpace(name) == pkg
				break
			}
		}
		if mine && m[1] >= found.When {
			found = device.CrashReport{App: pkg, When: m[1], Text: strings.TrimRight(strings.Join(lines[1:], "\n"), "\n")}
		}
	}
	return found, found.Text != ""
}

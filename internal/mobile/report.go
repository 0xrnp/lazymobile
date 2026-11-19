package mobile

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/logs"
)

// ReportOptions says what goes into a bug report. Zero values mean the defaults.
type ReportOptions struct {
	// Dir is the new folder the report is saved in. It must not exist yet.
	Dir string
	// Since is how far back the log goes (5 minutes), and Lines how many of its last lines are kept
	// (200).
	Since time.Duration
	Lines int
	// Version is lazymobile's own version, written in the summary. Now is the time of the report.
	Version string
	Now     time.Time
}

// Report is a bug report that was saved.
type Report struct {
	// Path is the folder.
	Path string `json:"path"`
	// Included are the files in it, and Skipped says for each part that could not be saved why not.
	Included []string `json:"included"`
	Skipped  []string `json:"skipped,omitempty"`
}

const (
	defaultReportSince = 5 * time.Minute
	defaultReportLines = 200
)

// Report saves what is needed to report a bug in an app on a running simulator, emulator or Android
// phone: a screenshot, the last lines of the app's log, its latest crash report, and the device
// and app version. Each part is saved on its own, so one that fails is listed in Skipped and does
// not stop the others. The folder is new and readable only by its owner, since a screen and a log
// can show personal data.
func (m *Manager) Report(ctx context.Context, d device.Device, app string, opts ReportOptions) (Report, error) {
	switch {
	case !d.Ready():
		return Report{}, apperr.New(apperr.InvalidState, "%s is not running", d.Name).WithHint("Boot or connect it first.")
	case d.Platform == device.IOS && d.Kind == device.Physical:
		return Report{}, apperr.New(apperr.Unsupported, "A report from an iPhone is not supported yet: it has no screenshot or crash report here").
			WithHint("Use a simulator, an emulator or an Android phone.")
	case !logs.ValidAppID(app):
		return Report{}, apperr.New(apperr.Usage, "%s does not look like an app id", app)
	}
	if opts.Since == 0 {
		opts.Since = defaultReportSince
	}
	if opts.Lines == 0 {
		opts.Lines = defaultReportLines
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	dir, err := checkNewFile(opts.Dir)
	if err != nil {
		return Report{}, err
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return Report{}, apperr.New(apperr.CommandFailed, "Could not make the folder %s: %v", dir, err)
	}
	report := Report{Path: dir}
	skip := func(part string, err error) {
		line, _, _ := strings.Cut(err.Error(), "\n")
		report.Skipped = append(report.Skipped, part+": "+line)
	}

	if _, err := m.Screenshot(ctx, d, filepath.Join(dir, "screenshot.png")); err != nil {
		skip("screenshot", err)
	} else {
		report.Included = append(report.Included, "screenshot.png")
	}

	if text, err := m.reportLog(ctx, d, app, opts); err != nil {
		skip("log", err)
	} else if _, err := SaveText(filepath.Join(dir, "log.txt"), text); err != nil {
		skip("log", err)
	} else {
		report.Included = append(report.Included, "log.txt")
	}

	if crash, err := m.Crash(ctx, d, app); err != nil {
		skip("crash report", err)
	} else {
		text := crash.Text
		if crash.Path != "" {
			text += "\n\nFull report: " + crash.Path
		}
		if _, err := SaveText(filepath.Join(dir, "crash.txt"), text); err != nil {
			skip("crash report", err)
		} else {
			report.Included = append(report.Included, "crash.txt")
		}
	}

	found := device.App{ID: app}
	if installed, err := m.InstalledApps(ctx, d); err != nil {
		skip("app version", err)
	} else {
		known := false
		for _, a := range installed {
			if a.ID == app {
				found, known = a, true
			}
		}
		if !known {
			skip("app version", fmt.Errorf("%s is not in the installed list", app))
		}
	}

	if _, err := SaveText(filepath.Join(dir, "summary.txt"), reportSummary(d, found, opts, report)); err != nil {
		skip("summary", err)
	} else {
		report.Included = append(report.Included, "summary.txt")
	}
	return report, nil
}

// reportLog reads the last lines of the app's log from the last opts.Since.
func (m *Manager) reportLog(ctx context.Context, d device.Device, app string, opts ReportOptions) (string, error) {
	mode := logs.System
	if d.Platform == device.Android {
		mode = logs.Logcat
	}
	var last []string
	total := 0
	session := logs.Start(ctx, m.LogDeps(), logs.Spec{Device: d, Mode: mode, App: app, Dump: true, Since: opts.Since}, logs.Handlers{
		Lines: func(lines []logs.Line) {
			for _, line := range lines {
				total++
				last = append(last, line.Text)
				if len(last) > opts.Lines {
					last = last[1:]
				}
			}
		},
		Status: func(string) {},
	})
	if err := session.Wait(); err != nil {
		return "", err
	}
	if total == 0 {
		return fmt.Sprintf("No log lines from %s in the last %s.\n", app, opts.Since), nil
	}
	head := ""
	if total > len(last) {
		head = fmt.Sprintf("(the last %d of %d lines from the last %s)\n", len(last), total, opts.Since)
	}
	return head + strings.Join(last, "\n") + "\n", nil
}

func reportSummary(d device.Device, app device.App, opts ReportOptions, r Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Bug report made by lazymobile %s on %s\n\n", opts.Version, opts.Now.Format("2006-01-02 15:04:05 -0700"))
	fmt.Fprintf(&b, "Device:   %s (%s)\n", d.Name, d.TargetID())
	system := []string{string(d.Kind)}
	if label := d.OSLabel(); label != "" {
		system = []string{label, string(d.Kind)}
	}
	fmt.Fprintf(&b, "          %s\n", strings.Join(system, ", "))
	fmt.Fprintf(&b, "App:      %s", app.ID)
	if app.Name != "" {
		fmt.Fprintf(&b, " (%s)", app.Name)
	}
	b.WriteString("\n")
	if v := app.VersionLabel(); v != "" {
		fmt.Fprintf(&b, "Version:  %s\n", v)
	}
	b.WriteString("\nFiles: " + strings.Join(append(append([]string{}, r.Included...), "summary.txt"), ", ") + "\n")
	for _, s := range r.Skipped {
		b.WriteString("Not included, " + s + "\n")
	}
	b.WriteString("\nThe screenshot and the log can show personal data. Look at them before you share this folder.\n")
	return b.String()
}

// ReportName is a folder name for a bug report on d made at now.
func ReportName(d device.Device, now time.Time) string {
	name := strings.TrimSuffix(mediaName(d, now, "x"), ".x")
	return "lazymobile-report-" + strings.TrimPrefix(name, "lazymobile-")
}

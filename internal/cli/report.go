package cli

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/0xrnp/lazymobile/internal/mobile"
	"github.com/0xrnp/lazymobile/internal/state"
)

// bugReport saves a bug report of an app on a device in a new folder.
func (a *App) bugReport(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	out := fs.String("out", "", "")
	since := fs.Duration("since", 0, "")
	lines := fs.Int("lines", 0, "")
	asJSON := fs.Bool("json", false, "")
	d, app, err := a.deviceAndApp(ctx, fs, args)
	if err != nil {
		return err
	}
	if *since < 0 || *lines < 0 {
		return usageError{"--since and --lines cannot be negative"}
	}
	now := time.Now()
	if *out == "" {
		*out = mobile.ReportName(d, now)
	}
	saved, err := a.Manager.Report(ctx, d, app, mobile.ReportOptions{
		Dir: *out, Since: *since, Lines: *lines, Version: a.Version, Now: now,
	})
	if err != nil {
		return err
	}
	_ = a.State.Update(func(f *state.File) { f.RememberApp(d.Platform, app) })
	var text strings.Builder
	fmt.Fprintf(&text, "Saved %s\n  %s", saved.Path, strings.Join(saved.Included, ", "))
	for _, s := range saved.Skipped {
		fmt.Fprintf(&text, "\n  not included, %s", s)
	}
	text.WriteString("\nThe screenshot and the log can show personal data. Look at them before you share it.")
	return a.done(*asJSON, result{Action: "report", Device: d, App: app, Path: saved.Path, Report: &saved, Warnings: saved.Skipped}, text.String())
}

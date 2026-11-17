// Command lazymobile lists and drives iOS simulators, Android emulators and phones, and shows
// their logs, in a terminal dashboard or as plain commands.
package main

import (
	"github.com/charmbracelet/x/term"

	"context"
	"os"
	"runtime"
	"runtime/debug"

	"github.com/0xrnp/lazymobile/internal/actions"
	"github.com/0xrnp/lazymobile/internal/android"
	"github.com/0xrnp/lazymobile/internal/cli"
	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/devicesets"
	"github.com/0xrnp/lazymobile/internal/doctor"
	"github.com/0xrnp/lazymobile/internal/eas"
	"github.com/0xrnp/lazymobile/internal/mobile"
	"github.com/0xrnp/lazymobile/internal/run"
	"github.com/0xrnp/lazymobile/internal/state"
	"github.com/0xrnp/lazymobile/internal/tui"
)

// version is set by the release build.
var version = "dev"

// moduleVersion is the version go install recorded, such as v0.1.0, for a binary that was not
// built by the release.
func moduleVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return version
}

// project is the Expo project of the current folder, when there is one.
// isTerminal reports whether f is a terminal, where a question can be asked.
func isTerminal(f *os.File) bool { return term.IsTerminal(f.Fd()) }

func project(runner run.Runner) (*eas.Client, bool) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, false
	}
	found, ok := eas.Detect(cwd, nil)
	if !ok {
		return nil, false
	}
	return &eas.Client{Runner: runner, Project: found}, true
}

func main() {
	if version == "dev" {
		version = moduleVersion()
	}
	runner := run.Exec{}
	options := android.Options{}
	store := state.Store{Path: state.DefaultPath()}
	manager := mobile.New(runner, options, runtime.GOOS)
	app := &cli.App{
		Manager: manager,
		Version: version,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Dashboard: func(ctx context.Context, m *mobile.Manager) error {
			// A nil interface, not a nil client, tells the dashboard there is no Expo project.
			var builds tui.Builds
			if client, found := project(runner); found {
				builds = client
			}
			folder, _ := os.Getwd()
			return tui.Run(ctx, m, builds, tui.Memory{Store: store, Folder: folder})
		},
		Builds: func() cli.BuildLister {
			if client, found := project(runner); found {
				return client
			}
			return nil
		},
		DeviceSets: func() (devicesets.Sets, error) {
			cwd, err := os.Getwd()
			if err != nil {
				return devicesets.Sets{}, nil
			}
			return devicesets.Load(cwd)
		},
		Actions: func() ([]actions.Action, error) {
			cwd, err := os.Getwd()
			if err != nil {
				return nil, nil
			}
			return actions.Load(cwd)
		},
		State: store,
		Doctor: func(ctx context.Context) doctor.Report {
			report := doctor.Check(ctx, runner, options, runtime.GOOS)
			extra := doctor.Extra{}
			devices, _ := manager.List(ctx)
			for _, d := range devices {
				if d.Platform == device.IOS && d.Kind == device.Physical {
					extra.IPhoneConnected = true
				}
			}
			if client, found := project(runner); found {
				extra.EAS = client
			}
			report.Add(doctor.CheckExtra(ctx, extra))
			return report
		},
		Stdin:       os.Stdin,
		Interactive: isTerminal(os.Stdin),
	}
	os.Exit(app.Run(context.Background(), os.Args[1:]))
}

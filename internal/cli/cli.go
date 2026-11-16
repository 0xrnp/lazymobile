// Package cli is the plain, scriptable side of lazymobile: every command prints text or JSON
// and exits. Running with no command opens the dashboard.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/doctor"
	"github.com/0xrnp/lazymobile/internal/eas"
	"github.com/0xrnp/lazymobile/internal/ios"
	"github.com/0xrnp/lazymobile/internal/logs"
	"github.com/0xrnp/lazymobile/internal/mobile"
	"github.com/0xrnp/lazymobile/internal/state"
)

// Exit codes.
const (
	ExitOK      = 0
	ExitFailure = 1
	ExitUsage   = 2
	// ExitInterrupted follows the shell convention for a program stopped with Ctrl-C.
	ExitInterrupted = 130
)

const usage = `lazymobile: simulators, emulators, phones and their logs, from the terminal.

Usage:
  lazymobile                       open the dashboard
  lazymobile list [--json]         list every device
  lazymobile boot <device> [--cold] [--wipe --yes] [--wait] [--no-window] [--json]
  lazymobile shutdown <device> [--keep-simulator-app] [--json]
  lazymobile erase <device> --yes [--json]   erase a simulator, or wipe an emulator's data
  lazymobile delete <device> --yes [--json]
  lazymobile uninstall <device> <app-id> --yes [--json]   remove an app and its data from a running device
  lazymobile install <device> <path> [--json]   an .apk, or an .app folder for a simulator
  lazymobile launch <device> <app-id> [--restart] [--json]   open an app. --restart stops a running copy first
  lazymobile stop <device> <app-id> [--json]    stop a running app. Its data is kept
  lazymobile link <device> <url> [--json]       open a web address or deep link, such as myapp://home
  lazymobile screenshot <device> [--out <file>] [--json]   save a PNG of the screen. Never replaces a file
  lazymobile logs <device> [--app <id> | --last-app] [--mode logcat|system|console] [--level <level>] [--json]
  lazymobile apps <device> [--json]    app ids used before, and the apps installed on the device
  lazymobile id <device> [--json]    the id for adb -s, flutter -d and xcrun
  lazymobile builds [--json] [--limit n]   Expo (EAS) builds of the project in this folder
  lazymobile doctor [--json]       what is installed, what is missing, and how to fix it
  lazymobile version

Once no simulator is running, Simulator or DeviceHub is quit, which closes the window it keeps
open for a simulator that shut down. --keep-simulator-app leaves it open. The dashboard takes
--no-window and --keep-simulator-app too.

App ids given to logs and launch are remembered, newest first, for --last-app, for apps and for the
dashboard's app list.

<device> is an id, an adb serial, a name, or a unique part of a name.
Commands that erase, delete or uninstall need --yes.
Exit codes: 0 success, 1 failure, 2 wrong usage.
With --json, a failure is printed on stdout as {"error": {"code", "message", "hint"}} and the exit
code is the same. See docs/json.md.
`

// App is what the commands need. main fills it in; tests replace parts of it.
type App struct {
	Manager *mobile.Manager
	Version string
	Stdout  io.Writer
	Stderr  io.Writer
	// Dashboard opens the full-screen interface.
	Dashboard func(ctx context.Context, m *mobile.Manager) error
	// Doctor produces the tool report.
	Doctor func(ctx context.Context) doctor.Report
	// Builds returns the Expo project of the current folder, or nil when there is none.
	Builds func() BuildLister
	// State remembers app ids between runs. The zero Store remembers nothing.
	State state.Store
}

// usageError is a mistake in how a command was typed.
type usageError struct{ message string }

func (e usageError) Error() string { return e.message }

// parse reads flags that may come before or after the positional arguments.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	fs.SetOutput(io.Discard)
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, usageError{err.Error()}
		}
		if fs.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// reportedError is a failure whose details were already printed, such as a doctor report with
// problems in it. With --json it is not printed a second time.
type reportedError struct{ error }

// jsonRequested reports whether the command line asks for JSON, before any command has parsed it,
// so that even a mistake in the command line can be answered in JSON.
func jsonRequested(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "--":
			return false
		case "--json", "-json", "--json=true", "-json=true":
			return true
		}
	}
	return false
}

// asError is err as the object --json prints: an apperr error as it is, a typing mistake as a
// usage error, and anything else as a failed command.
func asError(err error) *apperr.Error {
	var e *apperr.Error
	var usageErr usageError
	switch {
	case errors.As(err, &e):
		return e
	case errors.As(err, &usageErr):
		return apperr.New(apperr.Usage, "%s", usageErr.message)
	}
	return apperr.New(apperr.CommandFailed, "%s", err.Error())
}

// Run executes one command and returns the exit code.
func (a *App) Run(ctx context.Context, args []string) int {
	err := a.run(ctx, args)
	if err == nil {
		return ExitOK
	}
	if errors.Is(err, context.Canceled) {
		return ExitInterrupted
	}
	var usageErr usageError
	code := ExitFailure
	if errors.As(err, &usageErr) {
		code = ExitUsage
	}
	var reported reportedError
	switch {
	case jsonRequested(args):
		if !errors.As(err, &reported) {
			_ = a.writeJSON(map[string]any{"error": asError(err)})
		}
	case code == ExitUsage:
		fmt.Fprintf(a.Stderr, "lazymobile: %s\n\nRun `lazymobile help` for usage.\n", usageErr.message)
	default:
		fmt.Fprintf(a.Stderr, "lazymobile: %s\n", err)
	}
	return code
}

// result is what a command that changes a device prints with --json.
type result struct {
	OK     bool   `json:"ok"`
	Action string `json:"action"`
	// Device is as it was found, before the action.
	Device   device.Device `json:"device"`
	App      string        `json:"app,omitempty"`
	Path     string        `json:"path,omitempty"`
	URL      string        `json:"url,omitempty"`
	Warnings []string      `json:"warnings,omitempty"`
}

// done reports a finished action: the object with --json, the sentence otherwise.
func (a *App) done(asJSON bool, r result, sentence string) error {
	if asJSON {
		r.OK = true
		return a.writeJSON(r)
	}
	_, err := fmt.Fprintln(a.Stdout, sentence)
	return err
}

func (a *App) run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return a.Dashboard(ctx, a.Manager)
	}
	command, rest := args[0], args[1:]
	switch command {
	case "help", "-h", "--help":
		fmt.Fprint(a.Stdout, usage)
		return nil
	case "version", "--version":
		fmt.Fprintln(a.Stdout, "lazymobile "+a.Version)
		return nil
	case "list":
		return a.list(ctx, rest)
	case "boot":
		return a.boot(ctx, rest)
	case "shutdown":
		return a.shutdown(ctx, rest)
	case "erase":
		return a.destructive(ctx, rest, "erase", "Erased", a.Manager.Erase)
	case "delete":
		return a.destructive(ctx, rest, "delete", "Deleted", a.Manager.Delete)
	case "install":
		return a.install(ctx, rest)
	case "uninstall":
		return a.uninstall(ctx, rest)
	case "launch":
		return a.launch(ctx, rest)
	case "stop":
		return a.stop(ctx, rest)
	case "link":
		return a.link(ctx, rest)
	case "screenshot":
		return a.screenshot(ctx, rest)
	case "logs":
		return a.logs(ctx, rest)
	case "apps":
		return a.apps(ctx, rest)
	case "id":
		return a.id(ctx, rest)
	case "doctor":
		return a.doctor(ctx, rest)
	case "builds":
		return a.builds(ctx, rest)
	}
	if strings.HasPrefix(command, "-") {
		// Flags without a command belong to the dashboard.
		fs := flag.NewFlagSet("lazymobile", flag.ContinueOnError)
		noWindow := fs.Bool("no-window", false, "")
		keepApp := fs.Bool("keep-simulator-app", false, "")
		if positional, err := parse(fs, args); err != nil {
			return err
		} else if len(positional) > 0 {
			return usageError{"unknown command: " + positional[0]}
		}
		a.Manager.OpenWindow = !*noWindow
		a.Manager.QuitSimulatorApp = !*keepApp
		return a.Dashboard(ctx, a.Manager)
	}
	return usageError{"unknown command: " + command}
}

// one parses a command that takes exactly one device.
func (a *App) one(ctx context.Context, fs *flag.FlagSet, args []string, name string) (device.Device, error) {
	positional, err := parse(fs, args)
	if err != nil {
		return device.Device{}, err
	}
	if len(positional) != 1 {
		return device.Device{}, usageError{name + " needs one device"}
	}
	return a.Manager.Find(ctx, positional[0])
}

func (a *App) writeJSON(value any) error {
	encoder := json.NewEncoder(a.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func (a *App) list(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "")
	if positional, err := parse(fs, args); err != nil {
		return err
	} else if len(positional) > 0 {
		return usageError{"list takes no arguments"}
	}
	devices, errs := a.Manager.List(ctx)
	if *asJSON {
		problems := make([]*apperr.Error, 0, len(errs))
		for _, err := range errs {
			problems = append(problems, asError(err))
		}
		if devices == nil {
			devices = []device.Device{}
		}
		return a.writeJSON(map[string]any{"devices": devices, "errors": problems})
	}
	table := tabwriter.NewWriter(a.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "NAME\tOS\tKIND\tSTATE\tID")
	for _, d := range devices {
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n", d.Name, d.OSLabel(), d.Kind, strings.ReplaceAll(string(d.State), "_", " "), d.TargetID())
	}
	table.Flush()
	for _, err := range errs {
		fmt.Fprintf(a.Stderr, "warning: %s\n", err)
	}
	if len(devices) == 0 && len(errs) > 0 {
		return errors.New("no devices could be listed. Run `lazymobile doctor`")
	}
	return nil
}

func (a *App) boot(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("boot", flag.ContinueOnError)
	cold := fs.Bool("cold", false, "")
	wipe := fs.Bool("wipe", false, "")
	wait := fs.Bool("wait", false, "")
	yes := fs.Bool("yes", false, "")
	noWindow := fs.Bool("no-window", false, "")
	asJSON := fs.Bool("json", false, "")
	d, err := a.one(ctx, fs, args, "boot")
	if err != nil {
		return err
	}
	if *wipe && !*yes {
		return usageError{fmt.Sprintf("--wipe erases all data on %s. Add --yes to confirm", d.Name)}
	}
	a.Manager.OpenWindow = !*noWindow
	if err := a.Manager.Boot(ctx, d, mobile.BootOptions{Cold: *cold, Wipe: *wipe, Wait: *wait}); err != nil {
		return err
	}
	state := "is starting"
	if *wait {
		state = "is ready"
	}
	return a.done(*asJSON, result{Action: "boot", Device: d}, fmt.Sprintf("%s %s", d.Name, state))
}

func (a *App) shutdown(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("shutdown", flag.ContinueOnError)
	keepApp := fs.Bool("keep-simulator-app", false, "")
	asJSON := fs.Bool("json", false, "")
	d, err := a.one(ctx, fs, args, "shutdown")
	if err != nil {
		return err
	}
	a.Manager.QuitSimulatorApp = !*keepApp
	err = a.Manager.Shutdown(ctx, d)
	var notQuit *ios.AppNotQuitError
	if err != nil && !errors.As(err, &notQuit) {
		return err
	}
	r := result{Action: "shutdown", Device: d}
	if notQuit != nil {
		r.Warnings = []string{notQuit.Error()}
		if !*asJSON {
			defer fmt.Fprintf(a.Stderr, "warning: %s\n", notQuit)
		}
	}
	return a.done(*asJSON, r, "Shut down "+d.Name)
}

func (a *App) destructive(ctx context.Context, args []string, name, done string, act func(context.Context, device.Device) error) error {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	yes := fs.Bool("yes", false, "")
	asJSON := fs.Bool("json", false, "")
	d, err := a.one(ctx, fs, args, name)
	if err != nil {
		return err
	}
	if !*yes {
		return usageError{fmt.Sprintf("%s removes data on %s. Add --yes to confirm", name, d.Name)}
	}
	if err := act(ctx, d); err != nil {
		return err
	}
	return a.done(*asJSON, result{Action: name, Device: d}, done+" "+d.Name)
}

func (a *App) install(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "")
	positional, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 2 {
		return usageError{"install needs a device and a path"}
	}
	d, err := a.Manager.Find(ctx, positional[0])
	if err != nil {
		return err
	}
	if err := a.Manager.Install(ctx, d, positional[1]); err != nil {
		return err
	}
	return a.done(*asJSON, result{Action: "install", Device: d, Path: positional[1]}, "Installed on "+d.Name)
}

func (a *App) uninstall(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "")
	asJSON := fs.Bool("json", false, "")
	positional, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 2 {
		return usageError{"uninstall needs a device and an app id"}
	}
	d, err := a.Manager.Find(ctx, positional[0])
	if err != nil {
		return err
	}
	if !*yes {
		return usageError{fmt.Sprintf("uninstall removes %s and its data from %s. Add --yes to confirm", positional[1], d.Name)}
	}
	if err := a.Manager.Uninstall(ctx, d, positional[1]); err != nil {
		return err
	}
	return a.done(*asJSON, result{Action: "uninstall", Device: d, App: positional[1]},
		fmt.Sprintf("Uninstalled %s from %s", positional[1], d.Name))
}

// deviceAndApp parses a command that takes a device and an app id.
func (a *App) deviceAndApp(ctx context.Context, fs *flag.FlagSet, args []string) (device.Device, string, error) {
	positional, err := parse(fs, args)
	if err != nil {
		return device.Device{}, "", err
	}
	if len(positional) != 2 {
		return device.Device{}, "", usageError{fs.Name() + " needs a device and an app id"}
	}
	d, err := a.Manager.Find(ctx, positional[0])
	return d, positional[1], err
}

func (a *App) launch(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("launch", flag.ContinueOnError)
	restart := fs.Bool("restart", false, "")
	asJSON := fs.Bool("json", false, "")
	d, app, err := a.deviceAndApp(ctx, fs, args)
	if err != nil {
		return err
	}
	if err := a.Manager.Launch(ctx, d, app, mobile.LaunchOptions{Restart: *restart}); err != nil {
		return err
	}
	_ = a.State.Update(func(f *state.File) { f.RememberApp(d.Platform, app) })
	action, done := "launch", "Launched"
	if *restart {
		action, done = "restart", "Restarted"
	}
	return a.done(*asJSON, result{Action: action, Device: d, App: app}, fmt.Sprintf("%s %s on %s", done, app, d.Name))
}

func (a *App) link(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("link", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "")
	positional, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 2 {
		return usageError{"link needs a device and a link"}
	}
	d, err := a.Manager.Find(ctx, positional[0])
	if err != nil {
		return err
	}
	if err := a.Manager.OpenLink(ctx, d, positional[1]); err != nil {
		return err
	}
	return a.done(*asJSON, result{Action: "link", Device: d, URL: positional[1]}, fmt.Sprintf("Opened %s on %s", positional[1], d.Name))
}

func (a *App) screenshot(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("screenshot", flag.ContinueOnError)
	out := fs.String("out", "", "")
	asJSON := fs.Bool("json", false, "")
	d, err := a.one(ctx, fs, args, "screenshot")
	if err != nil {
		return err
	}
	if *out == "" {
		*out = mobile.ScreenshotName(d, time.Now())
	}
	path, err := a.Manager.Screenshot(ctx, d, *out)
	if err != nil {
		return err
	}
	return a.done(*asJSON, result{Action: "screenshot", Device: d, Path: path}, "Saved "+path)
}

func (a *App) stop(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "")
	d, app, err := a.deviceAndApp(ctx, fs, args)
	if err != nil {
		return err
	}
	if err := a.Manager.Stop(ctx, d, app); err != nil {
		return err
	}
	return a.done(*asJSON, result{Action: "stop", Device: d, App: app}, fmt.Sprintf("Stopped %s on %s", app, d.Name))
}

func (a *App) id(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("id", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "")
	d, err := a.one(ctx, fs, args, "id")
	if err != nil {
		return err
	}
	if *asJSON {
		return a.writeJSON(map[string]any{"device": d, "id": d.TargetID()})
	}
	fmt.Fprintln(a.Stdout, d.TargetID())
	return nil
}

func (a *App) logs(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	app := fs.String("app", "", "")
	lastApp := fs.Bool("last-app", false, "")
	mode := fs.String("mode", "", "")
	levelName := fs.String("level", "", "")
	asJSON := fs.Bool("json", false, "")
	d, err := a.one(ctx, fs, args, "logs")
	if err != nil {
		return err
	}
	if *lastApp {
		if *app != "" {
			return usageError{"give --app or --last-app, not both"}
		}
		recent := a.State.Load().Apps[d.Platform]
		if len(recent) == 0 {
			return apperr.New(apperr.NotFound, "No %s app id has been used yet", platformName(d.Platform)).
				WithHint("Pass --app <id> once, or run `lazymobile apps %q` to see the installed apps.", d.Name)
		}
		*app = recent[0]
		fmt.Fprintln(a.Stderr, "Using "+*app)
	}
	if *app != "" {
		if !logs.ValidAppID(*app) {
			return usageError{*app + " does not look like an app id"}
		}
	}
	minimum := logs.NoLevel
	if *levelName != "" {
		var known bool
		if minimum, known = logs.ParseLevel(*levelName); !known {
			return usageError{"unknown level: " + *levelName + " (verbose, debug, info, warning, error, fatal)"}
		}
	}
	modes := logs.Modes(d)
	if len(modes) == 0 {
		return apperr.New(apperr.InvalidState, "%s is not running", d.Name).WithHint("Boot or connect it first.")
	}
	chosen := logs.Mode(*mode)
	if chosen == "" {
		// The first stream that can start with what was given.
		for _, m := range modes {
			if !m.AppRequired || *app != "" {
				chosen = m.ID
				break
			}
		}
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	encoder := json.NewEncoder(a.Stdout)
	var remember sync.Once
	session := logs.Start(ctx, a.Manager.LogDeps(), logs.Spec{Device: d, Mode: chosen, App: *app}, logs.Handlers{
		Lines: func(lines []logs.Line) {
			// The app id is remembered once the log works, so a mistyped one is not offered again.
			// Remembering is a convenience: a state file that cannot be written does not stop the log.
			if *app != "" {
				remember.Do(func() { _ = a.State.Update(func(f *state.File) { f.RememberApp(d.Platform, *app) }) })
			}
			for _, line := range lines {
				// A line without a level belongs to the one before it, so it is kept.
				if line.Level != logs.NoLevel && line.Level < minimum {
					continue
				}
				if *asJSON {
					_ = encoder.Encode(line)
				} else {
					fmt.Fprintln(a.Stdout, line.Text)
				}
			}
		},
		Status: func(message string) { fmt.Fprintln(a.Stderr, message) },
	})
	if err := session.Wait(); err != nil {
		return err
	}
	return ctx.Err()
}

func platformName(p device.Platform) string {
	if p == device.IOS {
		return "iOS"
	}
	return "Android"
}

// apps lists the app ids that can be given to logs: the ones used before, then the ones installed
// on the device.
func (a *App) apps(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("apps", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "")
	d, err := a.one(ctx, fs, args, "apps")
	if err != nil {
		return err
	}
	var installed []device.App
	switch {
	case !d.Ready():
		fmt.Fprintf(a.Stderr, "%s is not running, so only app ids used before are listed.\n", d.Name)
	default:
		if installed, err = a.Manager.InstalledApps(ctx, d); err != nil {
			fmt.Fprintf(a.Stderr, "warning: the installed apps could not be listed: %s\n", firstLine(err.Error()))
		}
	}
	choices := state.Choices(a.State.Load().Apps[d.Platform], installed)
	if *asJSON {
		if choices == nil {
			choices = []state.AppChoice{}
		}
		return a.writeJSON(map[string]any{"device": d.Name, "apps": choices})
	}
	if len(choices) == 0 {
		fmt.Fprintln(a.Stderr, "No apps to show.")
		return nil
	}
	table := tabwriter.NewWriter(a.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "ID\tNAME\tVERSION\tFROM")
	for _, c := range choices {
		var from []string
		if c.Recent {
			from = append(from, "recent")
		}
		if c.Installed {
			from = append(from, "installed")
		}
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\n", c.ID, c.Name, c.VersionLabel(), strings.Join(from, ", "))
	}
	return table.Flush()
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return line
}

func (a *App) doctor(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "")
	if positional, err := parse(fs, args); err != nil {
		return err
	} else if len(positional) > 0 {
		return usageError{"doctor takes no arguments"}
	}
	report := a.Doctor(ctx)
	if *asJSON {
		if report.Found == nil {
			report.Found = []doctor.Found{}
		}
		if report.Problems == nil {
			report.Problems = []doctor.Problem{}
		}
		if err := a.writeJSON(report); err != nil {
			return err
		}
	} else {
		WriteReport(a.Stdout, report)
	}
	if !report.Healthy() {
		return reportedError{errors.New("some tools are set up wrongly")}
	}
	return nil
}

var severityMark = map[doctor.Severity]string{doctor.Wrong: "x", doctor.Unavailable: "!", doctor.Optional: "-"}

// WriteReport prints a doctor report for people.
func WriteReport(w io.Writer, report doctor.Report) {
	for _, found := range report.Found {
		fmt.Fprintf(w, "  ok  %s: %s\n", found.Name, found.Detail)
	}
	for _, p := range report.Problems {
		fmt.Fprintf(w, "  %s   [%s] %s\n", severityMark[p.Severity], p.Platform, p.Message)
		if p.Hint != "" {
			fmt.Fprintf(w, "        %s\n", p.Hint)
		}
	}
	if len(report.Problems) == 0 {
		fmt.Fprintln(w, "Everything needed is installed.")
	}
	if report.OS != "darwin" {
		fmt.Fprintln(w, "iOS needs macOS with Xcode, so only Android is checked here.")
	}
}

// BuildLister lists the Expo builds of the project in the current folder.
type BuildLister interface {
	Name() string
	List(ctx context.Context, limit int) ([]eas.Build, error)
}

func (a *App) builds(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("builds", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "")
	limit := fs.Int("limit", 10, "")
	if positional, err := parse(fs, args); err != nil {
		return err
	} else if len(positional) > 0 {
		return usageError{"builds takes no arguments"}
	}
	if *limit < 1 || *limit > 50 {
		return usageError{"--limit is between 1 and 50"}
	}
	project := a.Builds()
	if project == nil {
		return apperr.New(apperr.NotFound, "No Expo project was found here").
			WithHint("Run this inside a project that has an eas.json file.")
	}
	list, err := project.List(ctx, *limit)
	if err != nil {
		return err
	}
	if *asJSON {
		if list == nil {
			list = []eas.Build{}
		}
		return a.writeJSON(map[string]any{"project": project.Name(), "builds": list})
	}
	table := tabwriter.NewWriter(a.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "PLATFORM\tPROFILE\tSTATUS\tCREATED\tID")
	for _, b := range list {
		created := ""
		if !b.CreatedAt.IsZero() {
			created = b.CreatedAt.Local().Format("2006-01-02 15:04")
		}
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n", b.Platform, b.Profile, strings.ToLower(b.Status), created, b.ID)
	}
	return table.Flush()
}

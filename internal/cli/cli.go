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
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/0xrnp/lazymobile/internal/actions"
	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/devicesets"
	"github.com/0xrnp/lazymobile/internal/doctor"
	"github.com/0xrnp/lazymobile/internal/eas"
	"github.com/0xrnp/lazymobile/internal/ios"
	"github.com/0xrnp/lazymobile/internal/logs"
	"github.com/0xrnp/lazymobile/internal/mcpserver"
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
  lazymobile boot --all <name or pattern>... | --set <name> [--cold] [--wait] [--no-window] [--json]   several at once, such as "iPhone*"
  lazymobile shutdown <device> [--keep-simulator-app] [--json]
  lazymobile erase <device> --yes [--json]   erase a simulator, or wipe an emulator's data
  lazymobile delete <device> --yes [--json]
  lazymobile uninstall <device> <app-id> --yes [--json]   remove an app and its data from a running device
  lazymobile install <device> <path> [--json]   an .apk, or an .app folder for a simulator or an iPhone
  lazymobile install --all | --set <name> <path> [--phones] [--json]   on every running device that can take it. Real phones only with --phones
  lazymobile launch <device> <app-id> [--restart] [--json]   open an app. --restart stops a running copy first
  lazymobile stop <device> <app-id> [--json]    stop a running app. Its data is kept
  lazymobile appearance <device> [dark|light] [--json]   show the mode, or switch to dark or light
  lazymobile permission <device> grant|revoke <permission> <app-id> [--json]   such as camera, location, photos
  lazymobile cleardata <device> <app-id> --yes [--json]   erase an app's data on an emulator
  lazymobile location <device> <lat,lon>|clear [--json]   put a simulator or emulator somewhere
  lazymobile statusbar <device> clean|reset [--json]   a tidy status bar for screenshots
  lazymobile action --list [--json]   the project's own commands from .lazymobile.json
  lazymobile action <device> <name> [--app <app-id>] --yes [--json]   run one on a device. Without --yes it only shows the command
  lazymobile run [<device>] [--profile <name>] [--choose] [--here | --split] [--yes] [-- <tool flags>]   start the device if it is off and run this folder's app on it with the tool you already use (flutter run, or the action named "run" in .lazymobile.json, which needs --yes). In a terminal it asks which way to run (the actions named "run", "run dev", "run prod" in .lazymobile.json) and which device when it cannot tell, and remembers the answer for the folder; --choose asks again. Its keys, such as r for hot reload, keep working. --here runs it inside the dashboard: ctrl+] switches between the run and the full dashboard
  lazymobile link <device> <url> [--json]       open a web address or deep link, such as myapp://home
  lazymobile dev <device> reverse|menu|reload [--port 8081] [--json]   help an app in development on an Android device: forward a port to this computer (Metro), open its developer menu, reload it
  lazymobile record <device> [--out <file>] [--duration 30s] [--json]   film the screen until Ctrl-C or the duration. Never replaces a file
  lazymobile screenshot <device> [--out <file>] [--json]   save a PNG of the screen. Never replaces a file
  lazymobile crash <device> <app-id> [--save <file>] [--json]   the app's latest crash report, on a simulator or an Android device. --save never replaces a file
  lazymobile report <device> <app-id> [--out <folder>] [--since 5m] [--lines 200] [--json]   save a bug report in a new folder: a screenshot, the app's last log lines, its latest crash report, and the device and app version. Never replaces a folder
  lazymobile changelog [<version>] [--all] [--json]   what changed: this version's notes, one version's, or all of them
  lazymobile logs <device> [--app <id> | --last-app] [--mode logcat|system|console] [--level <level>] [--tail N] [--since 5m] [--json]
  lazymobile apps <device> [--json]    app ids used before, and the apps installed on the device
  lazymobile id <device> [--json]    the id for adb -s, flutter -d and xcrun
  lazymobile builds [--json] [--limit n] [--yes]   Expo (EAS) builds of the project in this folder. --yes is needed when .lazymobile.json sets the eas command
  lazymobile doctor [--fix [--yes]] [--json]       what is installed, what is missing, and how to fix it
  lazymobile mcp                   serve the device commands to an agent over MCP (stdio). Nothing that erases data
  lazymobile version

Once no simulator is running, Simulator or DeviceHub is quit, which closes the window it keeps
open for a simulator that shut down. --keep-simulator-app leaves it open. The dashboard takes
--no-window and --keep-simulator-app too.

App ids given to logs and launch are remembered, newest first, for --last-app, for apps and for the
dashboard's app list.

<device> is an id, an adb serial, a name, or a unique part of a name.
Commands that erase, delete, uninstall or clear data need --yes.
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
	// Here opens the dashboard on the run view, with the tool running in it: lazymobile run --here.
	Here func(ctx context.Context, m *mobile.Manager, run HereRun) error
	// Doctor produces the tool report.
	Doctor func(ctx context.Context) doctor.Report
	// Builds returns the Expo project of the current folder, or nil when there is none.
	Builds func() BuildLister
	// Stdin answers the questions of doctor --fix. Interactive is whether it is a terminal.
	Stdin       io.Reader
	Interactive bool
	// RunFix runs the command of a doctor fix. Nil runs it for real, with the terminal.
	RunFix func(ctx context.Context, argv []string) error
	// DeviceSets returns the named groups of devices from .lazymobile.json.
	DeviceSets func() (devicesets.Sets, error)
	// Actions returns the project's own commands from .lazymobile.json, or nil when there is none.
	Actions func() ([]actions.Action, error)
	// State remembers app ids between runs. The zero Store remembers nothing.
	State state.Store
	// Getenv reads the environment, to find the multiplexer for `run --split`. Nil means os.Getenv.
	Getenv func(string) string
	// Executable is the path of this program, for the dashboard that `run --split` opens. Empty
	// means the running one.
	Executable string
	// Terminal is whether input and output are a terminal, where a list can be shown to choose from.
	Terminal bool
	// Pick shows a list in the terminal and returns the index chosen, or ErrCancelled.
	Pick func(ctx context.Context, title string, items []PickItem, start int) (int, error)
	// Changelog is the text of CHANGELOG.md, which `changelog` prints.
	Changelog string
	// Getwd is the folder `run` looks at. Nil means the current folder.
	Getwd func() (string, error)
	// RunTool runs the project's command for `run`, with the terminal. Nil runs it for real.
	RunTool func(ctx context.Context, dir string, argv []string) error
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
	Device device.Device `json:"device"`
	App    string        `json:"app,omitempty"`
	Path   string        `json:"path,omitempty"`
	URL    string        `json:"url,omitempty"`
	// Port is the port a "dev_reverse" forwarded.
	Port int `json:"port,omitempty"`
	// Appearance is the mode a device was switched to.
	Appearance string `json:"appearance,omitempty"`
	// Permission is the permission that was granted or revoked.
	Permission string `json:"permission,omitempty"`
	// Location is "latitude,longitude", or "cleared".
	Location string `json:"location,omitempty"`
	// Crash is the report a "crash" found. Path is then the file it was saved to, if any.
	Crash *device.CrashReport `json:"crash,omitempty"`
	// Name and Command are the project action that ran, with the device filled in.
	Name     string   `json:"name,omitempty"`
	Command  []string `json:"command,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
	// Report is the bug report a "report" saved. Path is then its folder.
	Report *mobile.Report `json:"report,omitempty"`
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
	case "appearance":
		return a.appearance(ctx, rest)
	case "permission":
		return a.permission(ctx, rest)
	case "cleardata":
		return a.clearData(ctx, rest)
	case "location":
		return a.location(ctx, rest)
	case "statusbar":
		return a.statusBar(ctx, rest)
	case "action":
		return a.action(ctx, rest)
	case "run":
		return a.runProject(ctx, rest)
	case "link":
		return a.link(ctx, rest)
	case "dev":
		return a.dev(ctx, rest)
	case "record":
		return a.record(ctx, rest)
	case "screenshot":
		return a.screenshot(ctx, rest)
	case "crash":
		return a.crash(ctx, rest)
	case "report":
		return a.bugReport(ctx, rest)
	case "changelog":
		return a.changelog(rest)
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
	case "mcp":
		if len(rest) > 0 {
			return usageError{"mcp takes no arguments"}
		}
		server := mcpserver.Server{Manager: a.Manager, Doctor: a.Doctor, State: a.State, Version: a.Version}
		return server.Run(ctx)
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
	var many several
	many.flags(fs)
	positional, err := parse(fs, args)
	if err != nil {
		return err
	}
	if many.wanted() {
		if *wipe {
			return usageError{"--wipe erases data, so it is done for one device at a time"}
		}
		return a.bootSeveral(ctx, many, positional, mobile.BootOptions{Cold: *cold, Wait: *wait}, *noWindow, *asJSON)
	}
	if len(positional) != 1 {
		return usageError{"boot needs one device"}
	}
	d, err := a.Manager.Find(ctx, positional[0])
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
	phones := fs.Bool("phones", false, "")
	var many several
	many.flags(fs)
	positional, err := parse(fs, args)
	if err != nil {
		return err
	}
	if many.wanted() {
		if len(positional) != 1 {
			return usageError{"install --all and --set need just a path"}
		}
		return a.installSeveral(ctx, many, positional[0], *phones, *asJSON)
	}
	if *phones {
		return usageError{"--phones goes with --all or --set"}
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

func (a *App) appearance(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("appearance", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "")
	positional, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(positional) < 1 || len(positional) > 2 {
		return usageError{"appearance needs a device, and dark or light to switch it"}
	}
	d, err := a.Manager.Find(ctx, positional[0])
	if err != nil {
		return err
	}
	if len(positional) == 1 {
		mode, err := a.Manager.Appearance(ctx, d)
		if err != nil {
			return err
		}
		if *asJSON {
			return a.writeJSON(map[string]any{"device": d, "appearance": mode})
		}
		fmt.Fprintln(a.Stdout, mode)
		return nil
	}
	if err := a.Manager.SetAppearance(ctx, d, positional[1]); err != nil {
		return err
	}
	return a.done(*asJSON, result{Action: "appearance", Device: d, Appearance: positional[1]},
		fmt.Sprintf("%s is now in %s mode", d.Name, positional[1]))
}

func (a *App) permission(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("permission", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "")
	positional, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 4 || (positional[1] != "grant" && positional[1] != "revoke") {
		return usageError{"permission needs a device, grant or revoke, a permission and an app id"}
	}
	d, err := a.Manager.Find(ctx, positional[0])
	if err != nil {
		return err
	}
	grant, name, app := positional[1] == "grant", positional[2], positional[3]
	if err := a.Manager.SetPermission(ctx, d, app, name, grant); err != nil {
		return err
	}
	sentence := fmt.Sprintf("Revoked %s from %s on %s", name, app, d.Name)
	if grant {
		sentence = fmt.Sprintf("Granted %s to %s on %s", name, app, d.Name)
	}
	return a.done(*asJSON, result{Action: "permission_" + positional[1], Device: d, App: app, Permission: name}, sentence)
}

func (a *App) clearData(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("cleardata", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "")
	asJSON := fs.Bool("json", false, "")
	d, app, err := a.deviceAndApp(ctx, fs, args)
	if err != nil {
		return err
	}
	if !*yes {
		return usageError{fmt.Sprintf("cleardata erases everything %s saved on %s. Add --yes to confirm", app, d.Name)}
	}
	if err := a.Manager.ClearData(ctx, d, app); err != nil {
		return err
	}
	return a.done(*asJSON, result{Action: "cleardata", Device: d, App: app}, fmt.Sprintf("Cleared the data of %s on %s", app, d.Name))
}

func (a *App) location(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("location", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "")
	positional, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 2 {
		return usageError{"location needs a device, and latitude,longitude or clear"}
	}
	d, err := a.Manager.Find(ctx, positional[0])
	if err != nil {
		return err
	}
	if positional[1] == "clear" {
		if err := a.Manager.ClearLocation(ctx, d); err != nil {
			return err
		}
		return a.done(*asJSON, result{Action: "location", Device: d, Location: "cleared"}, "Cleared the location of "+d.Name)
	}
	lat, lon, err := mobile.ParseLocation(positional[1])
	if err != nil {
		return err
	}
	if err := a.Manager.SetLocation(ctx, d, lat, lon); err != nil {
		return err
	}
	place := strconv.FormatFloat(lat, 'f', -1, 64) + "," + strconv.FormatFloat(lon, 'f', -1, 64)
	return a.done(*asJSON, result{Action: "location", Device: d, Location: place}, fmt.Sprintf("%s is now at %s", d.Name, place))
}

func (a *App) statusBar(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("statusbar", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "")
	positional, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 2 || (positional[1] != "clean" && positional[1] != "reset") {
		return usageError{"statusbar needs a device, and clean or reset"}
	}
	d, err := a.Manager.Find(ctx, positional[0])
	if err != nil {
		return err
	}
	clean := positional[1] == "clean"
	if err := a.Manager.SetStatusBar(ctx, d, clean); err != nil {
		return err
	}
	if clean {
		return a.done(*asJSON, result{Action: "statusbar_clean", Device: d}, "The status bar of "+d.Name+" is tidy for a screenshot")
	}
	return a.done(*asJSON, result{Action: "statusbar_reset", Device: d}, "The status bar of "+d.Name+" is back to normal")
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

func (a *App) dev(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("dev", flag.ContinueOnError)
	port := fs.Int("port", mobile.DefaultDevPort, "")
	asJSON := fs.Bool("json", false, "")
	positional, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 2 || !slices.Contains(mobile.DevActions, positional[1]) {
		return usageError{"dev needs a device and one of: " + strings.Join(mobile.DevActions, ", ")}
	}
	if *port < 1 || *port > 65535 {
		return usageError{fmt.Sprintf("--port must be from 1 to 65535, not %d", *port)}
	}
	d, err := a.Manager.Find(ctx, positional[0])
	if err != nil {
		return err
	}
	action := positional[1]
	if err := a.Manager.Dev(ctx, d, action, *port); err != nil {
		return err
	}
	sentence := map[string]string{
		"reverse": fmt.Sprintf("%s can reach port %d on this computer", d.Name, *port),
		"menu":    "Opened the developer menu on " + d.Name,
		"reload":  "Reloaded the app on " + d.Name,
	}[action]
	r := result{Action: "dev_" + action, Device: d}
	if action == "reverse" {
		r.Port = *port
	}
	return a.done(*asJSON, r, sentence)
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

func (a *App) crash(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("crash", flag.ContinueOnError)
	save := fs.String("save", "", "")
	asJSON := fs.Bool("json", false, "")
	d, app, err := a.deviceAndApp(ctx, fs, args)
	if err != nil {
		return err
	}
	report, err := a.Manager.Crash(ctx, d, app)
	if err != nil {
		return err
	}
	saved := ""
	if *save != "" {
		if saved, err = mobile.SaveText(*save, report.Text); err != nil {
			return err
		}
	}
	r := result{Action: "crash", Device: d, App: app, Path: saved, Crash: &report}
	switch {
	case *asJSON:
		return a.done(true, r, "")
	case saved != "":
		return a.done(false, r, "Saved "+saved)
	}
	text := report.Text
	if report.Path != "" {
		text += "\n\nFull report: " + report.Path
	}
	return a.done(false, r, text)
}

func (a *App) record(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("record", flag.ContinueOnError)
	out := fs.String("out", "", "")
	duration := fs.Duration("duration", 0, "")
	asJSON := fs.Bool("json", false, "")
	d, err := a.one(ctx, fs, args, "record")
	if err != nil {
		return err
	}
	if *duration < 0 {
		return usageError{"--duration cannot be negative"}
	}
	if *out == "" {
		*out = mobile.RecordingName(d, time.Now())
	}
	// Ctrl-C ends the recording and keeps the video, so it must not end the command.
	recording, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	started := func() {
		if !*asJSON {
			fmt.Fprintf(a.Stderr, "Recording %s. Press Ctrl-C to stop.\n", d.Name)
		}
	}
	path, err := a.Manager.Record(recording, d, *out, *duration, started)
	if err != nil {
		return err
	}
	return a.done(*asJSON, result{Action: "record", Device: d, Path: path}, "Saved "+path)
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
	tail := fs.Int("tail", 0, "")
	since := fs.Duration("since", 0, "")
	asJSON := fs.Bool("json", false, "")
	d, err := a.one(ctx, fs, args, "logs")
	if err != nil {
		return err
	}
	if *tail < 0 || *since < 0 {
		return usageError{"--tail and --since cannot be negative"}
	}
	// With either, the log is read and the command ends, instead of following it.
	dump := *tail > 0 || *since > 0
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
	var last []logs.Line
	emit := func(line logs.Line) {
		if *asJSON {
			_ = encoder.Encode(line)
		} else {
			fmt.Fprintln(a.Stdout, line.Text)
		}
	}
	session := logs.Start(ctx, a.Manager.LogDeps(), logs.Spec{Device: d, Mode: chosen, App: *app, Dump: dump, Since: *since}, logs.Handlers{
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
				if *tail > 0 {
					last = append(last, line)
					if len(last) > *tail {
						last = last[1:]
					}
					continue
				}
				emit(line)
			}
		},
		Status: func(message string) { fmt.Fprintln(a.Stderr, message) },
	})
	if err := session.Wait(); err != nil {
		return err
	}
	for _, line := range last {
		emit(line)
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
	fixIt := fs.Bool("fix", false, "")
	yes := fs.Bool("yes", false, "")
	if positional, err := parse(fs, args); err != nil {
		return err
	} else if len(positional) > 0 {
		return usageError{"doctor takes no arguments"}
	}
	if *fixIt && *asJSON {
		return usageError{"--fix asks questions, so it cannot be used with --json. The fixes are in the report's \"fix\" fields"}
	}
	if *yes && !*fixIt {
		return usageError{"--yes goes with --fix"}
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
	if *fixIt {
		ran, err := a.fix(ctx, report, *yes)
		if err != nil {
			return err
		}
		if ran {
			fmt.Fprintln(a.Stdout, "\nChecking again:")
			report = a.Doctor(ctx)
			WriteReport(a.Stdout, report)
		}
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
	if len(fixable(report)) > 0 {
		fmt.Fprintln(w, "\nRun `lazymobile doctor --fix` to see the command for each fix. It asks before running one.")
	}
	if report.OS != "darwin" {
		fmt.Fprintln(w, "iOS needs macOS with Xcode, so only Android is checked here.")
	}
}

// BuildLister lists the Expo builds of the project in the current folder.
type BuildLister interface {
	Name() string
	// ConfiguredCommand is the eas command from .lazymobile.json, or nil when there is none.
	ConfiguredCommand() []string
	List(ctx context.Context, limit int) ([]eas.Build, error)
}

func (a *App) builds(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("builds", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "")
	yes := fs.Bool("yes", false, "")
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
	// The settings file can come from a repository someone else wrote, so its command is shown first.
	if argv := project.ConfiguredCommand(); len(argv) > 0 && !*yes {
		return usageError{fmt.Sprintf("%s sets the eas command to `%s`, which would run in %s. Add --yes to run it",
			eas.ConfigFile, strings.Join(argv, " "), project.Name())}
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

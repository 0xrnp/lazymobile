package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/0xrnp/lazymobile/internal/actions"
	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/mobile"
	"github.com/0xrnp/lazymobile/internal/state"
)

// runProfile reports whether an action of .lazymobile.json is a way to run the app: one named "run",
// or "run dev" or "run:dev". Each is a profile to choose from, such as a flavor. A name such as
// "run-tests" is an ordinary action, since projects already use names like that for other things.
func runProfile(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "run" {
		return true
	}
	for _, prefix := range []string{"run ", "run:"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// runPlan is one way to run the project in a folder: the command, given a device.
type runPlan struct {
	// name is what the way is called when there are several to choose from.
	name string
	// dir is where the command runs.
	dir string
	// fromFile is whether the command comes from .lazymobile.json, which a repository someone else
	// wrote can set, so it is shown and needs --yes.
	fromFile bool
	// platform limits the plan to one platform. Empty means both.
	platform device.Platform
	argv     func(d device.Device) ([]string, error)
}

// HereRun is a tool for the dashboard to run in its run view, on a device that is running.
type HereRun struct {
	// Dir is where the tool runs, and Folder where lazymobile run was typed.
	Dir, Folder string
	Argv        []string
	// Title names the tool, such as "flutter run" or "run dev".
	Title  string
	Device device.Device
}

// PickItem is a line of a list the user chooses from.
type PickItem struct {
	Label  string
	Detail string
}

// ErrCancelled is what a pick returns when the user backs out of it.
var ErrCancelled = errors.New("cancelled")

// runProject runs the app in this folder with the tool the project already uses, on a device that
// lazymobile finds and boots first. The tool keeps the terminal, so its keys work as before. With
// several ways to run it, or several devices, a terminal gets a list to choose from, and the
// choice is remembered for the folder.
func (a *App) runProject(ctx context.Context, args []string) error {
	own, extra := splitTool(args)
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "")
	split := fs.Bool("split", false, "")
	here := fs.Bool("here", false, "")
	choose := fs.Bool("choose", false, "")
	profile := fs.String("profile", "", "")
	positional, err := parse(fs, own)
	if err != nil {
		return err
	}
	if len(positional) > 1 {
		return usageError{"run takes one device, and the tool's own flags after --"}
	}
	if *choose && !a.Terminal {
		return usageError{"--choose needs a terminal to ask in"}
	}
	if *here && *split {
		return usageError{"--here runs the tool inside lazymobile, and --split beside it: use one"}
	}
	if *here && (!a.Terminal || a.Here == nil) {
		return usageError{"--here needs a terminal to show the dashboard in"}
	}
	// What is remembered, and what a dashboard looks up, is keyed by the folder this was typed in.
	folder, err := a.getwd()
	if err != nil {
		return err
	}
	plans, err := a.runPlans(folder, extra)
	if err != nil {
		return err
	}
	plan, err := a.chooseRunPlan(ctx, plans, folder, *profile, *choose)
	if err != nil {
		return err
	}
	d, err := a.runDevice(ctx, plan, folder, positional, *choose)
	if err != nil {
		return err
	}
	argv, err := plan.argv(d)
	if err != nil {
		return err
	}
	if plan.fromFile && !*yes {
		return usageError{fmt.Sprintf("%s in %s would run `%s` on %s. Add --yes to run it",
			actions.ConfigFile, plan.dir, strings.Join(argv, " "), d.Name)}
	}
	if !d.Ready() {
		fmt.Fprintf(a.Stderr, "Starting %s...\n", d.Name)
		if err := a.Manager.Boot(ctx, d, mobile.BootOptions{Wait: true}); err != nil {
			return err
		}
		// An emulator has an adb serial only once it runs, and that is what the tools want.
		if d, err = a.Manager.Find(ctx, d.ID); err != nil {
			return err
		}
		if argv, err = plan.argv(d); err != nil {
			return err
		}
	}
	fmt.Fprintf(a.Stderr, "Running `%s` in %s\n", strings.Join(argv, " "), plan.dir)
	// A dashboard open in this folder follows the run: it selects this device and opens the app's log.
	_ = a.State.Update(func(f *state.File) { f.RememberRunStarted(folder, d.Key(), time.Now()) })
	if *here {
		title := plan.name
		if title == runAction {
			title = filepath.Base(argv[0])
		}
		return a.Here(ctx, a.Manager, HereRun{Dir: plan.dir, Folder: folder, Argv: argv, Title: title, Device: d})
	}
	if *split {
		if err := a.openDashboardBeside(ctx, folder); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(a.Stderr, "Logs and device tools: add --here to run it inside lazymobile, or run lazymobile in another terminal in this folder (or add --split). It opens on %s.\n", d.Name)
	}
	return a.runTool(ctx, plan.dir, argv)
}

// openDashboardBeside opens the dashboard in a pane next to this terminal, in the folder of the run,
// where it follows the run. It must be asked for in a terminal that Rex, tmux or Zellij is running.
func (a *App) openDashboardBeside(ctx context.Context, dir string) error {
	name, argv := splitCommand(a.env, a.executable(), dir)
	if argv == nil {
		return apperr.New(apperr.Unsupported, "--split needs a terminal that Rex, tmux or Zellij is running").
			WithHint("Open lazymobile in a second terminal in this folder instead: it follows the run.")
	}
	if err := a.spawn(ctx, dir, argv); err != nil {
		var e *apperr.Error
		if errors.As(err, &e) {
			e.Hint = "lazymobile could not open a pane in " + name + ". Open lazymobile in a second terminal in this folder instead."
		}
		return err
	}
	fmt.Fprintf(a.Stderr, "Opened lazymobile beside this terminal in %s.\n", name)
	return nil
}

// splitTool separates the tool's own flags, which come after "--", from lazymobile's.
func splitTool(args []string) (own, tool []string) {
	for i, arg := range args {
		if arg == "--" {
			return args[:i], args[i+1:]
		}
	}
	return args, nil
}

// runPlans lists the ways to run the project in folder: each profile in .lazymobile.json, in file
// order, or else the tool the folder looks like it uses, Flutter or Expo.
func (a *App) runPlans(folder string, extra []string) ([]runPlan, error) {
	var all []actions.Action
	if a.Actions != nil {
		var err error
		if all, err = a.Actions(); err != nil {
			return nil, err
		}
	}
	var plans []runPlan
	for _, chosen := range all {
		if !runProfile(chosen.Name) {
			continue
		}
		plans = append(plans, runPlan{name: chosen.Name, dir: chosen.Dir, fromFile: true, platform: chosen.Platform,
			argv: func(d device.Device) ([]string, error) {
				argv, err := chosen.Resolve(d, "")
				return append(argv, extra...), err
			}})
	}
	if len(plans) > 0 {
		// What the project wrote down is how it is run. The built-in tools are only a guess.
		return plans, nil
	}
	if _, err := os.Stat(filepath.Join(folder, "pubspec.yaml")); err == nil {
		plans = append(plans, runPlan{name: "flutter run", dir: folder, argv: func(d device.Device) ([]string, error) {
			return append([]string{"flutter", "run", "-d", d.TargetID()}, extra...), nil
		}})
	} else if hasExpo(folder) {
		plans = append(plans, runPlan{name: "expo run", dir: folder, argv: func(d device.Device) ([]string, error) {
			bin, err := findBin(folder, "expo")
			if err != nil {
				return nil, err
			}
			return append([]string{bin, "run:" + string(d.Platform), "--device", d.TargetID()}, extra...), nil
		}})
	}
	if len(plans) == 0 {
		return nil, apperr.New(apperr.NotFound, "Cannot tell how to run the app in %s", folder).
			WithHint("lazymobile run knows Flutter and Expo projects (a pubspec.yaml, or expo in package.json). For another tool, add an action named \"run\" to %s, for example {\"name\": \"run\", \"command\": [\"npx\", \"expo\", \"run:ios\", \"--device\", \"{device_id}\"]}. Add \"run dev\" and \"run prod\" for flavors.",
				actions.ConfigFile)
	}
	return plans, nil
}

// chooseRunPlan is the way to run that was named with --profile, else the only one, else the one
// remembered for the folder, else the one the user picks. Without a terminal to ask in, several ways
// to run mean the one named "run", since that is what a project with one has always used.
func (a *App) chooseRunPlan(ctx context.Context, plans []runPlan, folder, name string, choose bool) (runPlan, error) {
	names := make([]string, len(plans))
	for i, p := range plans {
		names[i] = p.name
	}
	index := func(want string) int {
		for i, p := range plans {
			if strings.EqualFold(p.name, want) {
				return i
			}
		}
		return -1
	}
	if name != "" {
		if i := index(name); i >= 0 {
			a.rememberProfile(folder, plans[i], len(plans))
			return plans[i], nil
		}
		return runPlan{}, usageError{fmt.Sprintf("no way to run is named %q: %s", name, strings.Join(names, ", "))}
	}
	if len(plans) == 1 {
		return plans[0], nil
	}
	last := index(a.State.Load().Runs[folder].Profile)
	if last >= 0 && !choose {
		return plans[last], nil
	}
	if !a.Terminal || a.Pick == nil {
		if i := index(runAction); i >= 0 {
			return plans[i], nil
		}
		return runPlan{}, usageError{fmt.Sprintf("several ways to run: %s. Name one with --profile", strings.Join(names, ", "))}
	}
	items := make([]PickItem, len(plans))
	for i, p := range plans {
		items[i] = PickItem{Label: p.name, Detail: "from " + actions.ConfigFile}
	}
	start := max(last, index(runAction), 0)
	chosen, err := a.Pick(ctx, "How to run it?", items, start)
	if err != nil {
		return runPlan{}, err
	}
	a.rememberProfile(folder, plans[chosen], len(plans))
	return plans[chosen], nil
}

// runAction is the name of the plain way to run a project from .lazymobile.json.
const runAction = "run"

func (a *App) rememberProfile(folder string, plan runPlan, of int) {
	if of > 1 {
		_ = a.State.Update(func(f *state.File) { f.RememberRunProfile(folder, plan.name) })
	}
}

// runDevice is the device that was named, else the one this folder was last run on, else the one
// simulator or emulator that is running, else the one the user picks in a terminal. A real phone is
// never taken on its own, so an app does not land on one because it was plugged in or used before.
// It is only used when it is named or picked, and it is not remembered.
func (a *App) runDevice(ctx context.Context, plan runPlan, folder string, positional []string, choose bool) (device.Device, error) {
	if len(positional) == 1 {
		d, err := a.Manager.Find(ctx, positional[0])
		if err == nil {
			a.rememberRun(folder, d)
		}
		return d, err
	}
	all, errs := a.Manager.List(ctx)
	fits := func(d device.Device) bool {
		return d.Kind != device.Physical && (plan.platform == "" || plan.platform == d.Platform)
	}
	last := a.State.Load().Runs[folder].Device
	if !choose {
		for _, d := range all {
			if d.Key() == last && fits(d) {
				fmt.Fprintf(a.Stderr, "Using %s, the device this folder was last run on\n", d.Name)
				return d, nil
			}
		}
	}
	var running []device.Device
	for _, d := range all {
		if fits(d) && d.Ready() {
			running = append(running, d)
		}
	}
	if len(running) == 1 && !choose {
		a.rememberRun(folder, running[0])
		return running[0], nil
	}
	if a.Terminal && a.Pick != nil {
		return a.pickDevice(ctx, all, plan, folder, last)
	}
	if len(running) == 0 {
		e := apperr.New(apperr.NotFound, "No simulator or emulator is running")
		e.Hint = "Name one and lazymobile starts it: lazymobile run <device>. `lazymobile list` shows them."
		if len(errs) > 0 {
			e.Hint += " Some tools failed: " + errs[0].Error()
		}
		return device.Device{}, e
	}
	names := make([]string, len(running))
	for i, d := range running {
		names[i] = fmt.Sprintf("%s (%s)", d.Name, d.ID)
	}
	return device.Device{}, apperr.New(apperr.InvalidState, "Several devices are running").
		WithHint("Name one: lazymobile run <device>. Running: %s", strings.Join(names, ", "))
}

// pickDevice asks which device to run on: the ones running first, then the ones that are off, which
// run starts, then phones that are connected.
func (a *App) pickDevice(ctx context.Context, all []device.Device, plan runPlan, folder, last string) (device.Device, error) {
	var running, off, phones []device.Device
	for _, d := range all {
		switch {
		case plan.platform != "" && plan.platform != d.Platform:
		case d.Kind == device.Physical && d.Ready():
			phones = append(phones, d)
		case d.Kind == device.Physical:
		case d.Ready():
			running = append(running, d)
		default:
			off = append(off, d)
		}
	}
	list := append(append(running, off...), phones...)
	if len(list) == 0 {
		return device.Device{}, apperr.New(apperr.NotFound, "No simulator, emulator or phone to run on").
			WithHint("`lazymobile list` shows what lazymobile can see. `lazymobile doctor` says what is missing.")
	}
	items := make([]PickItem, len(list))
	start := 0
	for i, d := range list {
		detail := strings.TrimSpace(d.OSLabel() + " " + string(d.State))
		if d.Kind == device.Physical {
			detail = "real phone, " + detail
		}
		for j := range list {
			// Two devices can share a name, so the id says which is which.
			if j != i && list[j].Name == d.Name {
				detail += "  " + d.ID[:min(len(d.ID), 8)]
				break
			}
		}
		items[i] = PickItem{Label: d.Name, Detail: detail}
		if d.Key() == last {
			start = i
		}
	}
	chosen, err := a.Pick(ctx, "Run on which device?", items, start)
	if err != nil {
		return device.Device{}, err
	}
	a.rememberRun(folder, list[chosen])
	return list[chosen], nil
}

// rememberRun makes d the device `run` uses next time in folder. A real phone is not remembered.
func (a *App) rememberRun(folder string, d device.Device) {
	if d.Kind == device.Physical {
		return
	}
	_ = a.State.Update(func(f *state.File) { f.RememberRun(folder, d.Key()) })
}

func (a *App) getwd() (string, error) {
	if a.Getwd != nil {
		return a.Getwd()
	}
	return os.Getwd()
}

// runTool runs argv in dir with the terminal's own input and output, so the tool's keys work.
func (a *App) runTool(ctx context.Context, dir string, argv []string) error {
	if a.RunTool != nil {
		return a.RunTool(ctx, dir, argv)
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, a.Stdout, a.Stderr
	// An interrupt first, so a tool like flutter can shut down its own children.
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 10 * time.Second
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return nil
	case ctx.Err() != nil:
		return ctx.Err()
	case errors.As(err, &exit):
		e := apperr.New(apperr.CommandFailed, "%s exited with code %d", argv[0], exit.ExitCode())
		e.Argv, e.ExitCode = argv, exit.ExitCode()
		return e
	case errors.Is(err, exec.ErrNotFound):
		e := apperr.New(apperr.ToolMissing, "%s is not installed or not on your PATH", argv[0])
		e.Argv = argv
		return e
	}
	return apperr.New(apperr.CommandFailed, "%s could not start: %v", argv[0], err)
}

// hasExpo reports whether the package.json in dir lists expo as a dependency.
func hasExpo(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return false
	}
	var pkg struct {
		Dependencies    map[string]json.RawMessage `json:"dependencies"`
		DevDependencies map[string]json.RawMessage `json:"devDependencies"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return false
	}
	_, inDeps := pkg.Dependencies["expo"]
	_, inDev := pkg.DevDependencies["expo"]
	return inDeps || inDev
}

// findBin is the project's own copy of a tool, in node_modules/.bin of dir or a folder above it (a
// monorepo keeps one at the top). It is not looked for on the PATH or downloaded, so the project's
// version runs.
func findBin(dir, name string) (string, error) {
	for folder := dir; ; folder = filepath.Dir(folder) {
		path := filepath.Join(folder, "node_modules", ".bin", name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
		if folder == filepath.Dir(folder) {
			break
		}
	}
	return "", apperr.New(apperr.ToolMissing, "%s is not installed in %s", name, dir).
		WithHint("Install the project's packages first, for example with pnpm install, npm install or yarn.")
}

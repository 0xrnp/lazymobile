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
	"slices"
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
	// from is the file the way was found in, shown when there are several to choose from.
	from string
	// script is the text of the package.json script the plan runs, shown before it runs.
	script string
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
	// NoLog starts with the log hidden, so the tool has the whole width.
	NoLog bool
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
	noLog := fs.Bool("no-log", false, "")
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
	if *noLog && !*here {
		return usageError{"--no-log hides the log beside the tool that --here runs: add --here"}
	}
	if *here && (!a.Terminal || a.Here == nil) {
		return usageError{"--here needs a terminal to show the dashboard in"}
	}
	// What is remembered, and what a dashboard looks up, is keyed by the folder this was typed in.
	folder, err := a.getwd()
	if err != nil {
		return err
	}
	plans, err := a.runPlans(ctx, folder, extra)
	if err != nil {
		return err
	}
	if len(positional) == 1 {
		// A named device leaves out the ways to run on the other platform, such as an ios script.
		d, err := a.Manager.Find(ctx, positional[0])
		if err != nil {
			return err
		}
		plans = plansFor(plans, d.Platform)
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
	fmt.Fprintf(a.Stderr, "Running `%s` in %s\n", shownCommand(argv), plan.dir)
	if plan.script != "" {
		// The script runs whatever it says, so say it, not just its name.
		fmt.Fprintf(a.Stderr, "The script is `%s`\n", plan.script)
	}
	// A dashboard open in this folder follows the run: it selects this device and opens the app's log.
	_ = a.State.Update(func(f *state.File) { f.RememberRunStarted(folder, d.Key(), time.Now()) })
	if *here {
		title := plan.name
		if title == runAction {
			title = filepath.Base(argv[0])
		}
		return a.Here(ctx, a.Manager, HereRun{Dir: plan.dir, Folder: folder, Argv: argv, Title: title, Device: d, NoLog: *noLog})
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
// order, else the package.json scripts that run the app, else the tool the folder looks like it uses:
// Flutter, Expo, React Native, Gradle or Xcode.
func (a *App) runPlans(ctx context.Context, folder string, extra []string) ([]runPlan, error) {
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
		plans = append(plans, runPlan{name: chosen.Name, dir: chosen.Dir, fromFile: true, from: actions.ConfigFile, platform: chosen.Platform,
			argv: func(d device.Device) ([]string, error) {
				argv, err := chosen.Resolve(d, "")
				return append(argv, extra...), err
			}})
	}
	if len(plans) > 0 {
		// What the project wrote down is how it is run. The built-in tools are only a guess.
		return plans, nil
	}
	if plans = scriptPlans(folder, extra); len(plans) > 0 {
		// A project with an ios script and no android one still runs on Android with its own tool.
		if missing := uncovered(plans); missing != "" {
			builtIn, err := a.builtInPlans(ctx, folder, extra)
			if err != nil {
				return nil, err
			}
			for _, p := range builtIn {
				if p.platform == "" || p.platform == missing {
					p.platform = missing
					plans = append(plans, p)
				}
			}
		}
		return plans, nil
	}
	plans, err := a.builtInPlans(ctx, folder, extra)
	if err != nil {
		return nil, err
	}
	if len(plans) == 0 {
		return nil, apperr.New(apperr.NotFound, "Cannot tell how to run the app in %s", folder).
			WithHint("lazymobile run knows Flutter, Expo and React Native projects, native Android projects with a gradlew, Xcode projects, and package.json scripts that run one of them. For another tool, add an action named \"run\" to %s, for example {\"name\": \"run\", \"command\": [\"make\", \"run-dev\", \"DEVICE={device_id}\"]}. Add \"run dev\" and \"run prod\" for flavors.",
				actions.ConfigFile)
	}
	return plans, nil
}

// uncovered is the platform none of plans runs on, or "" when they cover both.
func uncovered(plans []runPlan) device.Platform {
	has := map[device.Platform]bool{}
	for _, p := range plans {
		if p.platform == "" {
			return ""
		}
		has[p.platform] = true
	}
	switch {
	case !has[device.IOS]:
		return device.IOS
	case !has[device.Android]:
		return device.Android
	}
	return ""
}

// builtInPlans runs a Flutter, Expo or React Native project with its own tool, and a native one
// with Gradle or Xcode.
func (a *App) builtInPlans(ctx context.Context, folder string, extra []string) ([]runPlan, error) {
	var plans []runPlan
	pkg, _ := readPackage(folder)
	if _, err := os.Stat(filepath.Join(folder, "pubspec.yaml")); err == nil {
		plans = append(plans, runPlan{name: "flutter run", dir: folder, from: "pubspec.yaml", argv: func(d device.Device) ([]string, error) {
			return append([]string{"flutter", "run", "-d", d.TargetID()}, extra...), nil
		}})
	} else if pkg.has("expo") {
		plans = append(plans, runPlan{name: "expo run", dir: folder, from: "package.json", argv: func(d device.Device) ([]string, error) {
			bin, err := findBin(folder, "expo")
			if err != nil {
				return nil, err
			}
			return append([]string{bin, "run:" + string(d.Platform), "--device", expoDevice(d)}, extra...), nil
		}})
	} else if pkg.has("react-native") {
		plans = append(plans, runPlan{name: "react-native run", dir: folder, from: "package.json", argv: func(d device.Device) ([]string, error) {
			bin, err := findBin(folder, "react-native")
			if err != nil {
				return nil, err
			}
			return append(append([]string{bin, "run-" + string(d.Platform)}, reactNativeDevice(d)...), extra...), nil
		}})
	} else {
		return a.nativePlans(ctx, folder, extra)
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
		items[i] = PickItem{Label: p.name}
		if p.from != "" {
			items[i].Detail = "from " + p.from
		}
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

// plansFor keeps the plans that can run on platform, unless that leaves none, so that the error
// about the device comes from where it always has.
func plansFor(plans []runPlan, platform device.Platform) []runPlan {
	var kept []runPlan
	for _, p := range plans {
		if p.platform == "" || p.platform == platform {
			kept = append(kept, p)
		}
	}
	if len(kept) == 0 {
		return plans
	}
	return kept
}

// packageJSON is what run reads from a package.json.
type packageJSON struct {
	Dependencies    map[string]json.RawMessage `json:"dependencies"`
	DevDependencies map[string]json.RawMessage `json:"devDependencies"`
	Scripts         map[string]string          `json:"scripts"`
	PackageManager  string                     `json:"packageManager"`
}

func readPackage(dir string) (packageJSON, error) {
	var pkg packageJSON
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return pkg, err
	}
	return pkg, json.Unmarshal(data, &pkg)
}

// has reports whether the package lists name as a dependency.
func (p packageJSON) has(name string) bool {
	_, inDeps := p.Dependencies[name]
	_, inDev := p.DevDependencies[name]
	return inDeps || inDev
}

// scriptPlans are the package.json scripts that end by running the app with Expo, React Native or
// Flutter, by name. Such a script is how the project is usually run, with its flavor, env file or
// entry file, so it is offered before lazymobile's own guess. The device is added at the end of the
// script, the way `npm run ios -- --device <id>` would. A script that names its own device is left
// out, since it would not run on the one chosen.
func scriptPlans(folder string, extra []string) []runPlan {
	pkg, err := readPackage(folder)
	if err != nil {
		return nil
	}
	manager := packageManager(folder, pkg)
	var names []string
	for name := range pkg.Scripts {
		names = append(names, name)
	}
	slices.Sort(names)
	var plans []runPlan
	for _, name := range names {
		tool, platform, ok := scriptTool(pkg.Scripts[name])
		if !ok {
			continue
		}
		script := []string{manager, "run", name}
		if manager == "npm" {
			// npm keeps the flags after the script's name for itself unless they follow --.
			script = append(script, "--")
		}
		plans = append(plans, runPlan{name: strings.Join(script[:3], " "), dir: folder, from: "package.json", script: pkg.Scripts[name], platform: platform,
			argv: func(d device.Device) ([]string, error) {
				if platform != "" && platform != d.Platform {
					return nil, apperr.New(apperr.Unsupported, "The %q script is for %s, and %s is %s", name, platform, d.Name, d.Platform)
				}
				return append(append(slices.Clone(script), toolDevice(tool, d)...), extra...), nil
			}})
	}
	return plans
}

// scriptTool is the tool the last command of a script runs the app with, and the platform it runs
// on (empty for both). ok is false for a script that does something else, or names its own device.
func scriptTool(script string) (tool string, platform device.Platform, ok bool) {
	last := script
	for _, sep := range []string{"&&", "||", ";"} {
		if i := strings.LastIndex(last, sep); i >= 0 {
			last = last[i+len(sep):]
		}
	}
	words := strings.Fields(last)
	for _, w := range words {
		if strings.ContainsAny(w, "|>") || slices.Contains([]string{"--device", "--udid", "--simulator", "-d", "--device-id", "--deviceId"}, strings.SplitN(w, "=", 2)[0]) {
			return "", "", false
		}
	}
	for i := 0; i+1 < len(words); i++ {
		switch command := words[i] + " " + words[i+1]; command {
		case "expo run:ios", "react-native run-ios":
			return words[i], device.IOS, true
		case "expo run:android", "react-native run-android":
			return words[i], device.Android, true
		case "flutter run":
			return words[i], "", true
		}
	}
	return "", "", false
}

// toolDevice are the flags that point tool at d.
func toolDevice(tool string, d device.Device) []string {
	switch tool {
	case "flutter":
		return []string{"-d", d.TargetID()}
	case "react-native":
		return reactNativeDevice(d)
	case "expo":
		return []string{"--device", expoDevice(d)}
	}
	return []string{"--device", d.TargetID()}
}

// expoDevice is how `expo run` finds d. On iOS that is the UDID. On Android it is a name, not the
// adb serial: an emulator's AVD name, and a phone's model as adb lists it (Pixel_7).
func expoDevice(d device.Device) string {
	switch {
	case d.Platform == device.IOS:
		return d.TargetID()
	case d.Kind == device.Emulator:
		return d.ID
	case d.Model != "":
		return d.Model
	}
	return "Device " + d.TargetID()
}

// reactNativeDevice points the React Native CLI at d: run-ios takes the UDID, and run-android the
// adb serial (its older --deviceId is deprecated).
func reactNativeDevice(d device.Device) []string {
	if d.Platform == device.IOS {
		return []string{"--udid", d.TargetID()}
	}
	return []string{"--device", d.TargetID()}
}

// packageManager is the one the project uses: the packageManager field of package.json, else the
// one whose lock file is in folder or a folder above it (a monorepo keeps it at the top), else npm.
func packageManager(folder string, pkg packageJSON) string {
	known := []string{"pnpm", "yarn", "bun", "npm"}
	if name, _, _ := strings.Cut(pkg.PackageManager, "@"); slices.Contains(known, name) {
		return name
	}
	locks := [][2]string{{"pnpm-lock.yaml", "pnpm"}, {"yarn.lock", "yarn"}, {"bun.lock", "bun"}, {"bun.lockb", "bun"}, {"package-lock.json", "npm"}}
	for dir := folder; ; dir = filepath.Dir(dir) {
		for _, lock := range locks {
			if _, err := os.Stat(filepath.Join(dir, lock[0])); err == nil {
				return lock[1]
			}
		}
		if dir == filepath.Dir(dir) {
			return "npm"
		}
	}
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

// shownCommand is argv as the user would type it: the script itself for `sh -c`, which a native
// build uses to build, install and launch in one command.
func shownCommand(argv []string) string {
	if len(argv) == 3 && argv[0] == "sh" && argv[1] == "-c" {
		return argv[2]
	}
	return strings.Join(argv, " ")
}

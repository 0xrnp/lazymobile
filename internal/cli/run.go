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

// runAction is the name of the action in .lazymobile.json that `lazymobile run` uses before it
// tries to tell what the project is.
const runAction = "run"

// runPlan is how the project in a folder is run: the command, given a device.
type runPlan struct {
	dir string
	// fromFile is whether the command comes from .lazymobile.json, which a repository someone else
	// wrote can set, so it is shown and needs --yes.
	fromFile bool
	// platform limits the plan to one platform. Empty means both.
	platform device.Platform
	argv     func(d device.Device) ([]string, error)
}

// runProject runs the app in this folder with the tool the project already uses, on a device that
// lazymobile finds and boots first. The tool keeps the terminal, so its keys work as before.
func (a *App) runProject(ctx context.Context, args []string) error {
	own, extra := splitTool(args)
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "")
	positional, err := parse(fs, own)
	if err != nil {
		return err
	}
	if len(positional) > 1 {
		return usageError{"run takes one device, and the tool's own flags after --"}
	}
	plan, err := a.planRun(extra)
	if err != nil {
		return err
	}
	d, err := a.runDevice(ctx, plan, positional)
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
	return a.runTool(ctx, plan.dir, argv)
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

// planRun finds how the project in the current folder is run: an action named "run" in
// .lazymobile.json first, then what the folder looks like.
func (a *App) planRun(extra []string) (runPlan, error) {
	var all []actions.Action
	if a.Actions != nil {
		var err error
		if all, err = a.Actions(); err != nil {
			return runPlan{}, err
		}
	}
	for _, chosen := range all {
		if chosen.Name == runAction {
			return runPlan{dir: chosen.Dir, fromFile: true, platform: chosen.Platform,
				argv: func(d device.Device) ([]string, error) {
					argv, err := chosen.Resolve(d, "")
					return append(argv, extra...), err
				}}, nil
		}
	}
	dir, err := a.getwd()
	if err != nil {
		return runPlan{}, err
	}
	if _, err := os.Stat(filepath.Join(dir, "pubspec.yaml")); err == nil {
		return runPlan{dir: dir, argv: func(d device.Device) ([]string, error) {
			return append([]string{"flutter", "run", "-d", d.TargetID()}, extra...), nil
		}}, nil
	}
	if hasExpo(dir) {
		bin, err := findBin(dir, "expo")
		if err != nil {
			return runPlan{}, err
		}
		return runPlan{dir: dir, argv: func(d device.Device) ([]string, error) {
			return append([]string{bin, "run:" + string(d.Platform), "--device", d.TargetID()}, extra...), nil
		}}, nil
	}
	return runPlan{}, apperr.New(apperr.NotFound, "Cannot tell how to run the app in %s", dir).
		WithHint("lazymobile run knows Flutter and Expo projects (a pubspec.yaml, or expo in package.json). For another tool, add an action named %q to %s, for example {\"name\": \"run\", \"command\": [\"npx\", \"expo\", \"run:ios\", \"--device\", \"{device_id}\"]}.",
			runAction, actions.ConfigFile)
}

// runDevice is the device that was named, else the one this folder was last run on, else the one
// simulator or emulator that is running. A real phone is only used when it is named, and is not
// remembered, so an app never lands on one because it was plugged in or used before.
func (a *App) runDevice(ctx context.Context, plan runPlan, positional []string) (device.Device, error) {
	if len(positional) == 1 {
		d, err := a.Manager.Find(ctx, positional[0])
		if err == nil {
			a.rememberRun(plan.dir, d)
		}
		return d, err
	}
	all, errs := a.Manager.List(ctx)
	fits := func(d device.Device) bool {
		return d.Kind != device.Physical && (plan.platform == "" || plan.platform == d.Platform)
	}
	last := a.State.Load().Runs[plan.dir].Device
	for _, d := range all {
		if d.Key() == last && fits(d) {
			fmt.Fprintf(a.Stderr, "Using %s, the device this folder was last run on\n", d.Name)
			return d, nil
		}
	}
	var running []device.Device
	for _, d := range all {
		if fits(d) && d.Ready() {
			running = append(running, d)
		}
	}
	switch len(running) {
	case 1:
		a.rememberRun(plan.dir, running[0])
		return running[0], nil
	case 0:
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

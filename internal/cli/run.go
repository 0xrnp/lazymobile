package cli

import (
	"context"
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
	return runPlan{}, apperr.New(apperr.NotFound, "Cannot tell how to run the app in %s", dir).
		WithHint("lazymobile run knows Flutter projects (a pubspec.yaml). For another tool, add an action named %q to %s, for example {\"name\": \"run\", \"command\": [\"npx\", \"expo\", \"run:ios\", \"--device\", \"{device_id}\"]}.",
			runAction, actions.ConfigFile)
}

// runDevice is the device that was named, or the one simulator or emulator that is running. A real
// phone is only used when it is named, so an app never lands on one because it was plugged in.
func (a *App) runDevice(ctx context.Context, plan runPlan, positional []string) (device.Device, error) {
	if len(positional) == 1 {
		return a.Manager.Find(ctx, positional[0])
	}
	all, errs := a.Manager.List(ctx)
	var running []device.Device
	for _, d := range all {
		if d.Kind != device.Physical && d.Ready() && (plan.platform == "" || plan.platform == d.Platform) {
			running = append(running, d)
		}
	}
	switch len(running) {
	case 1:
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

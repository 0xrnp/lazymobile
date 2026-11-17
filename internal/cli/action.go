package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/0xrnp/lazymobile/internal/actions"
	"github.com/0xrnp/lazymobile/internal/apperr"
)

// action lists the project's own commands, or runs one on a device.
func (a *App) action(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("action", flag.ContinueOnError)
	list := fs.Bool("list", false, "")
	yes := fs.Bool("yes", false, "")
	app := fs.String("app", "", "")
	asJSON := fs.Bool("json", false, "")
	positional, err := parse(fs, args)
	if err != nil {
		return err
	}
	var all []actions.Action
	if a.Actions != nil {
		if all, err = a.Actions(); err != nil {
			return err
		}
	}
	if *list {
		return a.listActions(all, *asJSON)
	}
	if len(positional) != 2 {
		return usageError{"action needs a device and an action name, or --list"}
	}
	d, err := a.Manager.Find(ctx, positional[0])
	if err != nil {
		return err
	}
	chosen, err := actions.Find(all, positional[1])
	if err != nil {
		return err
	}
	argv, err := chosen.Resolve(d, *app)
	if err != nil {
		return err
	}
	// The file can come from a repository someone else wrote, so nothing runs unseen.
	if !*yes {
		return usageError{fmt.Sprintf("%q would run `%s` in %s. Add --yes to run it",
			chosen.Name, strings.Join(argv, " "), chosen.Dir)}
	}
	cmd := chosen.Prepare(ctx, argv)
	cmd.Stdin = os.Stdin
	cmd.Stdout, cmd.Stderr = a.Stdout, a.Stderr
	if *asJSON {
		// The command's own output must not break the JSON.
		cmd.Stdout = a.Stderr
	}
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case errors.As(err, &exit):
			e := apperr.New(apperr.CommandFailed, "%q exited with code %d", chosen.Name, exit.ExitCode())
			e.Argv, e.ExitCode = argv, exit.ExitCode()
			return e
		case errors.Is(err, exec.ErrNotFound):
			e := apperr.New(apperr.ToolMissing, "%s is not installed or not on your PATH", argv[0])
			e.Argv = argv
			return e
		}
		return apperr.New(apperr.CommandFailed, "%q could not start: %v", chosen.Name, err)
	}
	return a.done(*asJSON, result{Action: "action", Name: chosen.Name, Device: d, App: *app, Command: argv},
		fmt.Sprintf("Ran %q on %s", chosen.Name, d.Name))
}

func (a *App) listActions(all []actions.Action, asJSON bool) error {
	if asJSON {
		if all == nil {
			all = []actions.Action{}
		}
		return a.writeJSON(all)
	}
	if len(all) == 0 {
		fmt.Fprintln(a.Stdout, "No actions. Add them to "+actions.ConfigFile+", see the README.")
		return nil
	}
	for _, item := range all {
		scope := ""
		if item.Platform != "" {
			scope = " [" + string(item.Platform) + " only]"
		}
		fmt.Fprintf(a.Stdout, "%s%s\n    %s\n    in %s\n", item.Name, scope, strings.Join(item.Command, " "), item.Dir)
	}
	return nil
}

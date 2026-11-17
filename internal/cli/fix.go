package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/0xrnp/lazymobile/internal/doctor"
)

// fixable are the problems that have a command to fix them.
func fixable(report doctor.Report) []doctor.Problem {
	var out []doctor.Problem
	for _, p := range report.Problems {
		if len(p.Fix) > 0 {
			out = append(out, p)
		}
	}
	return out
}

// fix shows the command for each fixable problem and runs it only after the person says yes (or
// --yes was given, which also covers a script). It returns whether anything ran.
func (a *App) fix(ctx context.Context, report doctor.Report, yes bool) (bool, error) {
	list := fixable(report)
	if len(list) == 0 {
		fmt.Fprintln(a.Stdout, "\nNothing here can be fixed with one command.")
		return false, nil
	}
	if !yes && !a.Interactive {
		for _, p := range list {
			fmt.Fprintf(a.Stdout, "\n[%s] %s\n    %s\n", p.Platform, p.Message, strings.Join(p.Fix, " "))
		}
		fmt.Fprintln(a.Stdout, "\nNothing was run, because this is not a terminal to ask in. Add --yes to run these.")
		return false, nil
	}
	input := bufio.NewReader(a.Stdin)
	ran := false
	for _, p := range list {
		command := strings.Join(p.Fix, " ")
		fmt.Fprintf(a.Stdout, "\n[%s] %s\n    %s\n", p.Platform, p.Message, command)
		if !yes {
			fmt.Fprint(a.Stdout, "Run it? [y/N] ")
			answer, _ := input.ReadString('\n')
			if strings.ToLower(strings.TrimSpace(answer)) != "y" {
				fmt.Fprintln(a.Stdout, "Skipped.")
				continue
			}
		}
		ran = true
		if err := a.runFix(ctx, p.Fix); err != nil {
			fmt.Fprintf(a.Stdout, "It failed: %v\n", err)
		}
		if ctx.Err() != nil {
			return ran, ctx.Err()
		}
	}
	return ran, nil
}

func (a *App) runFix(ctx context.Context, argv []string) error {
	if a.RunFix != nil {
		return a.RunFix(ctx, argv)
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	// A fix such as sudo asks for a password, so it gets the terminal.
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, a.Stdout, a.Stderr
	return cmd.Run()
}

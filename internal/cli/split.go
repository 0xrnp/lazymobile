package cli

import (
	"context"
	"os"
	"os/exec"
	"strings"

	"github.com/0xrnp/lazymobile/internal/apperr"
)

// splitCommand is the command that opens exe in a new pane to the right of the terminal where
// lazymobile run was typed, working in dir. name is the multiplexer it is for. It is nil when the
// terminal is not in one that is known: Rex, tmux or Zellij.
func splitCommand(getenv func(string) string, exe, dir string) (name string, argv []string) {
	// tmux and Zellij come before Rex, since they are the ones run inside a Rex terminal, which
	// leaves its own variables set in every pane in them. The innermost multiplexer is the one to ask.
	switch {
	case getenv("TMUX") != "" && getenv("TMUX_PANE") != "":
		return "tmux", []string{"tmux", "split-window", "-h", "-d", "-t", getenv("TMUX_PANE"), "-c", dir, exe}
	case getenv("ZELLIJ") != "":
		// Zellij has no way to leave the focus where it is, so the new pane is focused.
		return "Zellij", []string{"zellij", "action", "new-pane", "--direction", "right", "--cwd", dir, "--", exe}
	case getenv("REX_SESSION") != "" && getenv("REX_BLOCK") != "":
		// --focus=false keeps the cursor in the pane that is about to run the tool.
		return "Rex", []string{"rex", "split", "-s", getenv("REX_SESSION"), "-b", getenv("REX_BLOCK"),
			"--split=right", "--focus=false", "--cwd", dir, "--", exe}
	}
	return "", nil
}

func (a *App) env(name string) string {
	if a.Getenv != nil {
		return a.Getenv(name)
	}
	return os.Getenv(name)
}

func (a *App) executable() string {
	if a.Executable != "" {
		return a.Executable
	}
	if exe, err := os.Executable(); err == nil {
		return exe
	}
	return "lazymobile"
}

// spawn runs a short command that opens a pane, and reports its failure with what it said. Unlike
// runTool it does not give it the terminal, since it is not the tool the user is running.
func (a *App) spawn(ctx context.Context, dir string, argv []string) error {
	if a.RunTool != nil {
		return a.RunTool(ctx, dir, argv)
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	said, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	if said == "" {
		said = err.Error()
	}
	return apperr.New(apperr.CommandFailed, "%s: %s", argv[0], said)
}

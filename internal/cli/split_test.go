package cli

import (
	"strings"
	"testing"
)

func envOf(pairs ...string) func(string) string {
	return func(name string) string {
		for i := 0; i+1 < len(pairs); i += 2 {
			if pairs[i] == name {
				return pairs[i+1]
			}
		}
		return ""
	}
}

func TestSplitCommandFindsTheMultiplexerTheTerminalIsIn(t *testing.T) {
	for name, tt := range map[string]struct {
		env  func(string) string
		want string
		by   string
	}{
		"rex":    {envOf("REX_SESSION", "session:1", "REX_BLOCK", "block:2"), "rex split -s session:1 -b block:2 --split=right --focus=false --cwd /work -- /bin/lazymobile", "Rex"},
		"tmux":   {envOf("TMUX", "/tmp/tmux-1/default,1,0", "TMUX_PANE", "%4"), "tmux split-window -h -d -t %4 -c /work /bin/lazymobile", "tmux"},
		"zellij": {envOf("ZELLIJ", "0"), "zellij action new-pane --direction right --cwd /work -- /bin/lazymobile", "Zellij"},
		// Rex leaves its variables set in a tmux or Zellij run inside it, and the inner one wins.
		"tmux in rex":   {envOf("REX_SESSION", "session:1", "REX_BLOCK", "block:2", "TMUX", "x,1,0", "TMUX_PANE", "%4"), "tmux split-window -h -d -t %4 -c /work /bin/lazymobile", "tmux"},
		"zellij in rex": {envOf("REX_SESSION", "session:1", "REX_BLOCK", "block:2", "ZELLIJ", "0"), "zellij action new-pane --direction right --cwd /work -- /bin/lazymobile", "Zellij"},
		"nothing":       {envOf(), "", ""},
		// Half of Rex's variables, or tmux without a pane, is not enough to know where to split.
		"half of rex": {envOf("REX_SESSION", "session:1"), "", ""},
		"tmux alone":  {envOf("TMUX", "x"), "", ""},
	} {
		by, argv := splitCommand(tt.env, "/bin/lazymobile", "/work")
		if got := strings.Join(argv, " "); got != tt.want || by != tt.by {
			t.Errorf("%s: %q for %q, want %q for %q", name, got, by, tt.want, tt.by)
		}
	}
}

func TestRunSplitOpensTheDashboardBesideBeforeTheToolRuns(t *testing.T) {
	e := newRunEnv(t, "pubspec.yaml")
	e.app.Getenv = envOf("REX_SESSION", "session:1", "REX_BLOCK", "block:2")
	e.app.Executable = "/bin/lazymobile"
	if code := e.run("run", "iPhone 16 Pro", "--split"); code != ExitOK {
		t.Fatalf("code %d, stderr %q", code, e.errs.String())
	}
	if len(e.ran) != 2 || e.ran[0][0] != "rex" || e.ran[1][0] != "flutter" {
		t.Fatalf("the pane was not opened first, then the tool: %v", e.ran)
	}
	if e.where[0] != e.dir || e.ran[0][len(e.ran[0])-1] != "/bin/lazymobile" || !strings.Contains(strings.Join(e.ran[0], " "), "--cwd "+e.dir) {
		t.Errorf("the dashboard opens in the wrong place: %v in %v", e.ran[0], e.where[0])
	}
	if !strings.Contains(e.errs.String(), "Opened lazymobile beside this terminal in Rex") || strings.Contains(e.errs.String(), "in another terminal") {
		t.Errorf("stderr = %q", e.errs.String())
	}
	// The run is recorded before the pane opens, so the dashboard has it to follow.
	if got := e.app.State.Load().Runs[e.dir]; got.Last == "" || got.Started.IsZero() {
		t.Errorf("the run was not recorded: %+v", got)
	}
}

func TestRunSplitWithoutAMultiplexerDoesNotRunTheTool(t *testing.T) {
	e := newRunEnv(t, "pubspec.yaml")
	e.app.Getenv = envOf()
	if code := e.run("run", "iPhone 16 Pro", "--split"); code != ExitFailure || len(e.ran) != 0 ||
		!strings.Contains(e.errs.String(), "Rex, tmux or Zellij") {
		t.Errorf("code %d, ran %v, stderr %q", code, e.ran, e.errs.String())
	}
}

func TestRunWithoutSplitSaysHowToGetThere(t *testing.T) {
	e := newRunEnv(t, "pubspec.yaml")
	if code := e.run("run", "iPhone 16 Pro"); code != ExitOK || !strings.Contains(e.errs.String(), "or add --split") {
		t.Errorf("code %d, stderr %q", code, e.errs.String())
	}
}

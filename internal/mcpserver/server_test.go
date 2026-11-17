package mcpserver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/0xrnp/lazymobile/internal/android"
	"github.com/0xrnp/lazymobile/internal/doctor"
	"github.com/0xrnp/lazymobile/internal/mobile"
	"github.com/0xrnp/lazymobile/internal/run"
	"github.com/0xrnp/lazymobile/internal/run/runtest"
	"github.com/0xrnp/lazymobile/internal/state"
)

const simctlList = `{"devices": {"com.apple.CoreSimulator.SimRuntime.iOS-18-2": [
  {"udid": "00000000-0000-4000-8000-0000000000A1", "name": "iPhone 16", "state": "Shutdown", "isAvailable": true},
  {"udid": "00000000-0000-4000-8000-0000000000A2", "name": "iPhone 16 Pro", "state": "Booted", "isAvailable": true}
]}}`

// connect serves the tools over memory, with two simulators and no Android SDK.
func connect(t *testing.T) (*mcp.ClientSession, *runtest.Fake, state.Store) {
	t.Helper()
	fake := &runtest.Fake{Answer: func(command string, argv []string, _ run.Options) run.Result {
		switch {
		case strings.Contains(command, "simctl list devices"):
			return runtest.Out(simctlList)
		case strings.Contains(command, "devicectl list devices"):
			if err := os.WriteFile(argv[len(argv)-1], []byte(`{"result": {"devices": []}}`), 0o600); err != nil {
				t.Error(err)
			}
			return run.Result{}
		case strings.Contains(command, "simctl io"):
			if err := os.WriteFile(argv[len(argv)-1], []byte("\x89PNG\r\n\x1a\n"), 0o644); err != nil {
				t.Error(err)
			}
			return run.Result{}
		case strings.HasPrefix(command, "xcrun simctl"), strings.HasPrefix(command, "open "):
			return run.Result{}
		}
		return run.Result{Missing: true}
	}}
	options := android.Options{
		OS: "darwin", Home: t.TempDir(), Getenv: func(string) string { return "" },
		LookPath:     func(string) (string, error) { return "", errors.New("not found") },
		SDKFallbacks: []string{}, JavaFallbacks: []string{},
	}
	store := state.Store{Path: filepath.Join(t.TempDir(), "state.json")}
	s := &Server{
		Manager: mobile.New(fake, options, "darwin"),
		Doctor:  func(context.Context) doctor.Report { return doctor.Report{OS: "darwin"} },
		State:   store,
		Version: "test",
	}
	ctx := context.Background()
	serverSide, clientSide := mcp.NewInMemoryTransports()
	if _, err := s.New().Connect(ctx, serverSide, nil); err != nil {
		t.Fatal(err)
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, clientSide, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session, fake, store
}

func call(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res
}

func textOf(res *mcp.CallToolResult) string {
	var parts []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func TestToolsLeaveOutWhatErasesData(t *testing.T) {
	session, _, _ := connect(t)
	list, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range list.Tools {
		names = append(names, tool.Name)
	}
	for _, want := range []string{"list_devices", "boot", "launch", "screenshot", "read_logs", "set_permission"} {
		if !slices.Contains(names, want) {
			t.Errorf("missing %s in %v", want, names)
		}
	}
	for _, refused := range []string{"erase", "delete", "uninstall", "clear_data"} {
		if slices.Contains(names, refused) {
			t.Errorf("%s is served", refused)
		}
	}
}

func TestListDevices(t *testing.T) {
	session, _, _ := connect(t)
	res := call(t, session, "list_devices", nil)
	if res.IsError || !strings.Contains(textOf(res), "iPhone 16 Pro") {
		t.Errorf("got %q", textOf(res))
	}
}

func TestAnUnknownDeviceIsAnErrorWithItsHint(t *testing.T) {
	session, _, _ := connect(t)
	res := call(t, session, "boot", map[string]any{"device": "Pixel"})
	// The hint follows the message on its own line.
	if !res.IsError || !strings.Contains(textOf(res), "No device matches \"Pixel\"\n") {
		t.Errorf("error %v, text %q", res.IsError, textOf(res))
	}
}

func TestLaunchRemembersTheApp(t *testing.T) {
	session, fake, store := connect(t)
	res := call(t, session, "launch", map[string]any{"device": "iPhone 16 Pro", "app": "com.example.app"})
	if res.IsError || !fake.Called("simctl launch") {
		t.Fatalf("text %q, calls %v", textOf(res), fake.Calls())
	}
	if recent := store.Load().Apps; len(recent) == 0 {
		t.Error("the app id was not remembered")
	}
}

func TestScreenshotReturnsTheImage(t *testing.T) {
	session, _, _ := connect(t)
	res := call(t, session, "screenshot", map[string]any{"device": "iPhone 16 Pro"})
	if res.IsError || len(res.Content) != 1 {
		t.Fatalf("got %q", textOf(res))
	}
	image, ok := res.Content[0].(*mcp.ImageContent)
	if !ok || image.MIMEType != "image/png" || !strings.HasPrefix(string(image.Data), "\x89PNG") {
		t.Errorf("got %#v", res.Content[0])
	}
}

// fakeXcrun puts an xcrun on PATH that prints these log lines, since a log is read straight from
// the tool and not through the runner.
func fakeXcrun(t *testing.T, lines ...string) {
	t.Helper()
	dir := t.TempDir()
	body := "#!/bin/sh\ncat <<'EOF'\n" + strings.Join(lines, "\n") + "\nEOF\n"
	if err := os.WriteFile(filepath.Join(dir, "xcrun"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestReadLogsKeepsTheLastLines(t *testing.T) {
	fakeXcrun(t,
		"2026-11-13 20:56:31.411 Df Runner[30348:6e9c9] line1",
		"2026-11-13 20:56:32.411 E Runner[30348:6e9c9] line2",
		"2026-11-13 20:56:33.411 Df Runner[30348:6e9c9] line3",
	)
	session, _, _ := connect(t)
	res := call(t, session, "read_logs", map[string]any{"device": "iPhone 16 Pro", "max_lines": 2, "since": "1m"})
	out := textOf(res)
	if res.IsError || strings.Contains(out, "line1") || !strings.Contains(out, "line3") || !strings.Contains(out, "the last 2 of 3") {
		t.Errorf("got %q", out)
	}
}

func TestReadLogsFilters(t *testing.T) {
	fakeXcrun(t,
		"2026-11-13 20:56:31.411 Df Runner[30348:6e9c9] starting",
		"2026-11-13 20:56:32.411 E Runner[30348:6e9c9] Terminating app due to uncaught exception",
		"2026-11-13 20:56:33.411 E Runner[30348:6e9c9] network failed",
	)
	session, _, _ := connect(t)
	tests := map[string]struct {
		args       map[string]any
		want, skip string
	}{
		"level":   {map[string]any{"level": "error"}, "network failed", "starting"},
		"crashes": {map[string]any{"crashes_only": true}, "uncaught exception", "network failed"},
		"text":    {map[string]any{"text": "network"}, "network failed", "uncaught"},
	}
	for name, tt := range tests {
		tt.args["device"] = "iPhone 16 Pro"
		res := call(t, session, "read_logs", tt.args)
		out := textOf(res)
		if res.IsError || !strings.Contains(out, tt.want) || strings.Contains(out, tt.skip) {
			t.Errorf("%s: got %q", name, out)
		}
	}
}

func TestReadLogsRefusesBadArguments(t *testing.T) {
	session, _, _ := connect(t)
	for _, args := range []map[string]any{
		{"device": "iPhone 16 Pro", "since": "soon"},
		{"device": "iPhone 16 Pro", "level": "loud"},
		{"device": "iPhone 16 Pro", "max_lines": -1},
	} {
		if res := call(t, session, "read_logs", args); !res.IsError {
			t.Errorf("%v: no error, got %q", args, textOf(res))
		}
	}
}

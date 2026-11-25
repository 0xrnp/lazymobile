package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/0xrnp/lazymobile/internal/device"
)

// runServer is a server whose project runs a small script, like flutter run: it says it is
// launching, then reacts to r, R and q.
func runServer(t *testing.T, script string, prepare func(RunRequest) error) *mcp.ClientSession {
	t.Helper()
	s := &Server{Version: "test", PrepareRun: func(_ context.Context, req RunRequest) (PreparedRun, error) {
		if prepare != nil {
			if err := prepare(req); err != nil {
				return PreparedRun{}, err
			}
		}
		return PreparedRun{Dir: t.TempDir(), Argv: []string{"sh", "-c", script}, Device: device.Device{Name: "Pixel 9a"}, Title: "demo"}, nil
	}}
	t.Cleanup(s.runs.stopAll)
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
	return session
}

func runView1(t *testing.T, res *mcp.CallToolResult) runView {
	t.Helper()
	if res.IsError {
		t.Fatalf("error: %s", textOf(res))
	}
	var v runView
	if err := json.Unmarshal([]byte(textOf(res)), &v); err != nil {
		t.Fatalf("%v in %q", err, textOf(res))
	}
	return v
}

const demoTool = `stty -icanon -echo; echo "Launching lib/main.dart"; while c=$(dd bs=1 count=1 2>/dev/null); do
case "$c" in r) echo "Reloaded 1 library";; R) echo "Restarted";; q) echo "bye"; exit 3;; esac; done`

func TestARunIsStartedReloadedReadAndStopped(t *testing.T) {
	session := runServer(t, demoTool, nil)
	start := runView1(t, call(t, session, "run_start", map[string]any{"wait_seconds": 5}))
	if start.State != "running" || start.Device != "Pixel 9a" || !strings.Contains(start.Output, "Launching lib/main.dart") {
		t.Fatalf("start: %+v", start)
	}
	reload := runView1(t, call(t, session, "run_reload", map[string]any{"id": start.ID}))
	if !strings.Contains(reload.Output, "Reloaded 1 library") {
		t.Errorf("reload: %q", reload.Output)
	}
	restart := runView1(t, call(t, session, "run_reload", map[string]any{"id": start.ID, "restart": true}))
	if !strings.Contains(restart.Output, "Restarted") {
		t.Errorf("restart: %q", restart.Output)
	}
	status := runView1(t, call(t, session, "run_status", map[string]any{"id": start.ID, "lines": 2}))
	if status.State != "running" || strings.Count(status.Output, "\n") != 1 {
		t.Errorf("status: %+v", status)
	}
	stopped := runView1(t, call(t, session, "run_stop", map[string]any{"id": start.ID}))
	if stopped.State != "exited" {
		t.Errorf("stop: %+v", stopped)
	}
	if res := call(t, session, "run_reload", map[string]any{"id": start.ID}); !res.IsError || !strings.Contains(textOf(res), "has ended") {
		t.Errorf("reload after stop: %q", textOf(res))
	}
}

func TestARunThatEndsSaysItsExitCode(t *testing.T) {
	session := runServer(t, `echo "Gradle task failed"; exit 7`, nil)
	v := runView1(t, call(t, session, "run_start", map[string]any{"wait_seconds": 5}))
	if v.State != "exited" || v.ExitCode == nil || *v.ExitCode != 7 || !strings.Contains(v.Output, "Gradle task failed") {
		t.Errorf("%+v", v)
	}
}

func TestRunStartPassesWhatWasAskedAndNeverMoreThanThreeRuns(t *testing.T) {
	var got RunRequest
	session := runServer(t, demoTool, func(r RunRequest) error { got = r; return nil })
	args := map[string]any{"folder": "/work/app", "device": "Pixel", "profile": "run dev", "yes": true, "flags": []string{"--flavor", "dev"}, "wait_seconds": 1}
	for i := 0; i < 3; i++ {
		runView1(t, call(t, session, "run_start", args))
	}
	if got.Folder != "/work/app" || got.Device != "Pixel" || got.Profile != "run dev" || !got.Yes || strings.Join(got.Flags, " ") != "--flavor dev" {
		t.Errorf("request %+v", got)
	}
	if res := call(t, session, "run_start", args); !res.IsError || !strings.Contains(textOf(res), "already going") {
		t.Errorf("a fourth run: %q", textOf(res))
	}
	if res := call(t, session, "run_status", map[string]any{"id": "run-99"}); !res.IsError {
		t.Error("an unknown id was accepted")
	}
}

func TestRunToolsAreOffWithoutAProject(t *testing.T) {
	session, _, _ := connect(t)
	list, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range list.Tools {
		if strings.HasPrefix(tool.Name, "run_") {
			t.Errorf("%s is offered", tool.Name)
		}
	}
}

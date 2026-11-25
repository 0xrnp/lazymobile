package mcpserver

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
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

// screen is what the fake simctl saves as a screenshot: a PNG taller than a model is shown.
var screen = func() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 600, 1700))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}()

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
			if err := os.WriteFile(argv[len(argv)-1], screen, 0o644); err != nil {
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
	manager := mobile.New(fake, options, "darwin")
	manager.IOS.CrashDir = t.TempDir()
	s := &Server{
		Manager: manager,
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
	// Without a hint, a client may ask before every call.
	for _, tool := range list.Tools {
		a := tool.Annotations
		if a == nil || (!a.ReadOnlyHint && (a.DestructiveHint == nil || *a.DestructiveHint)) {
			t.Errorf("%s may be taken as destructive: %+v", tool.Name, a)
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

func screenshotOf(t *testing.T, session *mcp.ClientSession, args map[string]any) *mcp.ImageContent {
	t.Helper()
	res := call(t, session, "screenshot", args)
	// The picture, and a line with the screen's size, which a tap is given in.
	if res.IsError || len(res.Content) != 2 || !strings.Contains(textOf(res), "The screen is 600x1700 pixels.") {
		t.Fatalf("got %q", textOf(res))
	}
	img, ok := res.Content[0].(*mcp.ImageContent)
	if !ok {
		t.Fatalf("got %#v", res.Content[0])
	}
	return img
}

func TestScreenshotIsScaledForTheModel(t *testing.T) {
	session, _, _ := connect(t)
	img := screenshotOf(t, session, map[string]any{"device": "iPhone 16 Pro"})
	if img.MIMEType != "image/jpeg" {
		t.Fatalf("type %s", img.MIMEType)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(img.Data))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width != 300 || cfg.Height != 850 {
		t.Errorf("got %dx%d, want 300x850", cfg.Width, cfg.Height)
	}
}

func TestScreenshotAtFullSizeIsThePNG(t *testing.T) {
	session, _, _ := connect(t)
	img := screenshotOf(t, session, map[string]any{"device": "iPhone 16 Pro", "full_size": true})
	if img.MIMEType != "image/png" || !bytes.Equal(img.Data, screen) {
		t.Errorf("got %s of %d bytes", img.MIMEType, len(img.Data))
	}
}

func TestFitForModelKeepsWhatIsAlreadySmall(t *testing.T) {
	var small bytes.Buffer
	if err := png.Encode(&small, image.NewRGBA(image.Rect(0, 0, 100, maxEdge))); err != nil {
		t.Fatal(err)
	}
	for name, in := range map[string][]byte{"small": small.Bytes(), "unreadable": []byte("\x89PNG\r\n\x1a\n")} {
		if out, mime := fitForModel(in); mime != "image/png" || !bytes.Equal(out, in) {
			t.Errorf("%s: changed to %s", name, mime)
		}
	}
}

func TestShrinkAveragesEachBlock(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 2, 2))
	copy(src.Pix, []uint8{0, 0, 0, 255, 100, 0, 0, 255, 0, 200, 0, 255, 0, 0, 40, 255})
	got := shrink(src, 2).Pix
	if want := []uint8{25, 50, 10, 255}; !bytes.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestCrashReportSaysWhereReportsAreWhenThereIsNone(t *testing.T) {
	session, _, _ := connect(t)
	res := call(t, session, "crash_report", map[string]any{"device": "iPhone 16 Pro", "app": "com.example.app"})
	if out := textOf(res); !res.IsError || !strings.Contains(out, "No crash of com.example.app") || !strings.Contains(out, "crash reports are in") {
		t.Errorf("error %v, text %q", res.IsError, out)
	}
}

func TestSetStatusBar(t *testing.T) {
	for mode, want := range map[string]string{"clean": "override", "reset": "clear"} {
		session, fake, _ := connect(t)
		res := call(t, session, "set_status_bar", map[string]any{"device": "iPhone 16 Pro", "mode": mode})
		if res.IsError || !fake.Called("simctl status_bar 00000000-0000-4000-8000-0000000000A2 "+want) {
			t.Errorf("%s: text %q, calls %v", mode, textOf(res), fake.Calls())
		}
	}
	session, _, _ := connect(t)
	if res := call(t, session, "set_status_bar", map[string]any{"device": "iPhone 16 Pro", "mode": "tidy"}); !res.IsError {
		t.Errorf("tidy was accepted: %q", textOf(res))
	}
}

func TestSendPush(t *testing.T) {
	session, fake, _ := connect(t)
	res := call(t, session, "send_push", map[string]any{"device": "iPhone 16 Pro", "app": "com.example.app", "body": "Hi"})
	if res.IsError || !fake.Called("simctl push 00000000-0000-4000-8000-0000000000A2 com.example.app -") {
		t.Errorf("text %q, calls %v", textOf(res), fake.Calls())
	}
	for name, args := range map[string]map[string]any{
		"both":            {"device": "iPhone 16 Pro", "app": "com.example.app", "body": "Hi", "payload": `{"aps": {}}`},
		"no aps":          {"device": "iPhone 16 Pro", "app": "com.example.app", "payload": `{"badge": 1}`},
		"a shut-down sim": {"device": "iPhone 16", "app": "com.example.app"},
	} {
		session, fake, _ := connect(t)
		if res := call(t, session, "send_push", args); !res.IsError || fake.Called("simctl push") {
			t.Errorf("%s was accepted: %q", name, textOf(res))
		}
	}
}

func TestServerTellsTheModelHowToStart(t *testing.T) {
	session, _, _ := connect(t)
	if got := session.InitializeResult().Instructions; !strings.Contains(got, "list_devices") {
		t.Errorf("instructions %q", got)
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

func TestAddMediaAndSetClipboard(t *testing.T) {
	photo := filepath.Join(t.TempDir(), "a.png")
	if err := os.WriteFile(photo, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	session, fake, _ := connect(t)
	if res := call(t, session, "add_media", map[string]any{"device": "iPhone 16 Pro", "files": []string{photo}}); res.IsError || !fake.Called("simctl addmedia 00000000-0000-4000-8000-0000000000A2 "+photo) {
		t.Errorf("add_media: %q, calls %v", textOf(res), fake.Calls())
	}
	if res := call(t, session, "add_media", map[string]any{"device": "iPhone 16 Pro", "files": []string{photo + ".pdf"}}); !res.IsError {
		t.Errorf("a missing file was accepted: %q", textOf(res))
	}
	if res := call(t, session, "set_clipboard", map[string]any{"device": "iPhone 16 Pro", "text": "hi"}); res.IsError || !fake.Called("simctl pbcopy 00000000-0000-4000-8000-0000000000A2") {
		t.Errorf("set_clipboard: %q, calls %v", textOf(res), fake.Calls())
	}
}

func TestTheScreenToolsRefuseASimulatorAndBadArguments(t *testing.T) {
	session, fake, _ := connect(t)
	ten := 10
	for name, c := range map[string]struct {
		tool string
		args map[string]any
		want string
	}{
		"ui_tree on a simulator": {"ui_tree", map[string]any{"device": "iPhone 16 Pro"}, "simctl"},
		"tap on a simulator":     {"tap", map[string]any{"device": "iPhone 16 Pro", "x": ten, "y": ten}, "simctl"},
		"tap with nothing":       {"tap", map[string]any{"device": "iPhone 16 Pro"}, "target, or x and y"},
		"tap with both":          {"tap", map[string]any{"device": "iPhone 16 Pro", "target": "OK", "x": ten, "y": ten}, "not both"},
		"type on a simulator":    {"type_text", map[string]any{"device": "iPhone 16 Pro", "text": "hi"}, "simctl"},
		"an unknown key":         {"press_key", map[string]any{"device": "iPhone 16 Pro", "key": "power"}, "simctl"},
		"a long recording":       {"record_screen", map[string]any{"device": "iPhone 16 Pro", "seconds": 600}, "1 to 60"},
	} {
		res := call(t, session, c.tool, c.args)
		if !res.IsError || !strings.Contains(textOf(res), c.want) {
			t.Errorf("%s: error %v, %q", name, res.IsError, textOf(res))
		}
	}
	if fake.Called("input") {
		t.Errorf("calls %v", fake.Calls())
	}
}

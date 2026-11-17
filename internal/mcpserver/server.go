// Package mcpserver serves lazymobile's device commands as Model Context Protocol tools over stdio, so
// an agent can boot, install, launch, screenshot and read logs. Nothing here erases data.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/doctor"
	"github.com/0xrnp/lazymobile/internal/logs"
	"github.com/0xrnp/lazymobile/internal/mobile"
	"github.com/0xrnp/lazymobile/internal/state"
)

const (
	defaultLogLines = 200
	maxLogLines     = 2000
)

// Server is what the tools need.
type Server struct {
	Manager *mobile.Manager
	Doctor  func(ctx context.Context) doctor.Report
	State   state.Store
	Version string
}

// Run serves the tools on stdin and stdout until the client disconnects.
func (s *Server) Run(ctx context.Context) error {
	return s.New().Run(ctx, &mcp.StdioTransport{})
}

// New returns the MCP server with every tool added.
func (s *Server) New() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "lazymobile", Version: s.Version}, nil)
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true}

	mcp.AddTool(server, &mcp.Tool{Name: "list_devices", Annotations: readOnly,
		Description: "List every iOS simulator, Android emulator and connected phone, with its state and id."}, s.listDevices)
	mcp.AddTool(server, &mcp.Tool{Name: "list_apps", Annotations: readOnly,
		Description: "List the app ids used before and the apps installed on a running device."}, s.listApps)
	mcp.AddTool(server, &mcp.Tool{Name: "doctor", Annotations: readOnly,
		Description: "Report which tools (Xcode, Android SDK, adb) are installed, what is missing, and how to fix it."}, s.doctor)

	mcp.AddTool(server, &mcp.Tool{Name: "boot",
		Description: "Boot a simulator or emulator and wait until it is ready."}, s.boot)
	mcp.AddTool(server, &mcp.Tool{Name: "shutdown",
		Description: "Shut down a simulator or emulator."}, s.shutdown)

	mcp.AddTool(server, &mcp.Tool{Name: "install",
		Description: "Install an .apk, or an .app folder for a simulator or an iPhone, on a running device."}, s.install)
	mcp.AddTool(server, &mcp.Tool{Name: "launch",
		Description: "Open an app by its bundle id or package name."}, s.launch)
	mcp.AddTool(server, &mcp.Tool{Name: "stop",
		Description: "Stop a running app. Its data is kept."}, s.stop)
	mcp.AddTool(server, &mcp.Tool{Name: "open_link",
		Description: "Open a web address or a deep link, such as myapp://home, on a running device."}, s.openLink)

	mcp.AddTool(server, &mcp.Tool{Name: "screenshot", Annotations: readOnly,
		Description: "Take a PNG of a running device's screen and return it as an image."}, s.screenshot)
	mcp.AddTool(server, &mcp.Tool{Name: "read_logs", Annotations: readOnly,
		Description: "Read what a running simulator, emulator or Android phone has already logged, and return the last lines. " +
			"Does not follow the log. A physical iPhone has no log history to read."}, s.readLogs)

	mcp.AddTool(server, &mcp.Tool{Name: "set_appearance",
		Description: "Switch a device to dark or light mode."}, s.setAppearance)
	mcp.AddTool(server, &mcp.Tool{Name: "set_location",
		Description: "Put a simulator or emulator at a latitude and longitude, or clear its location."}, s.setLocation)
	mcp.AddTool(server, &mcp.Tool{Name: "set_permission",
		Description: "Grant or revoke a permission, such as camera, location or photos, for an app."}, s.setPermission)
	return server
}

type deviceArgs struct {
	Device string `json:"device" jsonschema:"an id, an adb serial, a name, or a unique part of a name"`
}

type appArgs struct {
	Device string `json:"device" jsonschema:"an id, an adb serial, a name, or a unique part of a name"`
	App    string `json:"app" jsonschema:"the iOS bundle id or Android package name, such as com.example.app"`
}

type noArgs struct{}

// text is a tool result of one sentence.
func text(sentence string) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: sentence}}}, nil, nil
}

// jsonText is a tool result of v as indented JSON.
func jsonText(v any) (*mcp.CallToolResult, any, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return text(string(data))
}

func (s *Server) listDevices(ctx context.Context, _ *mcp.CallToolRequest, _ noArgs) (*mcp.CallToolResult, any, error) {
	devices, errs := s.Manager.List(ctx)
	problems := make([]string, 0, len(errs))
	for _, err := range errs {
		problems = append(problems, err.Error())
	}
	if devices == nil {
		devices = []device.Device{}
	}
	return jsonText(map[string]any{"devices": devices, "errors": problems})
}

func (s *Server) listApps(ctx context.Context, _ *mcp.CallToolRequest, in deviceArgs) (*mcp.CallToolResult, any, error) {
	d, err := s.Manager.Find(ctx, in.Device)
	if err != nil {
		return nil, nil, err
	}
	if !d.Ready() {
		return nil, nil, apperr.New(apperr.InvalidState, "%s is not running", d.Name).WithHint("Boot or connect it first.")
	}
	installed, err := s.Manager.InstalledApps(ctx, d)
	if err != nil {
		return nil, nil, err
	}
	choices := state.Choices(s.State.Load().Apps[d.Platform], installed)
	if choices == nil {
		choices = []state.AppChoice{}
	}
	return jsonText(map[string]any{"device": d.Name, "apps": choices})
}

func (s *Server) doctor(ctx context.Context, _ *mcp.CallToolRequest, _ noArgs) (*mcp.CallToolResult, any, error) {
	return jsonText(s.Doctor(ctx))
}

func (s *Server) boot(ctx context.Context, _ *mcp.CallToolRequest, in deviceArgs) (*mcp.CallToolResult, any, error) {
	d, err := s.Manager.Find(ctx, in.Device)
	if err != nil {
		return nil, nil, err
	}
	if err := s.Manager.Boot(ctx, d, mobile.BootOptions{Wait: true}); err != nil {
		return nil, nil, err
	}
	return text(d.Name + " is ready")
}

func (s *Server) shutdown(ctx context.Context, _ *mcp.CallToolRequest, in deviceArgs) (*mcp.CallToolResult, any, error) {
	d, err := s.Manager.Find(ctx, in.Device)
	if err != nil {
		return nil, nil, err
	}
	if err := s.Manager.Shutdown(ctx, d); err != nil {
		return nil, nil, err
	}
	return text("Shut down " + d.Name)
}

type installArgs struct {
	Device string `json:"device" jsonschema:"an id, an adb serial, a name, or a unique part of a name"`
	Path   string `json:"path" jsonschema:"the .apk file, or the .app folder, on this computer"`
}

func (s *Server) install(ctx context.Context, _ *mcp.CallToolRequest, in installArgs) (*mcp.CallToolResult, any, error) {
	d, err := s.Manager.Find(ctx, in.Device)
	if err != nil {
		return nil, nil, err
	}
	if err := s.Manager.Install(ctx, d, in.Path); err != nil {
		return nil, nil, err
	}
	return text("Installed on " + d.Name)
}

type launchArgs struct {
	Device  string `json:"device" jsonschema:"an id, an adb serial, a name, or a unique part of a name"`
	App     string `json:"app" jsonschema:"the iOS bundle id or Android package name, such as com.example.app"`
	Restart bool   `json:"restart,omitempty" jsonschema:"stop a running copy of the app first"`
}

func (s *Server) launch(ctx context.Context, _ *mcp.CallToolRequest, in launchArgs) (*mcp.CallToolResult, any, error) {
	d, err := s.Manager.Find(ctx, in.Device)
	if err != nil {
		return nil, nil, err
	}
	if err := s.Manager.Launch(ctx, d, in.App, mobile.LaunchOptions{Restart: in.Restart}); err != nil {
		return nil, nil, err
	}
	_ = s.State.Update(func(f *state.File) { f.RememberApp(d.Platform, in.App) })
	return text(fmt.Sprintf("Launched %s on %s", in.App, d.Name))
}

func (s *Server) stop(ctx context.Context, _ *mcp.CallToolRequest, in appArgs) (*mcp.CallToolResult, any, error) {
	d, err := s.Manager.Find(ctx, in.Device)
	if err != nil {
		return nil, nil, err
	}
	if err := s.Manager.Stop(ctx, d, in.App); err != nil {
		return nil, nil, err
	}
	return text(fmt.Sprintf("Stopped %s on %s", in.App, d.Name))
}

type linkArgs struct {
	Device string `json:"device" jsonschema:"an id, an adb serial, a name, or a unique part of a name"`
	URL    string `json:"url" jsonschema:"a web address or a deep link, such as myapp://home"`
}

func (s *Server) openLink(ctx context.Context, _ *mcp.CallToolRequest, in linkArgs) (*mcp.CallToolResult, any, error) {
	d, err := s.Manager.Find(ctx, in.Device)
	if err != nil {
		return nil, nil, err
	}
	if err := s.Manager.OpenLink(ctx, d, in.URL); err != nil {
		return nil, nil, err
	}
	return text(fmt.Sprintf("Opened %s on %s", in.URL, d.Name))
}

func (s *Server) screenshot(ctx context.Context, _ *mcp.CallToolRequest, in deviceArgs) (*mcp.CallToolResult, any, error) {
	d, err := s.Manager.Find(ctx, in.Device)
	if err != nil {
		return nil, nil, err
	}
	dir, err := os.MkdirTemp("", "lazymobile-mcp-")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(dir)
	path, err := s.Manager.Screenshot(ctx, d, filepath.Join(dir, "screen.png"))
	if err != nil {
		return nil, nil, err
	}
	png, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.ImageContent{Data: png, MIMEType: "image/png"}}}, nil, nil
}

type logArgs struct {
	Device      string `json:"device" jsonschema:"an id, an adb serial, a name, or a unique part of a name"`
	App         string `json:"app,omitempty" jsonschema:"only this app's lines: an iOS bundle id or Android package name"`
	Level       string `json:"level,omitempty" jsonschema:"the lowest level to keep: verbose, debug, info, warning, error or fatal"`
	Text        string `json:"text,omitempty" jsonschema:"only lines matching this search, as typed in the dashboard's log filter"`
	Since       string `json:"since,omitempty" jsonschema:"how far back to read, such as 30s or 5m. Default: 5m on iOS, the whole buffer on Android"`
	MaxLines    int    `json:"max_lines,omitempty" jsonschema:"how many of the last matching lines to return. Default 200, at most 2000"`
	CrashesOnly bool   `json:"crashes_only,omitempty" jsonschema:"only the lines of a crash"`
}

func (s *Server) readLogs(ctx context.Context, _ *mcp.CallToolRequest, in logArgs) (*mcp.CallToolResult, any, error) {
	limit := in.MaxLines
	switch {
	case limit < 0:
		return nil, nil, apperr.New(apperr.Usage, "max_lines cannot be negative")
	case limit == 0:
		limit = defaultLogLines
	case limit > maxLogLines:
		limit = maxLogLines
	}
	var since time.Duration
	if in.Since != "" {
		var err error
		if since, err = time.ParseDuration(in.Since); err != nil || since < 0 {
			return nil, nil, apperr.New(apperr.Usage, "since must be a duration such as 30s or 5m, not %q", in.Since)
		}
	}
	minimum := logs.NoLevel
	if in.Level != "" {
		var known bool
		if minimum, known = logs.ParseLevel(in.Level); !known {
			return nil, nil, apperr.New(apperr.Usage, "unknown level %q (verbose, debug, info, warning, error, fatal)", in.Level)
		}
	}
	query, err := logs.ParseQuery(in.Text)
	if err != nil {
		return nil, nil, apperr.New(apperr.Usage, "%s", err.Error())
	}
	d, err := s.Manager.Find(ctx, in.Device)
	if err != nil {
		return nil, nil, err
	}
	mode := logs.System
	if d.Platform == device.Android {
		mode = logs.Logcat
	}

	var last []string
	matched := 0
	session := logs.Start(ctx, s.Manager.LogDeps(), logs.Spec{Device: d, Mode: mode, App: in.App, Dump: true, Since: since}, logs.Handlers{
		Lines: func(lines []logs.Line) {
			for _, line := range lines {
				// A line without a level belongs to the one before it, so it is kept.
				if line.Level != logs.NoLevel && line.Level < minimum {
					continue
				}
				if in.CrashesOnly && !line.Crash {
					continue
				}
				if !query.Match(line.Text) {
					continue
				}
				matched++
				last = append(last, line.Text)
				if len(last) > limit {
					last = last[1:]
				}
			}
		},
		Status: func(string) {},
	})
	if err := session.Wait(); err != nil {
		return nil, nil, err
	}
	if matched == 0 {
		return text("No matching lines.")
	}
	out := strings.Join(last, "\n")
	if matched > len(last) {
		out = fmt.Sprintf("(the last %d of %d matching lines)\n%s", len(last), matched, out)
	}
	return text(out)
}

type appearanceArgs struct {
	Device string `json:"device" jsonschema:"an id, an adb serial, a name, or a unique part of a name"`
	Mode   string `json:"mode" jsonschema:"dark or light"`
}

func (s *Server) setAppearance(ctx context.Context, _ *mcp.CallToolRequest, in appearanceArgs) (*mcp.CallToolResult, any, error) {
	d, err := s.Manager.Find(ctx, in.Device)
	if err != nil {
		return nil, nil, err
	}
	if err := s.Manager.SetAppearance(ctx, d, in.Mode); err != nil {
		return nil, nil, err
	}
	return text(fmt.Sprintf("%s is now in %s mode", d.Name, in.Mode))
}

type locationArgs struct {
	Device   string `json:"device" jsonschema:"an id, an adb serial, a name, or a unique part of a name"`
	Location string `json:"location" jsonschema:"latitude,longitude such as 37.5665,126.978, or clear"`
}

func (s *Server) setLocation(ctx context.Context, _ *mcp.CallToolRequest, in locationArgs) (*mcp.CallToolResult, any, error) {
	d, err := s.Manager.Find(ctx, in.Device)
	if err != nil {
		return nil, nil, err
	}
	if in.Location == "clear" {
		if err := s.Manager.ClearLocation(ctx, d); err != nil {
			return nil, nil, err
		}
		return text("Cleared the location of " + d.Name)
	}
	lat, lon, err := mobile.ParseLocation(in.Location)
	if err != nil {
		return nil, nil, err
	}
	if err := s.Manager.SetLocation(ctx, d, lat, lon); err != nil {
		return nil, nil, err
	}
	return text(fmt.Sprintf("%s is now at %g,%g", d.Name, lat, lon))
}

type permissionArgs struct {
	Device     string `json:"device" jsonschema:"an id, an adb serial, a name, or a unique part of a name"`
	App        string `json:"app" jsonschema:"the iOS bundle id or Android package name, such as com.example.app"`
	Permission string `json:"permission" jsonschema:"such as camera, location, photos, microphone, contacts or notifications"`
	Grant      bool   `json:"grant" jsonschema:"true to grant, false to revoke"`
}

func (s *Server) setPermission(ctx context.Context, _ *mcp.CallToolRequest, in permissionArgs) (*mcp.CallToolResult, any, error) {
	d, err := s.Manager.Find(ctx, in.Device)
	if err != nil {
		return nil, nil, err
	}
	if err := s.Manager.SetPermission(ctx, d, in.App, in.Permission, in.Grant); err != nil {
		return nil, nil, err
	}
	if in.Grant {
		return text(fmt.Sprintf("Granted %s to %s on %s", in.Permission, in.App, d.Name))
	}
	return text(fmt.Sprintf("Revoked %s from %s on %s", in.Permission, in.App, d.Name))
}

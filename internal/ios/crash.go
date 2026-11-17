package ios

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
)

const (
	// crashBugType is the bug_type of an app crash in an .ips report. Hangs and other reports
	// have other types.
	crashBugType = "309"
	// maxCrashFiles is how many of the newest reports are looked at, so a folder of years of
	// reports is not read whole.
	maxCrashFiles = 300
)

// LatestCrash returns the newest crash report of an app on a simulator. A simulator's crash reports
// are written on the Mac, with the simulator's id in them, so the simulator need not be running.
// An iPhone keeps its reports on the phone, which this does not read.
func (c *Client) LatestCrash(ctx context.Context, d device.Device, bundleID string) (device.CrashReport, error) {
	if d.Kind == device.Physical {
		return device.CrashReport{}, apperr.New(apperr.Unsupported, "The crash reports of %s stay on the phone, and cannot be read yet", d.Name).
			WithHint("Xcode's Devices and Simulators window shows them.")
	}
	dir := c.CrashDir
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return device.CrashReport{}, apperr.New(apperr.CommandFailed, "Could not find the home folder: %v", err)
		}
		dir = filepath.Join(home, "Library", "Logs", "DiagnosticReports")
	}
	for _, path := range newestReports(dir, bundleID) {
		if ctx.Err() != nil {
			return device.CrashReport{}, ctx.Err()
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		report, ok := ParseCrashReport(data, bundleID, d.ID)
		if ok {
			report.Path = path
			return report, nil
		}
	}
	return device.CrashReport{}, apperr.New(apperr.NotFound, "No crash of %s found for %s", bundleID, d.Name).
		WithHint("A simulator's crash reports are in %s.", dir)
}

// newestReports lists the .ips crash reports of bundleID in dir, newest first, up to
// maxCrashFiles. Only each file's first line is read here, so other apps' reports are never read
// whole.
func newestReports(dir, bundleID string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	type file struct {
		path string
		mod  int64
	}
	var files []file
	for _, e := range entries {
		// Only plain files: a link or a pipe named .ips is not followed or waited on.
		if !e.Type().IsRegular() || filepath.Ext(e.Name()) != ".ips" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, file{filepath.Join(dir, e.Name()), info.ModTime().UnixNano()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod > files[j].mod })
	var paths []string
	for _, f := range files {
		if len(paths) == maxCrashFiles {
			break
		}
		if isCrashOf(f.path, bundleID) {
			paths = append(paths, f.path)
		}
	}
	return paths
}

// isCrashOf reads the one-line header of an .ips file and reports whether it is a crash of
// bundleID.
func isCrashOf(path, bundleID string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	line, err := bufio.NewReaderSize(file, 64*1024).ReadSlice('\n')
	if err != nil {
		return false
	}
	var header ipsHeader
	return json.Unmarshal(line, &header) == nil && header.BugType == crashBugType && header.BundleID == bundleID
}

// ipsHeader is the first line of an .ips crash report.
type ipsHeader struct {
	BugType  string `json:"bug_type"`
	BundleID string `json:"bundleID"`
	AppName  string `json:"app_name"`
	Version  string `json:"app_version"`
	Build    string `json:"build_version"`
}

// ipsBody is the part of an .ips crash report's body that is shown.
type ipsBody struct {
	CaptureTime   string `json:"captureTime"`
	CoalitionName string `json:"coalitionName"`
	Exception     struct {
		Type    string `json:"type"`
		Signal  string `json:"signal"`
		Subtype string `json:"subtype"`
	} `json:"exception"`
	Termination struct {
		Indicator string `json:"indicator"`
	} `json:"termination"`
	// ASI holds messages from the system libraries, such as why abort() was called.
	ASI                    map[string][]string `json:"asi"`
	LastExceptionBacktrace []ipsFrame          `json:"lastExceptionBacktrace"`
	FaultingThread         int                 `json:"faultingThread"`
	Threads                []struct {
		Frames []ipsFrame `json:"frames"`
	} `json:"threads"`
	UsedImages []struct {
		Name string `json:"name"`
	} `json:"usedImages"`
}

type ipsFrame struct {
	// ImageIndex is nil when the report does not say which library the frame is in.
	ImageIndex     *int   `json:"imageIndex"`
	ImageOffset    uint64 `json:"imageOffset"`
	Symbol         string `json:"symbol"`
	SymbolLocation uint64 `json:"symbolLocation"`
}

// simulatorCoalition is how a simulator's process names the simulator in its crash report.
const simulatorCoalition = "com.apple.CoreSimulator.SimDevice."

// ParseCrashReport reads an .ips crash report, and returns it as text when it is a crash of
// bundleID on the simulator with id udid.
func ParseCrashReport(data []byte, bundleID, udid string) (device.CrashReport, bool) {
	first, rest, found := bytes.Cut(data, []byte("\n"))
	if !found {
		return device.CrashReport{}, false
	}
	var header ipsHeader
	if json.Unmarshal(first, &header) != nil || header.BugType != crashBugType || header.BundleID != bundleID {
		return device.CrashReport{}, false
	}
	var body ipsBody
	if json.Unmarshal(rest, &body) != nil || body.CoalitionName != simulatorCoalition+udid {
		return device.CrashReport{}, false
	}
	return device.CrashReport{App: bundleID, When: body.CaptureTime, Text: crashText(header, body)}, true
}

// crashText is a report as a person reads it: the app, what stopped it and why, then the code
// that was running.
func crashText(header ipsHeader, body ipsBody) string {
	var b strings.Builder
	name := header.AppName
	if v := (device.App{Version: header.Version, Build: header.Build}).VersionLabel(); v != "" {
		name += " " + v
	}
	fmt.Fprintf(&b, "%s, %s\n", name, header.BundleID)
	fmt.Fprintf(&b, "Crashed %s\n", body.CaptureTime)
	exception := body.Exception.Type
	if body.Exception.Signal != "" {
		exception += " (" + body.Exception.Signal + ")"
	}
	if exception != "" {
		b.WriteString(exception + "\n")
	}
	for _, line := range []string{body.Exception.Subtype, body.Termination.Indicator} {
		if line != "" {
			b.WriteString(line + "\n")
		}
	}
	libraries := make([]string, 0, len(body.ASI))
	for library := range body.ASI {
		libraries = append(libraries, library)
	}
	sort.Strings(libraries)
	for _, library := range libraries {
		for _, message := range body.ASI[library] {
			fmt.Fprintf(&b, "%s: %s\n", library, message)
		}
	}
	image := func(i *int) string {
		if i != nil && *i >= 0 && *i < len(body.UsedImages) && body.UsedImages[*i].Name != "" {
			return body.UsedImages[*i].Name
		}
		return "???"
	}
	frames := func(title string, list []ipsFrame) {
		if len(list) == 0 {
			return
		}
		fmt.Fprintf(&b, "\n%s\n", title)
		width := 0
		for _, f := range list {
			width = max(width, len(image(f.ImageIndex)))
		}
		for i, f := range list {
			at := fmt.Sprintf("0x%x", f.ImageOffset)
			if f.Symbol != "" {
				at = fmt.Sprintf("%s + %d", f.Symbol, f.SymbolLocation)
			}
			fmt.Fprintf(&b, "%-3d %-*s  %s\n", i, width, image(f.ImageIndex), at)
		}
	}
	frames("Last exception backtrace:", body.LastExceptionBacktrace)
	if t := body.FaultingThread; t >= 0 && t < len(body.Threads) {
		frames(fmt.Sprintf("Thread %d crashed:", t), body.Threads[t].Frames)
	}
	return strings.TrimRight(b.String(), "\n")
}

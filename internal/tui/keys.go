package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/0xrnp/lazymobile/internal/device"
)

// keyContext is where a key works: in one pane, or everywhere.
type keyContext int

const (
	ctxDevices keyContext = iota
	ctxLogs
	ctxBuilds
	ctxEverywhere
	// ctxRun is the run view of lazymobile run --here. While the tool has the keys, only the keys
	// with a modifier here are not the tool's.
	ctxRun
)

func (c keyContext) String() string {
	return [...]string{"Devices", "Logs", "Builds", "Everywhere", "Run"}[c]
}

// keyAction is one thing a key does. The table below is the one place that says what the keys are:
// the keys menu and the hints under the dashboard read it, and a test checks it against the
// handlers, so a key cannot be added or changed without it.
type keyAction struct {
	id  string
	ctx keyContext
	// keys are what the handler for ctx sees, every alias included. The first is what the keys menu
	// sends to run the action.
	keys []string
	// show is how the keys are written in the menu.
	show string
	// hintKey is how the keys are written in the hints, when keys[0] is not it.
	hintKey string
	desc    string
	// footer is the short label for the hints under the dashboard, or "" to leave the action out.
	// It is a function because some labels depend on what is open.
	footer func(m *Model) string
	// when says why the action cannot be done now, or "" when it can.
	when func(m *Model) string
}

func say(text string) func(*Model) string { return func(*Model) string { return text } }

// Why an action cannot be done now.
func selectedDevice(check func(d device.Device) string) func(*Model) string {
	return func(m *Model) string {
		d, ok := m.selected()
		if !ok {
			return "No device is selected"
		}
		return check(d)
	}
}

func virtualOnly(d device.Device) string {
	if d.Kind == device.Physical {
		return d.Name + " is a real device: it is not booted, shut down, erased or deleted from here"
	}
	return ""
}

func running(d device.Device) string {
	if !d.Ready() {
		return d.Name + " is not running"
	}
	return ""
}

// screenFeature is why d cannot show a screenshot or open a link, or "" when it can.
func screenFeature(d device.Device) string {
	switch {
	case !d.Ready():
		return d.Name + " is not running"
	case d.Platform == device.IOS && d.Kind == device.Physical:
		return d.Name + " is an iPhone: Apple's tools cannot do this from here"
	}
	return ""
}

// virtualOnlyRunning is why d cannot be moved or have its status bar tidied, or "" when it can:
// only a running simulator or emulator can.
func virtualOnlyRunning(d device.Device) string {
	switch {
	case !d.Ready():
		return d.Name + " is not running"
	case d.Kind == device.Physical:
		return d.Name + " is a real device: its location and status bar are left alone"
	}
	return ""
}

func needsLog(m *Model) string {
	if m.logCount() == 0 {
		return "No log is open: press l on a running device"
	}
	return ""
}

func needsBuilds(m *Model) string {
	if m.builds == nil {
		return "Builds are for Expo projects: start lazymobile in one"
	}
	return ""
}

// keyTable lists every key, in the order the menu shows them within a context.
var keyTable = []keyAction{
	// Devices.
	{id: "move", ctx: ctxDevices, keys: []string{"down", "j", "up", "k"}, show: "j k", desc: "Move"},
	{id: "edges", ctx: ctxDevices, keys: []string{"g", "home", "G", "end"}, show: "g G", desc: "First or last device"},
	{id: "boot", ctx: ctxDevices, keys: []string{"b"}, show: "b", desc: "Boot a simulator or emulator",
		footer: say("boot"), when: selectedDevice(virtualOnly)},
	{id: "coldboot", ctx: ctxDevices, keys: []string{"B"}, show: "B", desc: "Cold boot an emulator, ignoring its snapshot",
		when: selectedDevice(func(d device.Device) string {
			if d.Kind != device.Emulator {
				return "Cold boot is for Android emulators"
			}
			return ""
		})},
	{id: "shutdown", ctx: ctxDevices, keys: []string{"s"}, show: "s", desc: "Shut down",
		footer: say("shut down"), when: selectedDevice(virtualOnly)},
	{id: "erase", ctx: ctxDevices, keys: []string{"e"}, show: "e", desc: "Erase a simulator, or wipe an emulator (asks first)",
		footer: say("erase"), when: selectedDevice(virtualOnly)},
	{id: "delete", ctx: ctxDevices, keys: []string{"d"}, show: "d", desc: "Delete a simulator or emulator (asks first)",
		footer: say("delete"), when: selectedDevice(virtualOnly)},
	{id: "unavailable", ctx: ctxDevices, keys: []string{"U"}, show: "U", desc: "Delete the simulators Xcode can no longer run (asks first)",
		footer: func(m *Model) string {
			if len(m.unavailable) == 0 {
				return ""
			}
			return fmt.Sprintf("remove %d unavailable", len(m.unavailable))
		}, when: func(m *Model) string {
			if len(m.unavailable) == 0 {
				return "There are no unavailable simulators"
			}
			return ""
		}},
	{id: "uninstall", ctx: ctxDevices, keys: []string{"u"}, show: "u", desc: "Uninstall an app (asks first)",
		when: selectedDevice(running)},
	{id: "app", ctx: ctxDevices, keys: []string{"o"}, show: "o", desc: "Launch, restart or stop an app",
		when: selectedDevice(running)},
	{id: "appearance", ctx: ctxDevices, keys: []string{"m"}, show: "m", desc: "Switch between dark and light mode",
		when: selectedDevice(screenFeature)},
	{id: "location", ctx: ctxDevices, keys: []string{"f"}, show: "f", desc: "Put a simulator or emulator at a place",
		when: selectedDevice(virtualOnlyRunning)},
	{id: "statusbar", ctx: ctxDevices, keys: []string{"c", "C"}, show: "c C", desc: "Tidy the status bar for a screenshot, or restore it",
		when: selectedDevice(virtualOnlyRunning)},
	{id: "screenshot", ctx: ctxDevices, keys: []string{"p"}, show: "p", desc: "Save a screenshot in this folder",
		when: selectedDevice(screenFeature)},
	{id: "install", ctx: ctxDevices, keys: []string{"i"}, show: "i", desc: "Install an .apk or .app on a running device",
		when: selectedDevice(screenFeature)},
	{id: "project_action", ctx: ctxDevices, keys: []string{"R"}, show: "R", desc: "Run a project command from .lazymobile.json (asks first)",
		when: selectedDevice(running)},
	{id: "link", ctx: ctxDevices, keys: []string{"t"}, show: "t", desc: "Open a web address or deep link",
		when: selectedDevice(screenFeature)},
	{id: "dev", ctx: ctxDevices, keys: []string{"D"}, show: "D", desc: "Reach Metro, open the dev menu or reload (Android)",
		when: selectedDevice(devFeature)},
	{id: "logs", ctx: ctxDevices, keys: []string{"l", "enter"}, show: "l enter", desc: "Open a log in a new pane",
		footer: func(m *Model) string {
			if m.logCount() > 0 {
				return "another log"
			}
			return "logs"
		}, when: selectedDevice(running)},
	{id: "new", ctx: ctxDevices, keys: []string{"n"}, show: "n", desc: "New simulator or emulator", footer: say("new")},
	{id: "wifi", ctx: ctxDevices, keys: []string{"w"}, show: "w", desc: "Pair or connect an Android phone over Wi-Fi", footer: say("wi-fi")},
	{id: "disconnect", ctx: ctxDevices, keys: []string{"x"}, show: "x", desc: "Disconnect a Wi-Fi phone",
		when: selectedDevice(func(d device.Device) string {
			if d.Platform != device.Android || d.Transport != "wifi" {
				return "Only phones connected over Wi-Fi can be disconnected"
			}
			return ""
		})},
	{id: "copyid", ctx: ctxDevices, keys: []string{"y"}, show: "y", desc: "Copy the device id",
		footer: say("copy id"), when: selectedDevice(func(device.Device) string { return "" })},
	{id: "refresh", ctx: ctxDevices, keys: []string{"r"}, show: "r", desc: "Refresh the list", footer: say("refresh")},

	// Logs.
	{id: "log_scroll", ctx: ctxLogs, keys: []string{"down", "j", "up", "k"}, show: "j k", desc: "Scroll", when: needsLog},
	{id: "log_page", ctx: ctxLogs, keys: []string{"pgdown", "ctrl+d", "space", "pgup", "ctrl+u"}, show: "pgup pgdn", desc: "Half a page up or down", when: needsLog},
	{id: "log_first", ctx: ctxLogs, keys: []string{"g", "home"}, show: "g", desc: "First line", when: needsLog},
	{id: "log_follow", ctx: ctxLogs, keys: []string{"G", "end"}, show: "G", desc: "Jump to the newest line and follow", when: needsLog},
	{id: "log_error", ctx: ctxLogs, keys: []string{"e", "E"}, show: "e E", desc: "Jump to the next or previous error or crash", when: needsLog},
	{id: "log_slide", ctx: ctxLogs, keys: []string{"right", "l", "left", "h"}, show: "h l", desc: "Scroll sideways", when: needsLog},
	{id: "log_reset", ctx: ctxLogs, keys: []string{"0"}, show: "0", desc: "Back to the start of the lines", when: needsLog},
	{id: "log_line", ctx: ctxLogs, keys: []string{"enter"}, show: "enter", desc: "Show the selected line in full",
		footer: say("line"), when: needsLog},
	{id: "log_copy", ctx: ctxLogs, keys: []string{"y"}, show: "y", desc: "Copy the selected line", footer: say("copy"), when: needsLog},
	{id: "log_wrap", ctx: ctxLogs, keys: []string{"w"}, show: "w", desc: "Wrap long lines", footer: say("wrap"), when: needsLog},
	{id: "log_filter", ctx: ctxLogs, keys: []string{"/", "f"}, show: "/", desc: "Filter: words, -leave out, /regex/", footer: say("filter"), when: needsLog},
	{id: "log_presets", ctx: ctxLogs, keys: []string{"P"}, show: "P", desc: "Filters saved for this app: apply one, or save this one", when: needsLog},
	{id: "log_level", ctx: ctxLogs, keys: []string{"L"}, show: "L", desc: "Minimum level", footer: say("level"), when: needsLog},
	{id: "log_pause", ctx: ctxLogs, keys: []string{"p"}, show: "p", desc: "Pause or resume, losing nothing", footer: say("pause"), when: needsLog},
	{id: "log_clear", ctx: ctxLogs, keys: []string{"c"}, show: "c", desc: "Clear", footer: say("clear"), when: needsLog},
	{id: "log_save", ctx: ctxLogs, keys: []string{"S"}, show: "S", desc: "Save what is shown to a file", footer: say("save"), when: needsLog},
	{id: "log_stop", ctx: ctxLogs, keys: []string{"x"}, show: "x", desc: "Stop the stream", footer: say("stop"), when: needsLog},
	{id: "log_restart", ctx: ctxLogs, keys: []string{"r"}, show: "r", desc: "Restart the stream, or launch a waiting one", footer: say("restart"), when: needsLog},
	{id: "log_usage", ctx: ctxLogs, keys: []string{"M"}, show: "M", desc: "Show the app's CPU and memory in the heading, or hide them", when: needsLog},
	{id: "log_jank", ctx: ctxLogs, keys: []string{"J"}, show: "J", desc: "Restart the jank count (Android, after M)", when: needsLog},
	{id: "log_close", ctx: ctxLogs, keys: []string{"X"}, show: "X", desc: "Close this log", footer: say("close"), when: needsLog},

	// Builds.
	{id: "build_move", ctx: ctxBuilds, keys: []string{"down", "j", "up", "k"}, show: "j k", desc: "Move", when: needsBuilds},
	{id: "build_edges", ctx: ctxBuilds, keys: []string{"g", "home", "G", "end"}, show: "g G", desc: "First or last build", when: needsBuilds},
	{id: "build_load", ctx: ctxBuilds, keys: []string{"enter", "l"}, show: "enter", desc: "Load the builds, then show a build's details",
		footer: func(m *Model) string {
			if !m.build.asked {
				return "load builds"
			}
			return "details"
		}, when: needsBuilds},
	{id: "build_install", ctx: ctxBuilds, keys: []string{"i"}, show: "i", desc: "Install a finished build on a running device",
		footer: say("install"), when: needsBuilds},
	{id: "build_open", ctx: ctxBuilds, keys: []string{"o"}, show: "o", desc: "Open the build's page on expo.dev", footer: say("open page"), when: needsBuilds},
	{id: "build_new", ctx: ctxBuilds, keys: []string{"n"}, show: "n", desc: "Start a build (asks first)", footer: say("new build"), when: needsBuilds},
	{id: "build_cancel", ctx: ctxBuilds, keys: []string{"x"}, show: "x", desc: "Cancel a build (asks first)", footer: say("cancel"), when: needsBuilds},
	{id: "build_refresh", ctx: ctxBuilds, keys: []string{"r"}, show: "r", desc: "Refresh the builds", footer: say("refresh"), when: needsBuilds},

	// Everywhere.
	{id: "tab", ctx: ctxEverywhere, keys: []string{"tab", "shift+tab"}, show: "tab shift+tab", desc: "Next or previous pane; log to log when one shows",
		footer: func(m *Model) string {
			switch {
			case m.zoomed && m.logCount() > 1, m.logCount() > 1 && m.arrange == arrangeOne:
				return "next log"
			case m.focusOnLog() && m.logCount() > 1:
				return "next pane"
			case m.focusOnLog():
				return "devices"
			}
			return "next pane"
		}},
	{id: "jump", ctx: ctxEverywhere, keys: []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}, show: "1 to 9", desc: "Go to that log"},
	{id: "all_logs", ctx: ctxEverywhere, keys: []string{"a"}, show: "a", desc: "List the open logs, to go to one or close it",
		footer: func(m *Model) string {
			if m.logCount() > 0 {
				return "all logs"
			}
			return ""
		}},
	{id: "zoom", ctx: ctxEverywhere, keys: []string{"z"}, show: "z", desc: "Full screen for the focused pane, again to undo",
		footer: func(m *Model) string {
			if m.zoomed {
				return "back"
			}
			return "full screen"
		}},
	{id: "layout", ctx: ctxEverywhere, keys: []string{"v"}, show: "v", desc: "Layout of the logs: side by side, stacked, or one at a time",
		footer: func(m *Model) string {
			if m.logCount() > 1 && !m.zoomed {
				return "layout"
			}
			return ""
		}},
	{id: "widen", ctx: ctxEverywhere, keys: []string{"+", "=", "-"}, show: "+ -", desc: "Widen or narrow the devices pane"},
	{id: "esc", ctx: ctxEverywhere, keys: []string{"esc"}, show: "esc", desc: "Back to the devices",
		footer: func(m *Model) string {
			if m.logCount() > 1 && m.arrange == arrangeOne && !m.zoomed {
				return "devices"
			}
			return ""
		}},
	{id: "quick", ctx: ctxEverywhere, keys: []string{"ctrl+p"}, show: "ctrl+p", desc: "Quick: screenshot or video to the clipboard, log for AI",
		footer: say("quick")},
	{id: "keys", ctx: ctxEverywhere, keys: []string{"?"}, show: "?", desc: "This menu", footer: say("keys")},
	{id: "quit", ctx: ctxEverywhere, keys: []string{"q", "ctrl+c"}, show: "q", desc: "Quit (simulators and emulators keep running)", footer: say("quit")},

	// The run view.
	{id: "run_switch", ctx: ctxRun, keys: []string{"ctrl+]"}, show: "ctrl+]", desc: "The run or the full dashboard; the tool keeps running",
		footer: func(m *Model) string {
			if m.run == nil {
				return ""
			}
			if m.runShown() {
				return "dashboard"
			}
			return "back to " + m.runTitle()
		}, when: needsRun},
	{id: "run_focus", ctx: ctxRun, keys: []string{"ctrl+o"}, show: "ctrl+o", desc: "Give the keys to the tool or to its log",
		footer: func(m *Model) string {
			if m.run != nil && m.run.onTool {
				return "log"
			}
			return ""
		}, when: needsRun},
	{id: "run_quick", ctx: ctxRun, keys: []string{"ctrl+p"}, show: "ctrl+p", desc: "Quick actions on the run's device, from either pane",
		footer: say("quick"), when: needsRun},
	{id: "run_back", ctx: ctxRun, keys: []string{"esc"}, show: "esc", desc: "From the log, give the keys back to the tool",
		footer: func(m *Model) string { return "back to " + m.runTitle() }, when: needsRun},
	{id: "run_scroll", ctx: ctxRun, keys: []string{"shift+pgup", "shift+pgdown", "shift+up", "shift+down"}, show: "shift+pgup",
		desc: "Scroll back through the tool's output; the wheel does too", footer: say("scroll"), when: needsRun},
	{id: "run_again", ctx: ctxRun, keys: []string{"enter"}, show: "enter", desc: "Run the tool again once it has ended",
		footer: say("run again"), when: func(m *Model) string {
			if why := needsRun(m); why != "" {
				return why
			}
			if !m.run.ended {
				return m.runTitle() + " is running"
			}
			return ""
		}},
	{id: "run_pick", ctx: ctxRun, keys: []string{"l"}, show: "l", desc: "From the log: pick the app, which opens by itself next time",
		footer: say("pick the app"), when: needsRun},
	{id: "run_line", ctx: ctxRun, keys: []string{"shift+left", "shift+right"}, show: "shift+←→", hintKey: "shift+←→",
		desc: "Move the line between the panes, from either pane", footer: say("resize"), when: needsRun},
	{id: "run_resize", ctx: ctxRun, keys: []string{"+", "=", "-"}, show: "+ -", desc: "From the log: widen or narrow it, or drag the line",
		when: needsRun},
	{id: "run_zoom", ctx: ctxRun, keys: []string{"z"}, show: "z", desc: "From the log: show it at full width, again to undo",
		footer: say("full width"), when: needsRun},
	{id: "run_help", ctx: ctxRun, keys: []string{"?"}, show: "?", desc: "From the log, or once the tool ended: this menu",
		footer: say("keys"), when: needsRun},
	{id: "run_quit", ctx: ctxRun, keys: []string{"q", "ctrl+c"}, show: "q", desc: "From the log, or once the tool ended: quit (asks first)",
		footer: say("quit"), when: needsRun},
}

// keyByID is the action with this id.
func keyByID(id string) *keyAction {
	for i := range keyTable {
		if keyTable[i].id == id {
			return &keyTable[i]
		}
	}
	return nil
}

// footerKeys say which actions the hints under the dashboard show, in order, for what has the
// focus.
func (m *Model) footerKeys() []string {
	if m.runShown() {
		return m.runFooterKeys()
	}
	var ids []string
	switch {
	case m.focusOnLog() && m.focusedLogOpen():
		switch {
		case m.zoomed && m.logCount() > 1:
			ids = []string{"zoom", "tab", "all_logs", "log_close"}
		case m.zoomed:
			ids = []string{"zoom", "log_close"}
		case m.logCount() > 1 && m.arrange == arrangeOne:
			ids = []string{"tab", "all_logs", "zoom", "layout", "esc", "log_close"}
		case m.logCount() > 1:
			ids = []string{"all_logs", "tab", "zoom", "layout", "log_close"}
		default:
			ids = []string{"zoom", "tab", "log_close"}
		}
		ids = append(ids, "quick", "log_line", "log_copy", "log_filter", "log_presets", "log_level", "log_wrap", "log_pause", "log_clear", "log_save", "log_stop", "log_restart")
	case m.focusOnLog():
		ids = []string{"tab"}
	case m.focus == paneBuilds && !m.build.asked:
		ids = []string{"build_load", "tab"}
	case m.focus == paneBuilds:
		ids = []string{"build_load", "build_install", "build_open", "build_new", "build_cancel", "build_refresh", "tab"}
	default:
		ids = []string{"boot", "shutdown", "logs", "quick", "all_logs", "new", "erase", "delete", "copyid", "wifi", "refresh", "unavailable"}
	}
	if m.run != nil {
		// The way back to the tool comes first, so it is never left out for lack of room.
		ids = append([]string{"run_switch"}, ids...)
	}
	return append(ids, "keys", "quit")
}

// hintGap is what separates the hints.
const hintGap = "  "

// hintsLine is the line under the dashboard: the keys that matter for what has the focus, in a
// line of at most width columns. When they do not fit, the least important are left out from the
// end, so "? keys" and "q quit" are always there.
func (m *Model) hintsLine(width int) string {
	var parts []string
	for _, id := range m.footerKeys() {
		a := keyByID(id)
		if a == nil || a.footer == nil {
			continue
		}
		if label := a.footer(m); label != "" {
			key := a.keys[0]
			if a.hintKey != "" {
				key = a.hintKey
			}
			parts = append(parts, key+" "+label)
		}
	}
	// One column of margin on the left, one so the line does not touch the edge.
	fits := func() bool { return ansi.StringWidth(strings.Join(parts, hintGap)) <= width-2 }
	for !fits() && len(parts) > 2 {
		parts = append(parts[:len(parts)-3], parts[len(parts)-2:]...)
		// Saying that more keys exist takes room too, so it is counted before the next check.
		parts[len(parts)-2] = "? more keys"
	}
	return strings.Join(parts, hintGap)
}

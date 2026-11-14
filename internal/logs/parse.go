// Package logs streams device logs: what each device offers, how to read its lines, and a
// session that runs the stream and filters it to one app.
package logs

import (
	"regexp"
	"strings"
	"sync"

	"github.com/0xrnp/lazymobile/internal/device"
)

// Level ranks a log line from Verbose (1) to Fatal (6). Zero means the line has no level, as
// with plain console output and continuation lines.
type Level int

const (
	NoLevel Level = iota
	Verbose
	Debug
	Info
	Warning
	Error
	Fatal
)

var levelNames = []string{"all", "verbose", "debug", "info", "warning", "error", "fatal"}

func (l Level) String() string {
	if l < NoLevel || l > Fatal {
		return "all"
	}
	return levelNames[l]
}

// ParseLevel reads a level name. Unknown names are not ok.
func ParseLevel(name string) (Level, bool) {
	for i, known := range levelNames {
		if known == strings.ToLower(name) {
			return Level(i), true
		}
	}
	return NoLevel, false
}

// Line is one line of a log.
type Line struct {
	Text  string `json:"text"`
	Level Level  `json:"level,omitempty"`
	PID   string `json:"pid,omitempty"`
}

var androidRank = map[byte]Level{'V': Verbose, 'D': Debug, 'I': Info, 'W': Warning, 'E': Error, 'F': Fatal, 'A': Fatal}

// "11-13 20:49:44.298   917  1126 D Tag: msg"
var androidLine = regexp.MustCompile(`^\d\d-\d\d\s+\d\d:\d\d:\d\d\.\d+\s+(\d+)\s+\d+\s+([VDIWEFA])\s`)

// ParseAndroid reads a `logcat -v threadtime` line.
func ParseAndroid(text string) Line {
	m := androidLine.FindStringSubmatch(text)
	if m == nil {
		return Line{Text: text}
	}
	return Line{Text: text, PID: m[1], Level: androidRank[m[2][0]]}
}

// os_log types in `log stream --style compact`: Db debug, I info, Df default, E error, F fault.
// "A" lines are activities.
var iosRank = map[string]Level{"Db": Debug, "I": Info, "Df": Info, "A": Info, "E": Error, "F": Fatal}

// "2026-11-13 20:56:36.411 Df Runner[30348:6e9c9] message"
var iosLine = regexp.MustCompile(`^\d{4}-\d\d-\d\d \d\d:\d\d:\d\d\.\d+\s+([A-Za-z]+)\s+[^\[]+\[(\d+)`)

// ParseIOS reads a `log stream --style compact` line.
func ParseIOS(text string) Line {
	m := iosLine.FindStringSubmatch(text)
	if m == nil {
		return Line{Text: text}
	}
	return Line{Text: text, PID: m[2], Level: iosRank[m[1]]}
}

// ParsePlain is for an app's console output, which has no header.
func ParsePlain(text string) Line {
	return Line{Text: text}
}

// AppFilter keeps the lines of one Android app across restarts. An app gets a new pid each time
// it starts, so pids are learned from ActivityManager's "Start proc <pid>:<package>/..." line
// (instant) and from polling pidof (catches an app that was already running). Lines that mention
// the package, such as a crash report from the system, are kept too.
type AppFilter struct {
	pkg   string
	start *regexp.Regexp

	mu   sync.Mutex
	pids map[string]bool
}

// NewAppFilter returns a filter for one package.
func NewAppFilter(pkg string) *AppFilter {
	return &AppFilter{
		pkg:   pkg,
		start: regexp.MustCompile(`Start proc (\d+):` + regexp.QuoteMeta(pkg) + `[/:\s]`),
		pids:  map[string]bool{},
	}
}

// AddPIDs records process ids that belong to the app.
func (f *AppFilter) AddPIDs(pids []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, pid := range pids {
		f.pids[pid] = true
	}
}

// Accept reports whether a line belongs to the app.
func (f *AppFilter) Accept(line Line) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m := f.start.FindStringSubmatch(line.Text); m != nil {
		f.pids[m[1]] = true
		return true
	}
	if line.PID != "" && f.pids[line.PID] {
		return true
	}
	return strings.Contains(line.Text, f.pkg)
}

// Mode is a kind of log stream.
type Mode string

const (
	// Logcat is the Android log.
	Logcat Mode = "logcat"
	// System is the iOS unified log.
	System Mode = "system"
	// Console launches an iOS app and shows its standard output (print, NSLog).
	Console Mode = "console"
)

// ModeInfo describes a stream a device offers.
type ModeInfo struct {
	ID    Mode
	Label string
	// AppRequired is true when the stream cannot start without an app id.
	AppRequired bool
}

// Modes lists the log streams a device can give right now. It is empty when the device is not
// running.
func Modes(d device.Device) []ModeInfo {
	if !d.Ready() {
		return nil
	}
	launch := ModeInfo{ID: Console, Label: "Launch an app and show its output (print, NSLog)", AppRequired: true}
	switch {
	case d.Platform == device.Android:
		return []ModeInfo{{ID: Logcat, Label: "logcat (optionally only one app)"}}
	case d.Kind == device.Simulator:
		return []ModeInfo{{ID: System, Label: "System log (optionally only one app)"}, launch}
	}
	return []ModeInfo{launch, {ID: System, Label: "Device system log (needs idevicesyslog)"}}
}

var appID = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*$`)

// ValidAppID reports whether id looks like an Android package or an iOS bundle id. adb joins
// `shell` arguments into one command line for the device's shell, so anything else is refused
// before it gets there.
func ValidAppID(id string) bool {
	return appID.MatchString(id)
}

// An iOS process name goes into a log predicate, so only plain names are allowed.
var safeProcessName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._ -]*$`)

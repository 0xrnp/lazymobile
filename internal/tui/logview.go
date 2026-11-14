package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/0xrnp/lazymobile/internal/logs"
	"github.com/0xrnp/lazymobile/internal/mobile"
)

// maxLogLines is how many lines a log keeps. Older ones are dropped.
const maxLogLines = 20000

// logView is the state of the logs pane.
type logView struct {
	// id tells the messages of this stream from those of one that was replaced.
	id      int
	opened  bool
	session mobile.LogSession
	spec    logs.Spec

	// all is everything received, and shown is what passes the filters.
	all   []logs.Line
	shown []logs.Line
	// keptLast is whether the last line with a level passed, which decides for the lines without
	// one that follow it (the rest of a stack trace or a multi-line message).
	keptLast bool

	filter string
	level  logs.Level
	paused bool
	// follow keeps the newest line in view. When off, top is the first visible line.
	follow bool
	top    int
	left   int

	status string
	ended  bool
	endErr error
}

func (v *logView) passes(line logs.Line) bool {
	if line.Level == logs.NoLevel {
		if v.level != logs.NoLevel && !v.keptLast {
			return false
		}
	} else {
		v.keptLast = line.Level >= v.level
		if !v.keptLast {
			return false
		}
	}
	return v.filter == "" || strings.Contains(strings.ToLower(line.Text), strings.ToLower(v.filter))
}

func trim(lines []logs.Line) ([]logs.Line, int) {
	extra := len(lines) - maxLogLines
	if extra <= 0 {
		return lines, 0
	}
	return append(lines[:0:0], lines[extra:]...), extra
}

// add takes new lines. While paused they are kept but not shown, so nothing is lost.
func (v *logView) add(lines []logs.Line) {
	for _, line := range lines {
		line.Text = clean(line.Text)
		v.all = append(v.all, line)
		if !v.paused && v.passes(line) {
			v.shown = append(v.shown, line)
		}
	}
	v.all, _ = trim(v.all)
	var dropped int
	v.shown, dropped = trim(v.shown)
	if !v.follow {
		v.top = max(v.top-dropped, 0)
	}
}

// rebuild applies the filters to everything again.
func (v *logView) rebuild() {
	v.keptLast = false
	v.shown = v.shown[:0:0]
	for _, line := range v.all {
		if v.passes(line) {
			v.shown = append(v.shown, line)
		}
	}
	v.top = min(v.top, max(len(v.shown)-1, 0))
}

func (v *logView) clear() {
	v.all, v.shown, v.top, v.follow, v.keptLast = nil, nil, 0, true, false
}

// start is the index of the first visible line for a pane of this many rows.
func (v *logView) start(rows int) int {
	last := max(len(v.shown)-rows, 0)
	if v.follow {
		return last
	}
	return min(max(v.top, 0), last)
}

// scroll moves the view by delta lines. Reaching the end turns following back on.
func (v *logView) scroll(delta, rows int) {
	last := max(len(v.shown)-rows, 0)
	next := v.start(rows) + delta
	if next >= last {
		v.follow, v.top = true, last
		return
	}
	v.follow, v.top = false, max(next, 0)
}

func (v *logView) title() string {
	if !v.opened {
		return "Logs"
	}
	parts := []string{"Logs: " + v.spec.Device.Name}
	if v.spec.App != "" {
		parts = append(parts, v.spec.App)
	}
	if v.spec.Mode == logs.Console {
		parts = append(parts, "console")
	}
	if v.level != logs.NoLevel {
		parts = append(parts, v.level.String()+"+")
	}
	if v.filter != "" {
		parts = append(parts, fmt.Sprintf("%q", v.filter))
	}
	switch {
	case v.paused:
		parts = append(parts, "paused")
	case v.ended:
		parts = append(parts, "ended")
	case !v.follow:
		parts = append(parts, "scrolled")
	}
	return strings.Join(parts, " · ")
}

// lines draws the pane's content: rows of log, then one status row.
func (v *logView) lines(width, rows int) []string {
	if !v.opened {
		return []string{"", dim.Render(" Select a running device and press l to see its logs.")}
	}
	logRows := max(rows-1, 0)
	out := make([]string, 0, rows)
	start := v.start(logRows)
	for i := start; i < len(v.shown) && i < start+logRows; i++ {
		line := v.shown[i]
		text := line.Text
		if v.left > 0 {
			text = ansi.Cut(text, v.left, v.left+width)
		}
		out = append(out, levelStyle(line.Level).Render(ansi.Truncate(text, width, "…")))
	}
	for len(out) < logRows {
		out = append(out, "")
	}

	var status string
	switch {
	case v.endErr != nil:
		status = red.Render(" " + firstLine(v.endErr.Error()))
	case v.ended:
		status = dim.Render(" The stream was stopped. Press r to start it again.")
	case len(v.all) == 0:
		status = dim.Render(" Waiting for log lines… " + v.status)
	default:
		count := fmt.Sprintf(" %d shown", len(v.shown))
		if hidden := len(v.all) - len(v.shown); hidden > 0 {
			count += fmt.Sprintf(", %d hidden", hidden)
		}
		status = dim.Render(count + "  " + v.status)
	}
	return append(out, status)
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return line
}

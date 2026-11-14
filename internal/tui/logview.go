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
	// levelChosen is set once the level is changed by hand. Until then each stream starts at its
	// own default.
	levelChosen bool
	paused      bool
	// follow keeps the newest line in view and selected. When off, top is the first visible line
	// and cursor the selected one.
	follow bool
	top    int
	cursor int
	left   int
	// wrap shows long lines in full over several rows, in place of cutting them at the edge.
	wrap bool

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
		v.cursor = max(v.cursor-dropped, 0)
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
	v.cursor = min(v.cursor, max(len(v.shown)-1, 0))
}

func (v *logView) clear() {
	v.all, v.shown, v.top, v.cursor, v.follow, v.keptLast = nil, nil, 0, 0, true, false
}

// start is the index of the first visible line for a pane of this many rows.
func (v *logView) start(rows int) int {
	last := max(len(v.shown)-rows, 0)
	if v.follow {
		return last
	}
	return min(max(v.top, 0), last)
}

// at is the index of the selected line: the newest one while following. It is -1 when there are
// no lines.
func (v *logView) at() int {
	if v.follow {
		return len(v.shown) - 1
	}
	return min(max(v.cursor, 0), len(v.shown)-1)
}

// scroll moves the selection by delta lines and the view with it. Reaching the newest line turns
// following back on.
func (v *logView) scroll(delta, rows int) {
	if len(v.shown) == 0 {
		return
	}
	start := v.start(rows)
	next := v.at() + delta
	if next >= len(v.shown)-1 {
		v.follow, v.cursor, v.top = true, len(v.shown)-1, max(len(v.shown)-rows, 0)
		return
	}
	v.follow, v.cursor, v.top = false, max(next, 0), start
	switch {
	case v.cursor < start:
		v.top = v.cursor
	case v.cursor >= start+rows:
		v.top = v.cursor - rows + 1
	}
}

// slide scrolls sideways by delta columns, no further than the end of the widest line in view.
func (v *logView) slide(delta, width, rows int) {
	widest := 0
	start := v.start(rows)
	for i := start; i < len(v.shown) && i < start+rows; i++ {
		widest = max(widest, ansi.StringWidth(v.shown[i].Text))
	}
	v.left = min(max(v.left+delta, 0), max(widest-width, 0))
}

// first selects the oldest line.
func (v *logView) first() {
	v.follow, v.top, v.cursor = false, 0, 0
}

// entry is the selected line with the lines without a level that belong to it, such as the rest
// of a stack trace or of a multi-line message. A stream without levels gives the line alone.
func (v *logView) entry() []logs.Line {
	const most = 500
	i := v.at()
	if i < 0 {
		return nil
	}
	head := i
	for head > 0 && v.shown[head].Level == logs.NoLevel && i-head < most {
		head--
	}
	if v.shown[head].Level == logs.NoLevel {
		return v.shown[i : i+1]
	}
	end := i + 1
	for end < len(v.shown) && v.shown[end].Level == logs.NoLevel && end-head < most {
		end++
	}
	return v.shown[head:end]
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

// lines draws the pane's content: rows of log, then one status row. The selected line is marked
// when the pane has the focus and the view is not following.
func (v *logView) lines(width, rows int, focused bool) []string {
	if !v.opened {
		return []string{"", dim.Render(" Select a running device and press l to see its logs.")}
	}
	logRows := max(rows-1, 0)
	selected := -1
	if focused && !v.follow {
		selected = v.at()
	}
	var out []string
	cut := false
	if v.wrap {
		out = v.wrapped(width, logRows, selected)
	} else {
		start := v.start(logRows)
		// After the pane shrank, the selected line can be outside the rows top gives.
		if selected >= start+logRows {
			start = selected - logRows + 1
		}
		for i := start; i < len(v.shown) && i < start+logRows; i++ {
			line := v.shown[i]
			text := line.Text
			if v.left > 0 {
				text = ansi.Cut(text, v.left, v.left+width)
			}
			if ansi.StringWidth(line.Text) > v.left+width {
				cut = true
			}
			text = ansi.Truncate(text, width, "…")
			if i == selected {
				out = append(out, chosen.Render(fit(text, width)))
				continue
			}
			out = append(out, levelStyle(line.Level).Render(text))
		}
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
		if cut {
			count += "  (long lines cut: slide or h l to scroll, w wraps, enter shows one)"
		}
		status = dim.Render(count + "  " + v.status)
	}
	return append(out, status)
}

// wrapped draws the visible lines wrapped to the width. While following, the newest line ends at
// the bottom row. Otherwise the line at top starts at the first row, unless that would leave the
// selected line out of view: then the selected line ends at the bottom row.
func (v *logView) wrapped(width, rows, selected int) []string {
	pieces := func(i int) []string {
		line := v.shown[i]
		parts := strings.Split(ansi.Hardwrap(line.Text, width, true), "\n")
		for j, part := range parts {
			if i == selected {
				parts[j] = chosen.Render(fit(part, width))
			} else {
				parts[j] = levelStyle(line.Level).Render(part)
			}
		}
		return parts
	}
	upTo := func(last int) []string {
		var out []string
		for i := last; i >= 0 && len(out) < rows; i-- {
			out = append(pieces(i), out...)
		}
		return out[max(len(out)-rows, 0):]
	}
	if v.follow {
		return upTo(len(v.shown) - 1)
	}
	var out []string
	last := -1
	for i := v.start(rows); i < len(v.shown) && len(out) < rows; i++ {
		out = append(out, pieces(i)...)
		if len(out) <= rows {
			last = i
		}
	}
	// A first line taller than the pane shows from its start.
	if at := v.at(); at > last && at != v.start(rows) {
		return upTo(at)
	}
	return out[:min(len(out), rows)]
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return line
}

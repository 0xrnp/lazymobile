package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/0xrnp/lazymobile/internal/logs"
	"github.com/0xrnp/lazymobile/internal/mobile"
)

// Any number of logs can be open, each in a pane of its own. m.logPanes is always there and holds
// at least one pane. Only the first can be empty, which is how the dashboard starts, so every other
// pane has a log in it.

// newLog asks for a pane of its own: the first, while that is empty, and otherwise a new one.
const newLog = -1

// arrangement is how open logs sit in the area the logs have. Its values are what the state file
// keeps.
type arrangement string

const (
	// arrangeSide fills rows with columns, as many as fit, which is side by side for a few logs.
	arrangeSide arrangement = ""
	// arrangeStacked fills columns with rows, as many as fit, which is one above the other.
	arrangeStacked arrangement = "stacked"
	// arrangeOne is one log at a time, the one in focus, with tab flipping to the next.
	arrangeOne arrangement = "one"
)

// next is the arrangement v goes to.
func (a arrangement) next() arrangement {
	switch a {
	case arrangeSide:
		return arrangeStacked
	case arrangeStacked:
		return arrangeOne
	}
	return arrangeSide
}

// A log pane smaller than this is not worth drawing beside others.
const (
	minSideWidth  = 30
	minStackedRow = 6
)

// first is the pane that is always there.
func (m *Model) first() *logView { return m.logPanes[0] }

// logCount is how many logs are open.
func (m *Model) logCount() int {
	if !m.first().opened {
		return 0
	}
	return len(m.logPanes)
}

func (m *Model) focusOnLog() bool { return m.focus == paneLogs }

// focusedLog is the log pane that has the focus, if the focus is on one.
func (m *Model) focusedLog() (int, *logView, bool) {
	if m.focus != paneLogs || m.logIdx < 0 || m.logIdx >= len(m.logPanes) {
		return 0, nil, false
	}
	return m.logIdx, m.logPanes[m.logIdx], true
}

func (m *Model) focusedLogOpen() bool {
	_, v, ok := m.focusedLog()
	return ok && v.opened
}

// focusLog moves the focus to log pane i.
func (m *Model) focusLog(i int) {
	m.focus, m.logIdx, m.lastLog = paneLogs, i, i
}

// logByID is the pane a stream's message is for, or nil when that stream was replaced or closed.
func (m *Model) logByID(id int) *logView {
	if id <= 0 {
		return nil
	}
	for _, v := range m.logPanes {
		if v.id == id {
			return v
		}
	}
	return nil
}

// logSessions are the log tools that are running.
func (m *Model) logSessions() []mobile.LogSession {
	var out []mobile.LogSession
	for _, v := range m.logPanes {
		if v.session != nil {
			out = append(out, v.session)
		}
	}
	return out
}

// position is "2/5" for a log pane while more than one log is open, and "" otherwise.
func (m *Model) position(i int) string {
	if m.logCount() < 2 {
		return ""
	}
	return strconv.Itoa(i+1) + "/" + strconv.Itoa(len(m.logPanes))
}

// settingsOf is an empty pane that starts with the filter, level and wrap of v.
func settingsOf(v *logView) logView {
	return logView{follow: true, filter: v.filter, level: v.level, levelChosen: v.levelChosen, wrap: v.wrap}
}

// slotFor is the pane a log goes in: the one asked for, or, for newLog or a pane that is gone, the
// first while it is empty, and otherwise a new pane after the others.
func (m *Model) slotFor(slot int) int {
	if slot >= 0 && slot < len(m.logPanes) {
		return slot
	}
	if !m.first().opened {
		return 0
	}
	m.logPanes = append(m.logPanes, &logView{follow: true})
	return len(m.logPanes) - 1
}

// parkLog puts a log in a pane without starting it. It is shown, and saved, as it would be if it
// ran, and r starts it.
func (m *Model) parkLog(slot int, spec logs.Spec) {
	slot = m.slotFor(slot)
	v := m.logPanes[slot]
	level := v.level
	if !v.levelChosen {
		level = defaultLevel(spec)
	}
	*v = logView{opened: true, parked: true, spec: spec, follow: true, level: level, levelChosen: v.levelChosen, filter: v.filter, wrap: v.wrap}
}

// closeLog stops a log and takes its pane away. The focus goes to the pane that took its place, or
// the one before when it was the last. The first pane stays, empty, when it is the only one.
func (m *Model) closeLog(slot int) {
	v := m.logPanes[slot]
	if v.session != nil {
		v.session.Stop()
	}
	if len(m.logPanes) == 1 {
		*v = settingsOf(v)
	} else {
		m.logPanes = append(m.logPanes[:slot:slot], m.logPanes[slot+1:]...)
	}
	m.lastLog = min(m.lastLog, len(m.logPanes)-1)
	if !m.first().opened {
		m.focus = paneDevices
		return
	}
	m.focusLog(min(slot, len(m.logPanes)-1))
}

// showLogs lists every open log, to jump to one or close one without leaving the list.
func (m *Model) showLogs() {
	if m.logCount() == 0 {
		m.setNote("No log is open. Press l on a device", false)
		return
	}
	m.modal = m.logsModal(m.logIdx)
}

func (m *Model) logsModal(cursor int) *modal {
	items := make([]string, len(m.logPanes))
	for i, v := range m.logPanes {
		items[i] = fmt.Sprintf("%d  %s", i+1, strings.TrimPrefix(v.title(1000, ""), "Logs: "))
	}
	return &modal{
		kind: modalChoice, title: fmt.Sprintf("Logs (%d open)", len(items)), items: items,
		cursor: min(max(cursor, 0), len(items)-1), extraHint: "x: close it",
		onChoose: func(m *Model, index int) tea.Cmd {
			m.zoomed = m.zoomed && m.arrange != arrangeOne
			m.focusLog(index)
			return nil
		},
		onKey: func(m *Model, key string, cursor int) tea.Cmd {
			if key != "x" && key != "X" {
				return nil
			}
			m.closeLog(cursor)
			if m.logCount() == 0 {
				m.modal = nil
				return nil
			}
			m.modal = m.logsModal(cursor)
			return nil
		},
	}
}

// logBox is where a log pane is drawn, in screen cells.
type logBox struct {
	x, y, width, height int
}

// rows is how many log lines fit: two border rows and one status row are not log lines.
func (b logBox) rows() int { return max(b.height-3, 0) }

func (b logBox) contains(x, y int) bool {
	return b.width > 0 && x >= b.x && x < b.x+b.width && y >= b.y && y < b.y+b.height
}

// grid is how n panes sit in an area of this size: columns and rows of cells that are not smaller
// than the least a log is worth. Side by side takes columns first, as many as fit. Stacked takes
// rows first. It reports false when no grid keeps the cells that big.
func (a arrangement) grid(n, area, height int) (cols, rows int, ok bool) {
	if a == arrangeStacked {
		rows = min(n, height/minStackedRow)
		if rows < 1 {
			return 0, 0, false
		}
		cols = (n + rows - 1) / rows
		rows = (n + cols - 1) / cols
	} else {
		cols = min(n, area/minSideWidth)
		if cols < 1 {
			return 0, 0, false
		}
		rows = (n + cols - 1) / cols
	}
	return cols, rows, area/cols >= minSideWidth && height/rows >= minStackedRow
}

// logBoxes places the log panes in the area the logs have, which starts at column x, is area wide
// and as tall as the body. A pane that is not drawn has no width. Open logs share the area in a
// grid, side by side or stacked as m.arrange says, and a last row with fewer panes shares the
// whole width. When the cells would be too small, when only one pane fits on the screen or it is
// zoomed, or for one log at a time, the pane in focus (or, from the devices, the one last in
// focus) has it all.
func (m *Model) logBoxes(x, area, height int) []logBox {
	n := len(m.logPanes)
	boxes := make([]logBox, n)
	if area <= 0 || height <= 0 {
		return boxes
	}
	whole := func(i int) { boxes[i] = logBox{x: x, width: area, height: height} }
	if n == 1 {
		whole(0)
		return boxes
	}
	if m.width >= splitWidth && !m.zoomed && m.arrange != arrangeOne {
		if cols, rows, ok := m.arrange.grid(n, area, height); ok {
			index, y := 0, 0
			for r := range rows {
				cellHeight := height / rows
				if r == rows-1 {
					cellHeight = height - y
				}
				count := min(cols, n-index)
				cx := x
				for c := range count {
					cellWidth := area / count
					if c == count-1 {
						cellWidth = x + area - cx
					}
					boxes[index] = logBox{x: cx, y: y, width: cellWidth, height: cellHeight}
					cx += cellWidth
					index++
				}
				y += cellHeight
			}
			return boxes
		}
	}
	if m.focus == paneLogs && m.logIdx < n {
		whole(m.logIdx)
	} else {
		whole(min(m.lastLog, n-1))
	}
	return boxes
}

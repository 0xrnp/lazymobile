package tui

import (
	"fmt"

	"github.com/0xrnp/lazymobile/internal/logs"
	"github.com/0xrnp/lazymobile/internal/mobile"
)

// There are two log panes. The first is m.log, where the log has always been kept, and the second
// is m.second. A log opens in the second only while the first is taken, so the open ones are
// always the first, or the first and the second.
const logSlots = 2

// arrangement is how two open logs sit in the area the logs have. Its values are what the state
// file keeps.
type arrangement string

const (
	// arrangeSide is two logs next to each other, which is the default.
	arrangeSide arrangement = ""
	// arrangeStacked is one log above the other.
	arrangeStacked arrangement = "stacked"
	// arrangeOne is one log at a time, the one in focus, with tab flipping to the other.
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

// Where a log pane is smaller than this, it is not worth drawing beside another.
const (
	minSideWidth  = 30
	minStackedRow = 6
)

// slotOf is which log pane p is, if it is one.
func slotOf(p pane) (int, bool) {
	switch p {
	case paneLogs:
		return 0, true
	case paneLogs2:
		return 1, true
	}
	return 0, false
}

func logPane(slot int) pane {
	if slot == 1 {
		return paneLogs2
	}
	return paneLogs
}

func (m *Model) slot(i int) *logView {
	if i == 1 {
		return &m.second
	}
	return &m.log
}

// logByID is the pane a stream's message is for, or nil when that stream was replaced or closed.
func (m *Model) logByID(id int) *logView {
	for i := range logSlots {
		if v := m.slot(i); id > 0 && v.id == id {
			return v
		}
	}
	return nil
}

// focusedLog is the log pane that has the focus, if the focus is on one.
func (m *Model) focusedLog() (int, *logView, bool) {
	slot, ok := slotOf(m.focus)
	if !ok {
		return 0, nil, false
	}
	return slot, m.slot(slot), true
}

func (m *Model) focusOnLog() bool {
	_, ok := slotOf(m.focus)
	return ok
}

func (m *Model) focusedLogOpen() bool {
	_, v, ok := m.focusedLog()
	return ok && v.opened
}

// logSessions are the log tools that are running.
func (m *Model) logSessions() []mobile.LogSession {
	var out []mobile.LogSession
	for i := range logSlots {
		if s := m.slot(i).session; s != nil {
			out = append(out, s)
		}
	}
	return out
}

// nextLogSlot is the log pane a log opens in. It goes beside the one that is open while there is
// room, and otherwise takes the place of the one that had the focus last.
func (m *Model) nextLogSlot() int {
	if m.log.opened && !m.second.opened {
		return 1
	}
	return m.lastLog
}

// position is "1/2" for a log pane while two logs are open, and "" otherwise.
func (m *Model) position(slot int) string {
	if !m.second.opened {
		return ""
	}
	return fmt.Sprintf("%d/%d", slot+1, logSlots)
}

// parkLog puts a log in the log pane slot without starting it. It is shown, and saved, as it
// would be if it ran, and r starts it. Like startLogs, the second pane only opens while the first
// is taken.
func (m *Model) parkLog(slot int, spec logs.Spec) {
	if !m.log.opened {
		slot = 0
	}
	v := m.slot(slot)
	level := v.level
	if !v.levelChosen {
		level = defaultLevel(spec)
	}
	*v = logView{opened: true, parked: true, spec: spec, follow: true, level: level, levelChosen: v.levelChosen, filter: v.filter, wrap: v.wrap}
}

// closeLog stops a log and takes its pane away. The second log moves up into the first.
func (m *Model) closeLog(slot int) {
	v := m.slot(slot)
	if v.session != nil {
		v.session.Stop()
	}
	settings := logView{follow: true, filter: v.filter, level: v.level, levelChosen: v.levelChosen, wrap: v.wrap}
	if slot == 0 && m.second.opened {
		m.log, m.second = m.second, logView{follow: true, filter: m.second.filter, level: m.second.level, levelChosen: m.second.levelChosen, wrap: m.second.wrap}
	} else {
		*v = settings
	}
	m.lastLog = 0
	m.focus = paneDevices
	if m.log.opened {
		m.focus = paneLogs
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

// logBoxes places the log panes in the area the logs have, which is area wide and as tall as the
// body. A pane that is not drawn has no width. With two logs open they share the area, one above
// the other or side by side, which is the default, as m.arrange says. When they would be too small for that, or only one pane
// is shown on the screen, the pane in focus (or, from the devices, the one last in focus) has it all.
func (m *Model) logBoxes(x, area, height int) [logSlots]logBox {
	var boxes [logSlots]logBox
	if area <= 0 || height <= 0 {
		return boxes
	}
	whole := func(slot int) { boxes[slot] = logBox{x: x, width: area, height: height} }
	if !m.second.opened {
		whole(0)
		return boxes
	}
	shared := m.width >= splitWidth && !m.zoomed
	if shared && m.arrange == arrangeSide && area/2 >= minSideWidth {
		first := area / 2
		boxes[0] = logBox{x: x, width: first, height: height}
		boxes[1] = logBox{x: x + first, width: area - first, height: height}
		return boxes
	}
	if shared && m.arrange == arrangeStacked && height/2 >= minStackedRow {
		first := height / 2
		boxes[0] = logBox{x: x, width: area, height: first}
		boxes[1] = logBox{x: x, y: first, width: area, height: height - first}
		return boxes
	}
	if slot, ok := slotOf(m.focus); ok {
		whole(slot)
		return boxes
	}
	whole(m.lastLog)
	return boxes
}

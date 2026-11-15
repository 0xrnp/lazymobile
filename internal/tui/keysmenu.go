package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// keyColumn is the width of the column of keys in the menu: "tab shift+tab" is the widest.
const keyColumn = 13

// keyRow is a line of the menu: a section title, or an action.
type keyRow struct {
	header string
	action *keyAction
}

// keysMenu lists what the keys do, the focused pane's keys first. Enter runs the action under the
// cursor, as if its key had been pressed.
type keysMenu struct {
	m     *Model
	order []keyContext
	// cursor is the row the cursor is on. It is always an action.
	cursor    int
	filter    string
	filtering bool
}

// keysModal opens the menu for what has the focus.
func (m *Model) keysModal() *modal {
	first := ctxDevices
	switch m.focus {
	case paneLogs:
		first = ctxLogs
	case paneBuilds:
		first = ctxBuilds
	}
	order := []keyContext{first, ctxEverywhere}
	for _, c := range []keyContext{ctxDevices, ctxLogs, ctxBuilds} {
		if c != first && (c != ctxBuilds || m.builds != nil) {
			order = append(order, c)
		}
	}
	k := &keysMenu{m: m, order: order}
	k.settle()
	return &modal{kind: modalKeys, title: "Keys", keys: k}
}

// unavailable says why an action cannot be done now, or "".
func (a *keyAction) unavailable(m *Model) string {
	if a.when == nil {
		return ""
	}
	return a.when(m)
}

func (k *keysMenu) matches(a *keyAction) bool {
	if k.filter == "" {
		return true
	}
	text := strings.ToLower(a.show + " " + a.desc + " " + a.ctx.String())
	return strings.Contains(text, strings.ToLower(k.filter))
}

// rows are the lines shown: each section that has an action the filter keeps, with those actions.
func (k *keysMenu) rows() []keyRow {
	var out []keyRow
	for _, c := range k.order {
		start := len(out)
		out = append(out, keyRow{header: c.String()})
		for i := range keyTable {
			if a := &keyTable[i]; a.ctx == c && k.matches(a) {
				out = append(out, keyRow{action: a})
			}
		}
		if len(out) == start+1 {
			out = out[:start]
		}
	}
	return out
}

// settle puts the cursor on the first action, which is where it goes when the rows change.
func (k *keysMenu) settle() {
	k.cursor = 0
	for i, r := range k.rows() {
		if r.action != nil {
			k.cursor = i
			return
		}
	}
}

// move takes the cursor to the next action up or down, and stays where it is at either end.
func (k *keysMenu) move(step int) {
	rows := k.rows()
	for i := k.cursor + step; i >= 0 && i < len(rows); i += step {
		if rows[i].action != nil {
			k.cursor = i
			return
		}
	}
}

// edge takes the cursor to the first or last action.
func (k *keysMenu) edge(last bool) {
	k.settle()
	if !last {
		return
	}
	rows := k.rows()
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].action != nil {
			k.cursor = i
			return
		}
	}
}

// selected is the action under the cursor, if the filter left any.
func (k *keysMenu) selected() *keyAction {
	rows := k.rows()
	if k.cursor < 0 || k.cursor >= len(rows) {
		return nil
	}
	return rows[k.cursor].action
}

// position is how many actions come up to the cursor, and how many there are.
func (k *keysMenu) position() (int, int) {
	at, total := 0, 0
	for i, r := range k.rows() {
		if r.action == nil {
			continue
		}
		total++
		if i <= k.cursor {
			at = total
		}
	}
	return at, total
}

// wheel moves the cursor three actions per notch, as the wheel scrolls three lines in a log.
func (k *keysMenu) wheel(button tea.MouseButton) {
	step := map[tea.MouseButton]int{tea.MouseWheelUp: -1, tea.MouseWheelDown: 1}[button]
	for range 3 {
		k.move(step)
	}
}

// keysMenuKey handles a key press while the menu is open.
func (m *Model) keysMenuKey(msg tea.KeyPressMsg) tea.Cmd {
	k := m.modal.keys
	key := msg.String()
	if k.filtering {
		switch key {
		case "esc":
			k.filter, k.filtering = "", false
			k.settle()
		case "enter":
			k.filtering = false
		case "up":
			k.move(-1)
		case "down":
			k.move(1)
		case "backspace":
			if runes := []rune(k.filter); len(runes) > 0 {
				k.filter = string(runes[:len(runes)-1])
				k.settle()
			}
		case "ctrl+u":
			k.filter = ""
			k.settle()
		case "ctrl+c":
			m.modal = nil
		default:
			if text := msg.Key().Text; text != "" {
				k.filter += text
				k.settle()
			}
		}
		return nil
	}
	switch key {
	case "esc":
		if k.filter != "" {
			k.filter = ""
			k.settle()
			return nil
		}
		m.modal = nil
	case "q", "ctrl+c", "?":
		m.modal = nil
	case "/":
		k.filtering = true
	case "up", "k":
		k.move(-1)
	case "down", "j":
		k.move(1)
	case "g", "home":
		k.edge(false)
	case "G", "end":
		k.edge(true)
	case "pgup", "ctrl+u":
		for range max(m.layout().bodyHeight/2, 1) {
			k.move(-1)
		}
	case "pgdown", "ctrl+d", "space":
		for range max(m.layout().bodyHeight/2, 1) {
			k.move(1)
		}
	case "enter":
		return m.runKey(k.selected())
	}
	return nil
}

// runKey does an action by sending its key to the dashboard, after moving the focus to the pane
// the key belongs to. It is what pressing the key there does, a question to confirm included.
func (m *Model) runKey(a *keyAction) tea.Cmd {
	if a == nil {
		return nil
	}
	if why := a.unavailable(m); why != "" {
		m.setNote(why, true)
		return nil
	}
	m.modal = nil
	switch {
	case a.ctx == ctxDevices && m.focus != paneDevices:
		m.focus, m.zoomed = paneDevices, false
	case a.ctx == ctxBuilds && m.focus != paneBuilds:
		m.focus, m.zoomed = paneBuilds, false
	case a.ctx == ctxLogs && !m.focusOnLog():
		m.focusLog(min(m.lastLog, len(m.logPanes)-1))
		m.zoomed = false
	}
	return m.keyString(a.keys[0])
}

// render draws the menu in a window with room for inner columns, on a screen of this height.
func (k *keysMenu) render(inner, height int) []string {
	// A tall screen keeps some of the dashboard in view above and below the window.
	if height >= 20 {
		height -= 6
	}
	rows := k.rows()
	selected := k.selected()

	// The window takes the border and the hint, and above the hint two lines that say why an action
	// is unavailable when the screen is tall enough, or one blank line when it is not.
	why := 0
	if height >= 14 {
		why = 2
	}
	var head []string
	if k.filtering {
		head = append(head, " "+cyan.Render("/ ")+ansi.TruncateLeft(k.filter, max(ansi.StringWidth(k.filter)-(inner-6), 0), "")+chosen.Render(" "))
	} else if k.filter != "" {
		head = append(head, " "+dim.Render("filter: ")+k.filter)
	}
	room := max(height-3-max(why, 1)-len(head), 1)

	first := 0
	if len(rows) > room {
		first = min(max(k.cursor-room/2, 0), len(rows)-room)
	}
	lines := head
	if len(rows) == 0 {
		lines = append(lines, " "+dim.Render("No key matches "+k.filter))
	}
	for i := first; i < first+room && i < len(rows); i++ {
		lines = append(lines, k.line(rows[i], i == k.cursor, inner-1))
	}
	if why == 0 {
		lines = append(lines, "")
	}

	var reason []string
	if selected != nil {
		if text := selected.unavailable(k.m); text != "" {
			for _, line := range strings.Split(ansi.Wordwrap(text, inner-2, " "), "\n") {
				reason = append(reason, " "+yellow.Render(line))
			}
		}
	}
	for i := range why {
		if i < len(reason) {
			lines = append(lines, reason[i])
		} else {
			lines = append(lines, "")
		}
	}

	hint := "enter: run   /: filter   esc: close"
	if k.filtering {
		hint = "enter: keep   esc: clear   up down: move"
	}
	at, total := k.position()
	if counter := fmt.Sprintf("%d of %d", at, total); total > 0 && ansi.StringWidth(hint)+2+len(counter) <= inner-1 {
		hint += strings.Repeat(" ", inner-1-ansi.StringWidth(hint)-len(counter)) + counter
	}
	lines = append(lines, " "+dim.Render(hint))

	return box("Keys", lines, inner+2, min(len(lines)+2, height), true)
}

// line draws a row for a window width columns wide.
func (k *keysMenu) line(r keyRow, selected bool, width int) string {
	if r.action == nil {
		title := " ── " + r.header + " "
		return dim.Render(fit(title+strings.Repeat("─", max(width-ansi.StringWidth(title), 0)), width))
	}
	a := r.action
	pad := strings.Repeat(" ", max(keyColumn-ansi.StringWidth(a.show), 0))
	off := a.unavailable(k.m) != ""
	if selected {
		style := chosen
		if off {
			style = style.Strikethrough(true)
		}
		return style.Render(fit(" "+pad+a.show+"  "+a.desc, width))
	}
	if off {
		return fit(" "+pad+strike.Render(a.show+"  "+a.desc), width)
	}
	return fit(" "+pad+cyan.Render(a.show)+"  "+highlight(a.desc, k.filter, plain), width)
}

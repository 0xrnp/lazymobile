package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/0xrnp/lazymobile/internal/device"
)

// Below this size nothing useful fits.
const (
	minWidth  = 44
	minHeight = 10
	// Below this width only the focused pane is shown.
	splitWidth = 100
	// resizeStep is how many columns + and - move the line between the panes.
	resizeStep = 4
	// minPaneWidth keeps both panes usable however far the line is moved.
	minPaneWidth = 30
)

type layout struct {
	// bodyHeight is the height of the panes, borders included.
	bodyHeight int
	// devicesWidth and logsWidth are zero for a pane that is hidden.
	devicesWidth, logsWidth int
	// logs is where each log pane is drawn. A pane that is not drawn has no width.
	logs []logBox
	// buildsHeight is the height of the builds pane under the devices, or zero without one.
	buildsHeight int
}

func (m *Model) layout() layout {
	l := layout{bodyHeight: max(m.height-2, 0)}
	split := m.width >= splitWidth && !m.zoomed
	switch {
	case split:
		l.devicesWidth = m.devicesWidth()
		l.logsWidth = m.width - l.devicesWidth
	case m.focusOnLog():
		l.logsWidth = m.width
	default:
		l.devicesWidth = m.width
	}
	if m.builds != nil && l.devicesWidth > 0 {
		switch {
		case split:
			l.buildsHeight = min(max(l.bodyHeight*2/5, 5), l.bodyHeight-5)
		case m.focus == paneBuilds:
			// In one-pane mode the builds take the whole screen.
			l.buildsHeight = l.bodyHeight
		}
	}
	l.logs = m.logBoxes(l.devicesWidth, l.logsWidth, l.bodyHeight)
	return l
}

// defaultDevicesWidth is the devices pane's width beside the logs before it is resized.
func (m *Model) defaultDevicesWidth() int {
	return min(max(m.width*45/100, 48), 64)
}

// devicesWidth is the devices pane's width beside the logs.
func (m *Model) devicesWidth() int {
	return min(max(m.defaultDevicesWidth()+m.widen, minPaneWidth), m.width-minPaneWidth)
}

// resize moves the line between the devices and the logs by step columns.
func (m *Model) resize(step int) {
	if m.width < splitWidth || m.zoomed {
		m.setNote("Only one pane is shown. Press z to show both, or widen the terminal", false)
		return
	}
	m.widen += step
	// Kept within what the screen allows, so moving back takes effect at once.
	m.widen = m.devicesWidth() - m.defaultDevicesWidth()
}

// View implements tea.Model.
func (m *Model) View() tea.View {
	view := tea.NewView(m.render())
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	view.WindowTitle = "lazymobile"
	return view
}

func (m *Model) render() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	if m.width < minWidth || m.height < minHeight {
		return fmt.Sprintf("lazymobile needs at least %d columns and %d rows.", minWidth, minHeight)
	}
	l := m.layout()

	body := m.panes(l)
	if m.modal != nil {
		body = overlay(body, m.modal.render(m.width, l.bodyHeight), m.width)
	}

	note := " " + m.note
	if m.noteErr {
		note = red.Render(fit(note, m.width))
	}
	lines := append(body, fit(note, m.width), fit(" "+m.hints(), m.width))
	return strings.Join(lines, "\n")
}

// panes draws the dashboard's panes side by side.
func (m *Model) panes(l layout) []string {
	var left, right []string
	if l.devicesWidth > 0 {
		if top := l.bodyHeight - l.buildsHeight; top > 0 {
			left = box("Devices", m.deviceLines(l.devicesWidth-2, top-2), l.devicesWidth, top, m.focus == paneDevices)
		}
		if l.buildsHeight > 0 {
			left = append(left, box(m.buildsTitle(), m.buildLines(l.devicesWidth-2, l.buildsHeight-2), l.devicesWidth, l.buildsHeight, m.focus == paneBuilds)...)
		}
	}
	right = m.logLines(l)
	body := make([]string, l.bodyHeight)
	for i := range body {
		if i < len(left) {
			body[i] = left[i]
		}
		if i < len(right) {
			body[i] += right[i]
		}
	}
	return body
}

// logLines draws the log panes, wherever the layout put them. Each screen row is made of the rows
// of the panes it crosses, left to right.
func (m *Model) logLines(l layout) []string {
	drawn := make([][]string, len(l.logs))
	visible := false
	for i, b := range l.logs {
		if b.width == 0 {
			continue
		}
		visible = true
		v := m.logPanes[i]
		focused := m.focus == paneLogs && m.logIdx == i
		drawn[i] = box(v.title(b.width, m.position(i)), v.lines(b.width-2, b.height-2, focused), b.width, b.height, focused)
	}
	if !visible {
		return nil
	}
	rows := make([]string, l.bodyHeight)
	for y := range rows {
		var parts []struct {
			x    int
			line string
		}
		for i, b := range l.logs {
			if b.width > 0 && y >= b.y && y < b.y+b.height && y-b.y < len(drawn[i]) {
				parts = append(parts, struct {
					x    int
					line string
				}{b.x, drawn[i][y-b.y]})
			}
		}
		sort.Slice(parts, func(i, j int) bool { return parts[i].x < parts[j].x })
		for _, p := range parts {
			rows[y] += p.line
		}
	}
	return rows
}

// hints is the hint line as drawn: each key in the colour the keys menu uses, its label dim.
func (m *Model) hints() string {
	if m.modal != nil {
		return ""
	}
	parts := strings.Split(m.hintsLine(m.width), hintGap)
	for i, part := range parts {
		key, label, _ := strings.Cut(part, " ")
		parts[i] = cyan.Render(key) + " " + dim.Render(label)
	}
	return strings.Join(parts, hintGap)
}

// deviceLines draws the device list for a pane with this much room inside its border.
func (m *Model) deviceLines(width, height int) []string {
	if !m.loaded {
		return []string{"", dim.Render(" Looking for devices…")}
	}
	var lines []string
	cursorLine := 0
	index := 0
	// rowOf is the line of each device, for telling which one a click is on.
	var rowOf []int
	for _, g := range groups {
		var members []device.Device
		for _, d := range m.devices {
			if g.has(d) {
				members = append(members, d)
			}
		}
		if len(members) == 0 {
			continue
		}
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, " "+bold.Render(g.title))
		for _, d := range members {
			selected := index == m.cursor
			if selected {
				cursorLine = len(lines)
			}
			rowOf = append(rowOf, len(lines))
			lines = append(lines, m.deviceRow(d, width, selected))
			if selected && d.Detail != "" {
				for _, part := range strings.Split(ansi.Wordwrap(d.Detail, max(width-6, 10), " "), "\n") {
					lines = append(lines, "     "+yellow.Render(part))
				}
			}
			index++
		}
	}

	if len(m.devices) == 0 && len(m.listErrs) == 0 {
		lines = append(lines, "", " No devices yet.", "", dim.Render(" Press n to create a simulator or emulator,"), dim.Render(" or connect a phone with a cable."))
	}
	for _, err := range m.listErrs {
		lines = append(lines, "")
		for i, part := range strings.Split(ansi.Wordwrap(firstLine(err.Error()), max(width-4, 10), " "), "\n") {
			mark := "   "
			if i == 0 {
				mark = " ! "
			}
			lines = append(lines, red.Render(mark+part))
		}
		if hint := errorHint(err); hint != "" {
			for _, part := range strings.Split(ansi.Wordwrap(hint, max(width-4, 10), " "), "\n") {
				lines = append(lines, dim.Render("   "+part))
			}
		}
	}

	// Scroll so the selected device stays in view.
	first := 0
	if len(lines) > height && height > 0 {
		first = min(max(cursorLine-height/2, 0), len(lines)-height)
		lines = lines[first : first+height]
	}
	m.deviceRows = owners(rowOf, first, len(lines))
	// The selected device's details go at the bottom, when there is room for them.
	if d, ok := m.selected(); ok {
		details := deviceDetails(d, width)
		if len(lines)+1+len(details) <= height {
			for len(lines)+len(details) < height {
				lines = append(lines, "")
			}
			lines = append(lines, details...)
		}
	}
	return lines
}

// deviceDetails describes a device in a few rows, under a rule.
func deviceDetails(d device.Device, width int) []string {
	kind := map[device.Kind]string{device.Simulator: "simulator", device.Emulator: "emulator", device.Physical: "device"}[d.Kind]
	platform := map[device.Platform]string{device.IOS: "iOS", device.Android: "Android"}[d.Platform]
	rows := [][2]string{{"Id", d.TargetID() + "  (y copies)"}}
	if d.TargetID() != d.ID {
		rows = append(rows, [2]string{"AVD", d.ID})
	}
	about := platform + " " + kind
	if d.OSVersion != "" {
		about += ", " + d.OSLabel()
	}
	rows = append(rows, [2]string{"Kind", about})
	if d.Model != "" {
		rows = append(rows, [2]string{"Model", d.Model})
	}
	if d.Transport != "" && d.Kind == device.Physical {
		rows = append(rows, [2]string{"Via", d.Transport})
	}
	lines := []string{" " + dim.Render(strings.Repeat("─", max(width-2, 0)))}
	for _, row := range rows {
		lines = append(lines, " "+dim.Render(fit(row[0], 6))+" "+fit(row[1], max(width-9, 1)))
	}
	return lines
}

func (m *Model) deviceRow(d device.Device, width int, selected bool) string {
	state := strings.ReplaceAll(string(d.State), "_", " ")
	doing := m.doing(d.Key())
	busy := doing != ""
	if busy {
		state = strings.ToLower(doing)
	}
	if d.Kind == device.Physical && d.Transport != "" && d.Ready() {
		state = d.Transport
	}

	const stateWidth, osWidth = 13, 11
	nameWidth := width - 4 - stateWidth - 1
	showOS := width >= 44
	if showOS {
		nameWidth -= osWidth + 1
	}
	text := fit(d.Name, max(nameWidth, 4))
	if showOS {
		text += " " + fit(d.OSLabel(), osWidth)
	}
	text += " " + fit(state, stateWidth)

	if selected && m.focus == paneDevices {
		return " " + stateMark(d, busy) + " " + chosen.Render(text)
	}
	if selected {
		return " " + stateMark(d, busy) + " " + bold.Render(text)
	}
	return " " + stateMark(d, busy) + " " + text
}

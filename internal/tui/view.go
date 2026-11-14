package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/0xrnp/lazymobile/internal/device"
)

const helpText = `Devices
  j k, arrows   move            r   refresh
  b   boot                      B   cold boot (emulator)
  s   shut down                 l   logs (also enter)
  e   erase, asks first         d   delete, asks first
  n   new simulator or emulator y   copy the id
  w   Android phone over Wi-Fi  x   disconnect a Wi-Fi phone

Logs
  j k, arrows   scroll          G   follow the newest line
  g   first line                h l scroll sideways, 0 resets
  /   filter by text            L   minimum level
  p   pause or resume           c   clear
  S   save to a file            x   stop the stream
  r   restart the stream

Builds (inside an Expo project)
  enter load, then details      r   refresh
  i   install on a device       o   open the build page
  n   new build, asks first     x   cancel a build, asks first

Everywhere
  tab   next pane               ?   this help
  + -   widen or narrow devices z   zoom the focused pane
  q     quit

Emulators and simulators keep running after you quit.`

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
	// logRows is how many log lines fit.
	logRows int
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
	case m.focus == paneLogs:
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
	// Two border rows and one status row are not log lines.
	l.logRows = max(l.bodyHeight-3, 0)
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
	lines := append(body, fit(note, m.width), dim.Render(fit(" "+m.hints(), m.width)))
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
	if l.logsWidth > 0 {
		right = box(m.log.title(), m.log.lines(l.logsWidth-2, l.bodyHeight-2), l.logsWidth, l.bodyHeight, m.focus == paneLogs)
	}
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

func (m *Model) hints() string {
	switch {
	case m.modal != nil:
		return ""
	case m.focus == paneLogs && m.log.opened:
		return "/ filter  L level  p pause  c clear  S save  x stop  r restart  G follow  tab devices  ? help  q quit"
	case m.focus == paneLogs:
		return "tab devices  ? help  q quit"
	case m.focus == paneBuilds && !m.build.asked:
		return "enter load builds  tab next pane  ? help  q quit"
	case m.focus == paneBuilds:
		return "enter details  i install  o open page  n new build  x cancel  r refresh  tab next pane  ? help  q quit"
	}
	return "b boot  s shut down  l logs  n new  e erase  d delete  y copy id  w wi-fi  r refresh  ? help  q quit"
}

// deviceLines draws the device list for a pane with this much room inside its border.
func (m *Model) deviceLines(width, height int) []string {
	if !m.loaded {
		return []string{"", dim.Render(" Looking for devices…")}
	}
	var lines []string
	cursorLine := 0
	index := 0
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
	if len(lines) > height && height > 0 {
		first := min(max(cursorLine-height/2, 0), len(lines)-height)
		lines = lines[first : first+height]
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

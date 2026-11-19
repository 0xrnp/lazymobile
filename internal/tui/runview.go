package tui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/logs"
	"github.com/0xrnp/lazymobile/internal/vterm"
)

// The run view is what `lazymobile run --here` opens: the project's own tool, such as flutter run,
// in a pane on the left, and the log of the app it runs on the right. ctrl+] switches to the full
// dashboard and back, and the tool keeps running in either. While the tool's pane has the keys,
// every key goes to the tool, so its own keys (r for hot reload) work as in a terminal of its own.

// RunSpec is the tool the dashboard runs, and the device it runs the app on.
type RunSpec struct {
	Dir  string
	Argv []string
	// Title names the tool in the pane's border, such as "flutter run".
	Title  string
	Device device.Device
	// Start runs the tool on a terminal of cols by rows. Nil runs it in a pseudo-terminal; tests
	// give a fake.
	Start func(dir string, argv []string, cols, rows int) (ToolSession, error)
}

// ToolSession is a tool running on a terminal the dashboard draws.
type ToolSession interface {
	Resize(cols, rows int)
	SendKey(k uv.KeyEvent)
	Paste(text string)
	// Lines is the screen, one string per row, scrolled back by back rows.
	Lines(back int) []string
	ScrollbackLen() int
	Cursor() (x, y int, visible bool)
	Changed() <-chan struct{}
	Done() <-chan struct{}
	Err() error
	// Stop interrupts the tool and kills it a few seconds later if it has not ended. Wait waits.
	Stop()
	Wait()
}

func startInTerminal(dir string, argv []string, cols, rows int) (ToolSession, error) {
	return vterm.Start(dir, argv, os.Environ(), cols, rows)
}

// toolRun is the state of the run view.
type toolRun struct {
	spec    RunSpec
	session ToolSession
	// id counts the times the tool was started, so a message from an earlier one is dropped.
	id int
	// shown is whether the run view is on the screen, rather than the dashboard.
	shown bool
	// onTool is whether keys go to the tool, rather than to its log.
	onTool bool
	ended  bool
	// endErr is how the tool ended, and startErr why it could not start.
	endErr, startErr error
	// back is how many rows the tool's pane is scrolled back.
	back int
	// cols and rows are the size the tool was last told.
	cols, rows int
}

type (
	toolChangedMsg struct{ id int }
	toolEndMsg     struct {
		id  int
		err error
	}
)

// runKeysOwn are the keys the run view keeps while the tool has the keys. Every other key goes to
// the tool.
var runKeysOwn = map[string]bool{
	"ctrl+]": true, "ctrl+o": true,
	"shift+up": true, "shift+down": true, "shift+pgup": true, "shift+pgdown": true,
}

// attachRun makes the dashboard open on the run view of spec. The tool starts once the screen's
// size is known.
func (m *Model) attachRun(spec RunSpec) {
	m.run = &toolRun{spec: spec, shown: true, onTool: true}
	// The run is followed at the first look, not a tick later.
	m.followTicks = followEvery - 1
}

// runShown reports whether the run view is on the screen.
func (m *Model) runShown() bool { return m.run != nil && m.run.shown }

// toolRunning reports whether the tool is running now.
func (m *Model) toolRunning() bool {
	return m.run != nil && m.run.session != nil && !m.run.ended
}

// runTitle is the tool's short name, for notes and hints.
func (m *Model) runTitle() string {
	if m.run == nil {
		return ""
	}
	if m.run.spec.Title != "" {
		return m.run.spec.Title
	}
	if len(m.run.spec.Argv) > 0 {
		return m.run.spec.Argv[0]
	}
	return "the tool"
}

// runPaneWidths are the widths of the tool's pane and the log's pane in the run view. Below the
// split width only one shows, the one with the keys.
func (m *Model) runPaneWidths() (tool, log int) {
	if m.width >= splitWidth {
		log = max(m.width*2/5, minPaneWidth)
		return m.width - log, log
	}
	if m.run != nil && !m.run.onTool {
		return 0, m.width
	}
	return m.width, 0
}

// toolSize is the size of the tool's terminal: the inside of its pane in the run view. It is the
// same while the dashboard or the log is shown, so switching never makes the tool redraw.
func (m *Model) toolSize() (cols, rows int) {
	width := m.width
	if m.width >= splitWidth {
		width, _ = m.runPaneWidths()
	}
	return max(width-2, 20), max(m.height-4, 5)
}

// runLayout is the layout of the run view: the tool on the left, the run's log on the right.
func (m *Model) runLayout() layout {
	l := layout{bodyHeight: max(m.height-2, 0), logs: make([]logBox, len(m.logPanes))}
	toolWidth, logWidth := m.runPaneWidths()
	l.tool = logBox{width: toolWidth, height: l.bodyHeight}
	l.runLog = logBox{x: toolWidth, width: logWidth, height: l.bodyHeight}
	if slot := m.runLogSlot(); slot >= 0 && logWidth > 0 {
		l.logs[slot] = l.runLog
	}
	return l
}

// runLogSlot is the log pane the run view shows: a log of the run's device, the focused one or the
// one last focused when there are several, or -1 when none is open.
func (m *Model) runLogSlot() int {
	if m.run == nil {
		return -1
	}
	key := m.run.spec.Device.Key()
	found := -1
	for i, v := range m.logPanes {
		if !v.opened || v.spec.Device.Key() != key {
			continue
		}
		if m.focus == paneLogs && i == m.logIdx {
			return i
		}
		if found < 0 || i == m.lastLog {
			found = i
		}
	}
	return found
}

// startTool starts the tool at the size of its pane. A tool that cannot start leaves the reason in
// its pane, and enter tries again.
func (m *Model) startTool() tea.Cmd {
	r := m.run
	cols, rows := m.toolSize()
	start := r.spec.Start
	if start == nil {
		start = startInTerminal
	}
	r.id++
	r.back, r.endErr, r.startErr = 0, nil, nil
	session, err := start(r.spec.Dir, r.spec.Argv, cols, rows)
	if err != nil {
		r.session, r.ended, r.startErr = nil, true, err
		m.setNote(m.runTitle()+" could not start: "+firstLine(err.Error()), true)
		return nil
	}
	r.session, r.ended, r.cols, r.rows = session, false, cols, rows
	return m.listenTool()
}

// listenTool waits for the tool's screen to change, or for the tool to end.
func (m *Model) listenTool() tea.Cmd {
	session, id := m.run.session, m.run.id
	return func() tea.Msg {
		select {
		case <-session.Changed():
			return toolChangedMsg{id}
		case <-session.Done():
			return toolEndMsg{id, session.Err()}
		}
	}
}

// fitTool starts the tool once the screen's size is known, and tells it the size of its pane after
// the screen changed size.
func (m *Model) fitTool() tea.Cmd {
	r := m.run
	if r == nil || m.width == 0 || m.height == 0 {
		return nil
	}
	if r.session == nil && !r.ended {
		return m.startTool()
	}
	if cols, rows := m.toolSize(); r.session != nil && !r.ended && (cols != r.cols || rows != r.rows) {
		r.session.Resize(cols, rows)
		r.cols, r.rows = cols, rows
	}
	return nil
}

// toolEnded records how the tool ended. Its last screen stays in the pane.
func (m *Model) toolEnded(err error) {
	r := m.run
	r.ended, r.endErr = true, err
	if err != nil {
		m.setNote(m.runTitle()+" "+exitText(err)+". enter in its pane runs it again", true)
		return
	}
	m.setNote(m.runTitle()+" ended. enter in its pane runs it again", false)
}

// exitText says how a tool ended: "exited with code 1", or the error.
func exitText(err error) string {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if code := exit.ExitCode(); code >= 0 {
			return fmt.Sprintf("exited with code %d", code)
		}
		return "was stopped"
	}
	return firstLine(err.Error())
}

// toggleRun switches between the run view and the dashboard.
func (m *Model) toggleRun() {
	r := m.run
	r.shown = !r.shown
	if !r.shown {
		m.setNote("The dashboard. "+m.runTitle()+" keeps running: ctrl+] goes back to it", false)
		return
	}
	m.setNote("", false)
	if !r.onTool {
		m.focusRunLog()
	}
}

// focusRunLog gives the keys to the run's log.
func (m *Model) focusRunLog() {
	m.run.onTool = false
	if slot := m.runLogSlot(); slot >= 0 {
		m.focusLog(slot)
	}
}

// scrollTool moves the tool's pane back (up) by rows, or forward with a negative number.
func (m *Model) scrollTool(rows int) {
	r := m.run
	if r.session == nil {
		return
	}
	r.back = min(max(r.back+rows, 0), r.session.ScrollbackLen())
}

// runViewKey handles a key in the run view. While the tool has the keys and runs, a key goes to it,
// except the few the run view keeps.
func (m *Model) runViewKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	if m.run.onTool && m.toolRunning() && !runKeysOwn[key] {
		// Typing goes back to the newest output, as in a terminal.
		m.run.back = 0
		m.run.session.SendKey(uv.KeyPressEvent(msg))
		return nil
	}
	return m.runViewKeyString(key)
}

// runViewKeyString does what a key does in the run view when it is not the tool's. The keys menu runs a
// run action by sending its key here.
func (m *Model) runViewKeyString(key string) tea.Cmd {
	r := m.run
	_, rows := m.toolSize()
	switch key {
	case "ctrl+]":
		m.toggleRun()
		return nil
	case "ctrl+o":
		if r.onTool {
			m.focusRunLog()
		} else {
			r.onTool = true
		}
		return nil
	case "shift+up":
		m.scrollTool(1)
		return nil
	case "shift+down":
		m.scrollTool(-1)
		return nil
	case "shift+pgup":
		m.scrollTool(rows / 2)
		return nil
	case "shift+pgdown":
		m.scrollTool(-rows / 2)
		return nil
	}
	if r.onTool {
		// The tool has ended, or could not start.
		switch key {
		case "enter":
			if r.ended {
				return m.startTool()
			}
		case "q", "ctrl+c":
			return m.quit()
		case "?":
			m.modal = m.keysModal()
		}
		return nil
	}
	switch key {
	case "esc":
		r.onTool = true
		return nil
	case "q", "ctrl+c":
		return m.quit()
	case "?":
		m.modal = m.keysModal()
		return nil
	}
	slot := m.runLogSlot()
	if slot < 0 {
		switch key {
		case "l":
			return m.openRunLog()
		}
		return nil
	}
	m.focusLog(slot)
	return m.logKey(key)
}

// openRunLog opens a log of the run's device: of the folder's app when it is known, and otherwise
// it asks which app, as l on a device does.
func (m *Model) openRunLog() tea.Cmd {
	d, ok := m.runDevice()
	if !ok {
		m.setNote(m.run.spec.Device.Name+" is not in the device list yet", true)
		return nil
	}
	if !d.Ready() {
		m.setNote(d.Name+" is not running", true)
		return nil
	}
	if app := m.folderApp(d.Platform); app != "" && !(d.Platform == device.IOS && d.Kind == device.Physical) {
		mode := logs.System
		if d.Platform == device.Android {
			mode = logs.Logcat
		}
		return m.startLogs(m.slotFor(newLog), logs.Spec{Device: d, Mode: mode, App: app}, "")
	}
	return m.openLogs(d, newLog)
}

// runDevice is the run's device as the device list has it now, and selects it.
func (m *Model) runDevice() (device.Device, bool) {
	key := m.run.spec.Device.Key()
	for i, d := range m.devices {
		if d.Key() == key {
			m.cursor = i
			return d, true
		}
	}
	return device.Device{}, false
}

// toolPane draws the tool's pane.
func (m *Model) toolPane(width, height int) []string {
	r := m.run
	title := m.runTitle() + " · " + r.spec.Device.Name
	inner := max(width-2, 0)
	var lines []string
	switch {
	case r.startErr != nil:
		title += " · " + red.Render("could not start")
		lines = append(lines, "", " "+red.Render(m.runTitle()+" could not start:"))
		for _, part := range strings.Split(ansi.Wordwrap(r.startErr.Error(), max(inner-2, 10), " "), "\n") {
			lines = append(lines, " "+part)
		}
		lines = append(lines, "", dim.Render(" enter tries again   ctrl+] the dashboard   q quits"))
	case r.session == nil:
		lines = []string{"", dim.Render(" Starting " + m.runTitle() + "…")}
	default:
		lines = r.session.Lines(r.back)
		switch {
		case r.ended && r.endErr != nil:
			title += " · " + red.Render(exitText(r.endErr))
		case r.ended:
			title += " · ended"
		case r.back > 0:
			title += " · " + yellow.Render(fmt.Sprintf("%d rows back", r.back))
		default:
			title += " · " + green.Render("running")
		}
	}
	return box(title, lines, width, height, r.onTool)
}

// runLogPlaceholder fills the log's pane while no log of the run's device is open.
func (m *Model) runLogPlaceholder(b logBox) []string {
	r := m.run
	lines := []string{""}
	say := func(text string) {
		for _, part := range strings.Split(ansi.Wordwrap(text, max(b.width-4, 10), " "), "\n") {
			lines = append(lines, " "+part)
		}
	}
	say("The log of the app on " + r.spec.Device.Name + " shows here.")
	lines = append(lines, "")
	if r.onTool {
		say("ctrl+o, then l, to pick the app. It opens by itself next time.")
	} else {
		say("Press l to pick the app. It opens by itself next time.")
	}
	return box("Log · "+r.spec.Device.Name, lines, b.width, b.height, !r.onTool)
}

// runCursor is where the tool's cursor goes on the screen, when it is shown.
func (m *Model) runCursor(l layout) (*tea.Cursor, bool) {
	r := m.run
	if !m.runShown() || m.modal != nil || !r.onTool || !m.toolRunning() || r.back > 0 || l.tool.width == 0 {
		return nil, false
	}
	x, y, visible := r.session.Cursor()
	if !visible || x >= l.tool.width-2 || y >= l.tool.height-2 {
		return nil, false
	}
	return tea.NewCursor(l.tool.x+1+x, l.tool.y+1+y), true
}

// runClick gives the keys to the pane clicked in the run view.
func (m *Model) runClick(x, y int) bool {
	l := m.layout()
	switch {
	case l.tool.contains(x, y):
		m.run.onTool = true
	case l.runLog.contains(x, y):
		m.focusRunLog()
	default:
		return false
	}
	return true
}

// runWheel scrolls the tool's pane when the wheel turns over it.
func (m *Model) runWheel(msg tea.MouseWheelMsg) bool {
	if !m.layout().tool.contains(msg.X, msg.Y) {
		return false
	}
	switch msg.Button {
	case tea.MouseWheelUp:
		m.scrollTool(3)
	case tea.MouseWheelDown:
		m.scrollTool(-3)
	}
	return true
}

// stopTool stops the tool and waits for it, before lazymobile quits, so no dev server is left
// running.
func (m *Model) stopTool() {
	if m.run == nil || m.run.session == nil {
		return
	}
	m.run.session.Stop()
	m.run.session.Wait()
}

// runFooterKeys are the hints of the run view.
func (m *Model) runFooterKeys() []string {
	r := m.run
	switch {
	case r.onTool && m.toolRunning():
		return []string{"run_focus", "run_switch", "run_scroll"}
	case r.onTool:
		return []string{"run_again", "run_focus", "run_switch", "run_quit"}
	case m.runLogSlot() < 0:
		return []string{"run_pick", "run_back", "run_switch", "run_quit"}
	}
	return []string{"run_back", "run_switch", "log_filter", "log_level", "log_pause", "log_copy", "log_line", "log_wrap", "log_clear", "run_help", "run_quit"}
}

func needsRun(m *Model) string {
	if m.run == nil {
		return "No tool runs here: start one with lazymobile run --here"
	}
	return ""
}

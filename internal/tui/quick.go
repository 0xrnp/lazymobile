package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/mobile"
)

// aiLines is how many of the newest log lines "Copy for AI" copies.
const aiLines = 200

// recording is a screen recording that is running.
type recording struct {
	device  device.Device
	name    string
	started time.Time
	stop    context.CancelFunc
	// done is closed once the video is saved, or failed.
	done chan struct{}
}

type recordedMsg struct {
	rec  *recording
	path string
	err  error
}

// quickAction is an item of the quick menu: its key, and what it does to a device.
type quickAction struct {
	key   string
	label func(m *Model, d device.Device) string
	do    func(m *Model, d device.Device) tea.Cmd
}

var quickActions = []quickAction{
	{"s", func(*Model, device.Device) string { return "Screenshot: save it here and copy it" },
		(*Model).screenshotAndCopy},
	{"r", func(m *Model, d device.Device) string {
		if m.recording != nil {
			return "Stop recording " + m.recording.device.Name + ", save and copy the video"
		}
		return "Record the screen"
	}, (*Model).toggleRecording},
	{"c", func(*Model, device.Device) string {
		return fmt.Sprintf("Copy the app's log for AI (the last %d lines)", aiLines)
	}, (*Model).copyForAI},
	{"b", func(*Model, device.Device) string { return "Save a bug report: screenshot, log, crash, versions" },
		(*Model).quickReport},
	{"o", func(*Model, device.Device) string { return "The app: launch, restart, crash report, permissions" },
		(*Model).pickAppAction},
	{"t", func(*Model, device.Device) string { return "Open a link in the app" }, (*Model).openLink},
	{"m", func(*Model, device.Device) string { return "Switch dark and light mode" }, (*Model).toggleAppearance},
}

// quickDevice is the device the quick menu acts on: the run's in the run view, the selected one on
// the dashboard.
func (m *Model) quickDevice() (device.Device, bool) {
	if m.runShown() {
		d, ok := m.runDevice()
		if !ok {
			m.setNote(m.run.spec.Device.Name+" is not in the device list yet", true)
		}
		return d, ok
	}
	// A log that has the focus is of the device it is of, whichever device the cursor is on.
	if _, v, ok := m.focusedLog(); ok && v.opened {
		key := v.spec.Device.Key()
		for _, d := range m.devices {
			if d.Key() == key {
				return d, true
			}
		}
		return v.spec.Device, true
	}
	d, ok := m.selected()
	if !ok {
		m.setNote("Select a device first", true)
	}
	return d, ok
}

// quickMenu opens the quick menu over the run view or the dashboard. Each item has a letter, so
// ctrl+p then s takes a screenshot.
func (m *Model) quickMenu() tea.Cmd {
	d, ok := m.quickDevice()
	if !ok {
		return nil
	}
	items := make([]string, len(quickActions))
	for i, a := range quickActions {
		items[i] = a.key + "  " + a.label(m, d)
	}
	m.modal = &modal{
		kind: modalChoice, title: "Quick actions · " + d.Name, items: items,
		onChoose: func(m *Model, index int) tea.Cmd { return quickActions[index].do(m, d) },
		onKey: func(m *Model, key string, _ int) tea.Cmd {
			for _, a := range quickActions {
				if a.key == key {
					m.modal = nil
					return a.do(m, d)
				}
			}
			return nil
		},
		extraHint: "or its letter",
	}
	return nil
}

// screenshotAndCopy saves a screenshot in the folder and copies the picture, to paste into a chat.
func (m *Model) screenshotAndCopy(d device.Device) tea.Cmd {
	if problem := screenFeature(d); problem != "" {
		m.setNote(problem, true)
		return nil
	}
	name := mobile.ScreenshotName(d, m.now())
	backend := m.backend
	return m.act(d, "Taking a screenshot of", "Copied a screenshot, saved as "+name+", of", nil, func(ctx context.Context) error {
		path, err := backend.Screenshot(ctx, d, name)
		if err != nil {
			return err
		}
		if err := backend.CopyFile(ctx, path); err != nil {
			return fmt.Errorf("saved %s, but could not copy it: %w", name, err)
		}
		return nil
	})
}

// toggleRecording starts filming d's screen, or stops the recording that runs.
func (m *Model) toggleRecording(d device.Device) tea.Cmd {
	if rec := m.recording; rec != nil {
		rec.stop()
		m.setNote("Saving the recording of "+rec.device.Name+"…", false)
		return nil
	}
	if problem := screenFeature(d); problem != "" {
		m.setNote(problem, true)
		return nil
	}
	ctx, cancel := context.WithCancel(m.ctx)
	rec := &recording{device: d, name: mobile.RecordingName(d, m.now()), started: m.now(), stop: cancel, done: make(chan struct{})}
	m.recording = rec
	m.setNote("Recording "+d.Name+". ctrl+p, then r, stops it", false)
	backend := m.backend
	return func() tea.Msg {
		path, err := backend.Record(ctx, d, rec.name, 0, nil)
		cancel()
		close(rec.done)
		return recordedMsg{rec, path, err}
	}
}

// recorded copies the video of a recording that ended.
func (m *Model) recorded(msg recordedMsg) tea.Cmd {
	if m.recording == msg.rec {
		m.recording = nil
	}
	name := msg.rec.device.Name
	if msg.err != nil {
		m.fail("Could not record "+name, msg.err)
		return nil
	}
	m.setNote("Saved "+msg.rec.name+". Copying it…", false)
	ctx, backend := m.ctx, m.backend
	return func() tea.Msg {
		err := backend.CopyFile(ctx, msg.path)
		return applyMsg(func(m *Model) tea.Cmd {
			if err != nil {
				m.setNote("Saved "+msg.rec.name+", but could not copy it: "+firstLine(err.Error()), true)
				return nil
			}
			m.setNote("Copied the video of "+name+", saved as "+msg.rec.name+": paste it to attach it", false)
			return nil
		})
	}
}

// recordingMark is shown before the note while a recording runs.
func (m *Model) recordingMark() string {
	if m.recording == nil {
		return ""
	}
	took := m.now().Sub(m.recording.started).Round(time.Second)
	return red.Render(fmt.Sprintf("● REC %d:%02d", int(took.Minutes()), int(took.Seconds())%60)) + "  "
}

// logOf is the open log pane of the device with this key: the focused one, or the one last focused
// when there are several, or -1 when none is open.
func (m *Model) logOf(key string) int {
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

// copyForAI copies the newest lines of d's log, with what they are from, to paste into a chat and
// ask what went wrong.
func (m *Model) copyForAI(d device.Device) tea.Cmd {
	slot := m.logOf(d.Key())
	if slot < 0 {
		m.setNote("No log of "+d.Name+" is open. Open the app's log first", true)
		return nil
	}
	v := m.logPanes[slot]
	// What the pane shows, with its filter and level, as the person sees it.
	switch {
	case len(v.all) == 0:
		m.setNote("The log of "+d.Name+" is empty so far", true)
		return nil
	case len(v.shown) == 0:
		m.setNote("The filter of the log hides every line: clear it, or lower its level, first", true)
		return nil
	}
	lines := v.shown[max(len(v.shown)-aiLines, 0):]
	what := "the log"
	if v.spec.App != "" {
		what += " of " + v.spec.App
	}
	about := d.Name
	if label := d.OSLabel(); label != "" {
		about += ", " + label
	}
	text := fmt.Sprintf("The last %d lines of %s on %s (%s), from lazymobile:\n\n%s\n", len(lines), what, d.Name, about, entryText(lines))
	done := fmt.Sprintf("Copied the last %d lines of %s: paste them into a chat", len(lines), what)
	backend := m.backend
	return m.background("Copying "+what+"…", func(ctx context.Context) applyMsg {
		err := backend.CopyText(ctx, text)
		return applyMsg(func(m *Model) tea.Cmd {
			if err != nil {
				// No clipboard tool here, as over SSH: the terminal is asked to copy it instead, which
				// not every terminal does.
				m.setNote("No clipboard tool here ("+firstLine(err.Error())+"), so the terminal was asked to copy "+what+", which not every terminal does", false)
				return tea.SetClipboard(text)
			}
			m.setNote(done, false)
			return nil
		})
	})
}

// quickReport saves a bug report of the app whose log is open, or of the folder's app, and asks
// which app otherwise.
func (m *Model) quickReport(d device.Device) tea.Cmd {
	if slot := m.logOf(d.Key()); slot >= 0 && m.logPanes[slot].spec.App != "" {
		return m.saveReport(d, m.logPanes[slot].spec.App)
	}
	if app := m.folderApp(d.Platform); app != "" {
		return m.saveReport(d, app)
	}
	m.setNote("Which app? Choose it, then Save a bug report", false)
	return m.pickAppAction(d)
}

// stopRecordingAndWait stops a recording and waits until its video is saved, before lazymobile
// quits. It returns nil when nothing records.
func (m *Model) stopRecordingAndWait() func() {
	rec := m.recording
	if rec == nil {
		return nil
	}
	rec.stop()
	return func() { <-rec.done }
}

// quitWhat says what quitting stops, for the question asked before it.
func (m *Model) quitWhat() (title, body string) {
	var stops []string
	if m.toolRunning() {
		stops = append(stops, m.runTitle())
	}
	if m.recording != nil {
		stops = append(stops, "the recording")
	}
	title = "Stop " + strings.Join(stops, " and ") + " and quit?"
	if len(stops) > 1 {
		title = "Stop " + strings.Join(stops, " and ") + ", then quit?"
	}
	if m.toolRunning() {
		body = m.runTitle() + " is running. Quitting stops it: ctrl+c first, then a kill if it has not ended after a few seconds. ctrl+] goes back to it instead."
	}
	if m.recording != nil {
		body = strings.TrimSpace(body + " The recording of " + m.recording.device.Name + " is saved before lazymobile quits.")
	}
	return title, body
}

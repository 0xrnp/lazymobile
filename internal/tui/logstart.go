package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/logs"
	"github.com/0xrnp/lazymobile/internal/state"
)

// openLogs asks which log, and which app, then opens it in the log pane slot.
func (m *Model) openLogs(d device.Device, slot int) tea.Cmd {
	modes := logs.Modes(d)
	switch len(modes) {
	case 0:
		m.setNote(d.Name+" is not running. Boot or connect it to see its logs", true)
		return nil
	case 1:
		return m.pickApp(d, modes[0], slot)
	}
	labels := make([]string, len(modes))
	for i, mode := range modes {
		labels[i] = mode.Label
	}
	m.modal = &modal{
		kind: modalChoice, title: "Which log of " + d.Name + "?", items: labels,
		onChoose: func(m *Model, index int) tea.Cmd {
			return m.pickApp(d, modes[index], slot)
		},
	}
	return nil
}

// startLogs opens a log in the log pane slot, replacing what it showed, or in a pane of its own for
// newLog. version is the installed version of spec.App when the caller has just been told it, such
// as by the app list. Without one, the device is asked.
func (m *Model) startLogs(slot int, spec logs.Spec, version string) tea.Cmd {
	if m.note == waitingNote {
		m.setNote("", false)
	}
	slot = m.slotFor(slot)
	v := m.logPanes[slot]
	if v.session != nil {
		v.session.Stop()
	}
	m.logIDs++
	id := m.logIDs
	level := v.level
	if !v.levelChosen {
		level = defaultLevel(spec)
	}
	*v = logView{id: id, opened: true, spec: spec, follow: true, level: level, levelChosen: v.levelChosen, filter: v.filter, wrap: v.wrap}
	send := m.send
	session := m.backend.StartLogs(m.ctx, spec, logs.Handlers{
		Lines:  func(lines []logs.Line) { send(logLinesMsg{id, lines}) },
		Status: func(text string) { send(logStatusMsg{id, text}) },
	})
	v.session = session
	v.version = version
	m.focusLog(slot)
	if spec.App != "" {
		m.lastApp[spec.Device.Platform] = spec.App
		_ = m.memory.Store.Update(func(f *state.File) { f.RememberApp(spec.Device.Platform, spec.App) })
	}
	m.saveView()
	ended := func() tea.Msg { return logEndMsg{id, session.Wait()} }
	// Every start asks again, because a new build may have been installed since the last one, and
	// a log reopened from a past run, or typed by hand, has no version yet.
	if spec.App != "" && version == "" {
		return tea.Batch(ended, m.lookupVersion(id, spec))
	}
	return ended
}

// lookupVersion asks the device which version of the log's app is installed, and shows it on the
// log that is open when the answer comes. A device that cannot say, or cannot be reached, leaves
// the heading as it is.
func (m *Model) lookupVersion(id int, spec logs.Spec) tea.Cmd {
	ctx, backend := m.ctx, m.backend
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		app, err := backend.AppInfo(ctx, spec.Device, spec.App)
		return applyMsg(func(m *Model) tea.Cmd {
			if v := m.logByID(id); v != nil && err == nil {
				v.version = app.VersionLabel()
			}
			return nil
		})
	}
}

// defaultLevel is the level a stream starts at until one is chosen with L. An iOS simulator's
// system log is mostly debug messages from the system frameworks, so it starts at info.
func defaultLevel(spec logs.Spec) logs.Level {
	if spec.Device.Platform == device.IOS && spec.Mode == logs.System {
		return logs.Info
	}
	return logs.NoLevel
}

func entryText(lines []logs.Line) string {
	texts := make([]string, len(lines))
	for i, line := range lines {
		texts[i] = line.Text
	}
	return strings.Join(texts, "\n")
}

func (m *Model) saveLog(v *logView) tea.Cmd {
	name := strings.Map(func(r rune) rune {
		if r == ' ' || r == '/' {
			return '-'
		}
		return r
	}, v.spec.Device.Name)
	suggested := fmt.Sprintf("lazymobile-%s-%s.log", name, m.now().Format("20060102-150405"))
	m.modal = &modal{
		kind: modalPrompt, title: "Save the log", value: suggested,
		body: "Saves the lines that are shown now. A name without a folder goes in the current folder.",
		validate: func(value string) string {
			if value == "" {
				return "A file name is needed."
			}
			return ""
		},
		onSubmit: func(m *Model, value string) tea.Cmd {
			if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(value, "~/") {
				value = filepath.Join(home, value[2:])
			}
			var b strings.Builder
			for _, line := range v.shown {
				b.WriteString(line.Text)
				b.WriteByte('\n')
			}
			// Logs can hold tokens and personal data, so only the user may read the file.
			if err := os.WriteFile(value, []byte(b.String()), 0o600); err != nil {
				m.fail("Could not save the log", err)
				return nil
			}
			m.setNote(fmt.Sprintf("Saved %d lines to %s", len(v.shown), value), false)
			return nil
		},
	}
	return nil
}

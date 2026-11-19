package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/logs"
)

const (
	// followWithin is how old a run can be and still be followed, so an old run in the state file
	// does not move the dashboard a day later.
	followWithin = 10 * time.Minute
	// followListed is how long to wait for the run's device to show in the list, such as an emulator
	// that is still starting, before giving up on it.
	followListed = time.Minute
	// followEvery is how many ticks pass between looks at the state file.
	followEvery = 2
)

// followRun moves the dashboard to a run that `lazymobile run` started in this folder: it selects
// the device, and opens the log of the app when the folder's app is known. It is how a second
// terminal next to the one running the tool shows the device and its log without any keys. A run
// that was started before the dashboard was last used, or that is not recent, is left alone.
func (m *Model) followRun() tea.Cmd {
	if m.memory.Folder == "" || !m.restored || m.modal != nil {
		return nil
	}
	if m.followTicks++; m.followTicks%followEvery != 0 {
		return nil
	}
	run, ok := m.memory.Store.Load().Runs[m.memory.Folder]
	if !ok || run.Last == "" || !run.Started.After(m.runSeen) {
		return nil
	}
	age := m.now().Sub(run.Started)
	if age > followWithin {
		m.runSeen = run.Started
		return nil
	}
	at := -1
	for i, d := range m.devices {
		if d.Key() == run.Last {
			at = i
		}
	}
	if at < 0 {
		if age > followListed {
			m.runSeen = run.Started
		}
		return nil
	}
	m.runSeen = run.Started
	m.cursor = at
	d := m.devices[at]
	app := m.folderApp(d.Platform)
	switch {
	case m.logIsOpen(d, app):
		m.setNote("Following the run on "+d.Name, false)
		return nil
	case app == "" || !d.Ready() || (d.Platform == device.IOS && d.Kind == device.Physical):
		how := "press l and pick the app to see its log"
		if m.runShown() {
			// In the run view, l is the tool's until the log has the keys.
			how = "ctrl+o, then l, to pick the app and see its log"
		}
		m.setNote("Following the run on "+d.Name+": "+how, false)
		return nil
	}
	mode := logs.System
	if d.Platform == device.Android {
		mode = logs.Logcat
	}
	m.setNote("Following the run on "+d.Name+": the log of "+app, false)
	return m.startLogs(m.slotFor(newLog), logs.Spec{Device: d, Mode: mode, App: app}, "")
}

// folderApp is the app of this folder on a platform: the app of a log open now, or the one a log
// was last opened for here. It is "" when none was ever opened, and the first time the user picks it.
func (m *Model) folderApp(platform device.Platform) string {
	for _, v := range m.logPanes {
		if v.opened && v.spec.App != "" && v.spec.Device.Platform == platform {
			return v.spec.App
		}
	}
	return m.memory.Store.Load().Runs[m.memory.Folder].Apps[platform]
}

// logIsOpen reports whether a log of app on d is already showing.
func (m *Model) logIsOpen(d device.Device, app string) bool {
	for _, v := range m.logPanes {
		if v.opened && !v.ended && v.spec.Device.Key() == d.Key() && v.spec.App == app {
			return true
		}
	}
	return false
}

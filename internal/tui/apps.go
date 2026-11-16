package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/logs"
	"github.com/0xrnp/lazymobile/internal/mobile"
	"github.com/0xrnp/lazymobile/internal/state"
	"github.com/charmbracelet/x/ansi"
)

// pickApp offers the app ids used before and the apps installed on the device, then opens the
// log. With nothing to offer, it asks for an app id straight away.
func (m *Model) pickApp(d device.Device, mode logs.ModeInfo, slot int) tea.Cmd {
	recent := m.memory.Store.Load().Apps[d.Platform]
	m.setNote("Looking for the apps on "+d.Name+"…", false)
	m.appsAsked++
	asked := m.appsAsked
	ctx, backend := m.ctx, m.backend
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		// Without the installed apps, the ones used before are still offered, with no note on
		// whether each is on the device.
		installed, err := backend.InstalledApps(ctx, d)
		return applyMsg(func(m *Model) tea.Cmd {
			if m.note == "Looking for the apps on "+d.Name+"…" {
				m.setNote("", false)
			}
			// The answer is only wanted for the latest question, while its device is still the
			// selected one and nothing else has been opened.
			current, ok := m.selected()
			if asked == m.appsAsked && m.modal == nil && ok && current.Key() == d.Key() {
				m.offerApps(d, mode, state.Choices(recent, installed), err == nil, slot)
			}
			return nil
		})
	}
}

// pickUninstall lists the apps a user installed on a running device, asks which one to remove, and
// then asks again before doing it. Apps that come with the system are not listed.
func (m *Model) pickUninstall(d device.Device) tea.Cmd {
	if !d.Ready() {
		m.setNote(d.Name+" is not running: boot or connect it to uninstall an app", true)
		return nil
	}
	note := "Looking for the apps on " + d.Name + "…"
	m.setNote(note, false)
	m.appsAsked++
	asked := m.appsAsked
	ctx, backend := m.ctx, m.backend
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		apps, err := backend.InstalledApps(ctx, d)
		return applyMsg(func(m *Model) tea.Cmd {
			if m.note == note {
				m.setNote("", false)
			}
			current, ok := m.selected()
			if asked != m.appsAsked || m.modal != nil || !ok || current.Key() != d.Key() {
				return nil
			}
			switch {
			case err != nil:
				m.fail("Could not list the apps on "+d.Name, err)
			case len(apps) == 0:
				m.setNote("No apps of yours are installed on "+d.Name, false)
			default:
				m.offerUninstall(d, apps)
			}
			return nil
		})
	}
}

func (m *Model) offerUninstall(d device.Device, apps []device.App) {
	rows := make([]appRow, len(apps))
	for i, app := range apps {
		rows[i] = appRow{id: app.ID, name: app.Name, version: app.VersionLabel()}
	}
	items, notes := appRows(rows)
	m.modal = &modal{
		kind: modalChoice, title: "Uninstall which app from " + d.Name + "?", items: items, notes: notes, filterable: true,
		onChoose: func(m *Model, index int) tea.Cmd {
			id := apps[index].ID
			body := "This removes " + id + " and all its data from " + d.Name + ". It cannot be undone."
			if d.Kind == device.Physical {
				body += "\n" + d.Name + " is a real device."
			}
			m.confirm("Uninstall "+id+"?", body, func(m *Model) tea.Cmd {
				return m.act(d, "Uninstalling "+id+" from", "Uninstalled "+id+" from", nil, func(ctx context.Context) error {
					return m.backend.Uninstall(ctx, d, id)
				})
			})
			return nil
		},
	}
}

// pickAppAction lists the apps used before and the ones installed on a running device, then asks
// what to do with the chosen one: launch, restart or stop it.
func (m *Model) pickAppAction(d device.Device) tea.Cmd {
	if !d.Ready() {
		m.setNote(d.Name+" is not running: boot or connect it to launch an app", true)
		return nil
	}
	recent := m.memory.Store.Load().Apps[d.Platform]
	note := "Looking for the apps on " + d.Name + "…"
	m.setNote(note, false)
	m.appsAsked++
	asked := m.appsAsked
	ctx, backend := m.ctx, m.backend
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		installed, err := backend.InstalledApps(ctx, d)
		return applyMsg(func(m *Model) tea.Cmd {
			if m.note == note {
				m.setNote("", false)
			}
			current, ok := m.selected()
			if asked != m.appsAsked || m.modal != nil || !ok || current.Key() != d.Key() {
				return nil
			}
			// Without the installed apps, the ones used before are still offered.
			choices := state.Choices(recent, installed)
			switch {
			case len(choices) == 0 && err != nil:
				m.fail("Could not list the apps on "+d.Name, err)
			case len(choices) == 0:
				m.setNote("No apps of yours are installed on "+d.Name, false)
			default:
				m.offerAppActions(d, choices, err == nil)
			}
			return nil
		})
	}
}

func (m *Model) offerAppActions(d device.Device, choices []state.AppChoice, known bool) {
	rows := make([]appRow, len(choices))
	for i, c := range choices {
		rows[i] = appRow{id: c.ID, name: c.Name, version: c.VersionLabel(), used: c.Recent, missing: known && !c.Installed}
	}
	items, notes := appRows(rows)
	m.modal = &modal{
		kind: modalChoice, title: "Which app on " + d.Name + "?", items: items, notes: notes, filterable: true,
		onChoose: func(m *Model, index int) tea.Cmd {
			m.appActionModal(d, choices[index].ID)
			return nil
		},
	}
}

// screenshot saves a PNG of the selected device's screen in the folder lazymobile was started in.
func (m *Model) screenshot(d device.Device) tea.Cmd {
	if problem := screenFeature(d); problem != "" {
		m.setNote(problem, true)
		return nil
	}
	name := mobile.ScreenshotName(d, m.now())
	backend := m.backend
	return m.act(d, "Taking a screenshot of", "Saved "+name+" from", nil, func(ctx context.Context) error {
		_, err := backend.Screenshot(ctx, d, name)
		return err
	})
}

// toggleAppearance switches the selected device between dark and light mode. A device that
// follows the time of day (auto) is put in dark mode.
func (m *Model) toggleAppearance(d device.Device) tea.Cmd {
	if problem := screenFeature(d); problem != "" {
		m.setNote(problem, true)
		return nil
	}
	backend := m.backend
	return m.act(d, "Switching dark and light mode on", "Switched dark and light mode on", nil, func(ctx context.Context) error {
		current, err := backend.Appearance(ctx, d)
		if err != nil {
			return err
		}
		next := "dark"
		if current == "dark" {
			next = "light"
		}
		return backend.SetAppearance(ctx, d, next)
	})
}

// openLink asks for a web address or deep link, and opens it on the selected device.
func (m *Model) openLink(d device.Device) tea.Cmd {
	if problem := screenFeature(d); problem != "" {
		m.setNote(problem, true)
		return nil
	}
	m.modal = &modal{
		kind: modalPrompt, title: "Open a link on " + d.Name,
		body: "A web address, or a deep link into an app, such as myapp://home?id=1.",
		validate: func(value string) string {
			if !mobile.ValidLink(value) {
				return "Not a whole link: it needs a scheme such as https://, and no spaces."
			}
			return ""
		},
		onSubmit: func(m *Model, value string) tea.Cmd {
			backend := m.backend
			return m.act(d, "Opening a link on", "Opened a link on", nil, func(ctx context.Context) error {
				return backend.OpenLink(ctx, d, value)
			})
		},
	}
	return nil
}

// appAction is one thing that can be done to an app on a device.
type appAction struct {
	label, doing, done string
	run                func(ctx context.Context, backend Backend, d device.Device, id string) error
}

var (
	launchApp = appAction{"Launch", "Launching", "Launched", func(ctx context.Context, b Backend, d device.Device, id string) error {
		return b.Launch(ctx, d, id, mobile.LaunchOptions{})
	}}
	restartApp = appAction{"Restart", "Restarting", "Restarted", func(ctx context.Context, b Backend, d device.Device, id string) error {
		return b.Launch(ctx, d, id, mobile.LaunchOptions{Restart: true})
	}}
	stopApp = appAction{"Stop", "Stopping", "Stopped", func(ctx context.Context, b Backend, d device.Device, id string) error {
		return b.Stop(ctx, d, id)
	}}
)

func (m *Model) appActionModal(d device.Device, id string) {
	actions := []appAction{launchApp, restartApp, stopApp}
	if d.Platform == device.IOS && d.Kind == device.Physical {
		// An iPhone's app cannot be stopped on its own. Restart stops the running copy.
		actions = actions[:2]
	}
	labels := make([]string, len(actions))
	for i, action := range actions {
		labels[i] = action.label
	}
	m.modal = &modal{
		kind: modalChoice, title: id + " on " + d.Name, items: labels,
		onChoose: func(m *Model, index int) tea.Cmd {
			action := actions[index]
			_ = m.memory.Store.Update(func(f *state.File) { f.RememberApp(d.Platform, id) })
			backend := m.backend
			return m.act(d, action.doing+" "+id+" on", action.done+" "+id+" on", nil, func(ctx context.Context) error {
				return action.run(ctx, backend, d, id)
			})
		},
	}
}

// appRow is one app in a list: the id, and what tells builds of it apart.
type appRow struct {
	id, name, version string
	// used marks an app whose log was opened before, and missing one that the device's app list
	// does not have. An app used on another device may simply not be installed here yet.
	used, missing bool
}

// appRows lays apps out in columns: the id, then the version and build, then the name. The id
// column is as wide as the longest id, up to a limit, so the versions line up and a narrow
// window cuts the name, not the version.
func appRows(rows []appRow) (items, notes []string) {
	const idLimit = 44
	idWidth, versionWidth := 0, 0
	marked := false
	for _, r := range rows {
		idWidth = max(idWidth, min(ansi.StringWidth(r.id), idLimit))
		versionWidth = max(versionWidth, ansi.StringWidth(r.version))
		marked = marked || r.used
	}
	for _, r := range rows {
		item := fit(r.id, idWidth)
		if marked {
			mark := "  "
			if r.used {
				mark = "● "
			}
			item = mark + item
		}
		name := r.name
		if r.missing {
			name = "not found on this device"
		}
		note := strings.TrimRight(fit(r.version, versionWidth)+"  "+name, " ")
		items, notes = append(items, item), append(notes, note)
	}
	return items, notes
}

// offerApps lists the apps to log. When some were used before, the list starts with only those and
// tab shows every installed app, so the usual choice is a few lines and the rest is a key away.
//
// known is whether the device's app list could be read. Apps used before stay in the list either
// way, since the same id is used on several devices, and one still being installed here is fine.
func (m *Model) offerApps(d device.Device, mode logs.ModeInfo, choices []state.AppChoice, known bool, slot int) {
	if len(choices) == 0 {
		m.askApp(d, mode, slot)
		return
	}
	m.appsModal(d, mode, choices, known, slot, usedAndNot(choices), "")
}

// usedAndNot is whether some of the apps were used before and some were not.
func usedAndNot(choices []state.AppChoice) bool {
	var used, not bool
	for _, c := range choices {
		used, not = used || c.Recent, not || !c.Recent
	}
	return used && not
}

// appsModal shows the apps used before when compact, or every one. The toggle only exists when
// some apps were used before and some were not.
func (m *Model) appsModal(d device.Device, mode logs.ModeInfo, choices []state.AppChoice, known bool, slot int, compact bool, filter string) {
	mixed := usedAndNot(choices)
	shown := choices
	title := "Which app on " + d.Name + "?"
	if compact {
		shown = nil
		for _, c := range choices {
			if c.Recent {
				shown = append(shown, c)
			}
		}
		title += " (used before)"
	}
	rows := make([]appRow, len(shown))
	for i, c := range shown {
		rows[i] = appRow{id: c.ID, name: c.Name, version: c.VersionLabel(), used: c.Recent, missing: known && !c.Installed}
	}
	items, notes := appRows(rows)
	every := -1
	if !mode.AppRequired {
		every = len(items)
		items, notes = append(items, "Every app"), append(notes, "")
	}
	typed := len(items)
	items, notes = append(items, "Type an app id…"), append(notes, "")
	toggle := ""
	if mixed {
		toggle = "tab: used before"
		if compact {
			toggle = fmt.Sprintf("tab: all %d apps", len(choices))
		}
	}
	m.modal = &modal{
		kind: modalChoice, title: title, items: items, notes: notes, filterable: true, keep: len(items) - len(shown),
		filter: filter, extraHint: toggle,
		onKey: func(m *Model, key string, _ int) tea.Cmd {
			if key == "tab" && toggle != "" {
				m.appsModal(d, mode, choices, known, slot, !compact, m.modal.filter)
			}
			return nil
		},
		onChoose: func(m *Model, index int) tea.Cmd {
			switch index {
			case typed:
				m.askApp(d, mode, slot)
				return nil
			case every:
				return m.startLogs(slot, logs.Spec{Device: d, Mode: mode.ID}, "")
			}
			return m.startLogs(slot, logs.Spec{Device: d, Mode: mode.ID, App: shown[index].ID}, shown[index].VersionLabel())
		},
	}
}

func (m *Model) askApp(d device.Device, mode logs.ModeInfo, slot int) {
	body := "Show only one app: type its package name or bundle id. Leave empty for every app."
	if mode.AppRequired {
		body = "The bundle id of the app to launch, such as com.example.app."
	}
	value := m.lastApp[d.Platform]
	if recent := m.memory.Store.Load().Apps[d.Platform]; value == "" && len(recent) > 0 {
		value = recent[0]
	}
	m.modal = &modal{
		kind: modalPrompt, title: "App on " + d.Name, body: body, value: value,
		validate: func(value string) string {
			switch {
			case value == "" && mode.AppRequired:
				return "An app id is needed to launch it."
			case value != "" && !logs.ValidAppID(value):
				return "That does not look like an app id. Example: com.example.app"
			}
			return ""
		},
		onSubmit: func(m *Model, value string) tea.Cmd {
			m.lastApp[d.Platform] = value
			return m.startLogs(slot, logs.Spec{Device: d, Mode: mode.ID, App: value}, "")
		},
	}
}

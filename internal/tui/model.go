// Package tui is the dashboard: devices on the left, the log of one of them on the right.
package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/0xrnp/lazymobile/internal/android"
	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/ios"
	"github.com/0xrnp/lazymobile/internal/logs"
	"github.com/0xrnp/lazymobile/internal/mobile"
	"github.com/0xrnp/lazymobile/internal/state"
)

// Backend is what the dashboard needs from the rest of the program.
type Backend interface {
	HasIOS() bool
	List(ctx context.Context) ([]device.Device, []error)
	Boot(ctx context.Context, d device.Device, opts mobile.BootOptions) error
	Shutdown(ctx context.Context, d device.Device) error
	Erase(ctx context.Context, d device.Device) error
	Delete(ctx context.Context, d device.Device) error
	Runtimes(ctx context.Context) ([]ios.Runtime, error)
	CreateSimulator(ctx context.Context, name, deviceType, runtime string) error
	SystemImages() []android.SystemImage
	DeviceProfiles(ctx context.Context) ([]string, error)
	CreateAVD(ctx context.Context, name, image, profile string) error
	Pair(ctx context.Context, address, code string) error
	Connect(ctx context.Context, address string) error
	Disconnect(ctx context.Context, d device.Device) error
	Install(ctx context.Context, d device.Device, path string) error
	OpenURL(ctx context.Context, url string) error
	StartLogs(ctx context.Context, spec logs.Spec, h logs.Handlers) mobile.LogSession
	InstalledApps(ctx context.Context, d device.Device) ([]device.App, error)
}

// Memory is what the dashboard remembers between runs, and the folder it was started in, which
// decides which saved view is its own.
type Memory struct {
	Store  state.Store
	Folder string
}

const (
	// How often the device list is refreshed: every second while something is changing state, and
	// every few seconds otherwise, so devices started, stopped or plugged in elsewhere show up soon.
	// Listing runs several tools and takes about a second.
	refreshBusy = time.Second
	refreshIdle = 3 * time.Second
	// settleTimeout is how long the list is watched for an action's effect. The first boot of a
	// new emulator or simulator can take minutes.
	settleTimeout = 3 * time.Minute
)

const refreshingNote = "Refreshing…"

type pane int

const (
	paneDevices pane = iota
	paneBuilds
	paneLogs
)

type (
	devicesMsg struct {
		devices []device.Device
		errs    []error
	}
	tickMsg struct{}
	// applyMsg carries the result of background work back to the model.
	applyMsg    func(m *Model) tea.Cmd
	logLinesMsg struct {
		id    int
		lines []logs.Line
	}
	logStatusMsg struct {
		id   int
		text string
	}
	logEndMsg struct {
		id  int
		err error
	}
)

// Model is the dashboard's state.
type Model struct {
	ctx     context.Context
	backend Backend
	// builds is nil when the dashboard was not started inside an Expo project.
	builds Builds
	build  buildsView
	// send delivers a message from outside the update loop. Log streams use it.
	send func(tea.Msg)
	now  func() time.Time

	width, height int

	devices      []device.Device
	listErrs     []error
	loaded       bool
	refreshing   bool
	refreshAgain bool
	lastRefresh  time.Time
	cursor       int
	// busy maps a device key to what is being done to it, such as "Booting".
	busy map[string]string
	// settling holds actions whose command finished before the device list shows their effect.
	settling map[string]settling

	focus pane
	// widen is how many columns the devices pane was made wider (negative: narrower) with + and -.
	widen int
	// zoomed shows only the focused pane, on the whole screen.
	zoomed  bool
	modal   *modal
	note    string
	noteErr bool
	// lastApp remembers the app id typed for each platform, to offer it again.
	lastApp map[device.Platform]string
	log     logView

	// deviceRows and buildRows are, for each row drawn in those panes, the index of the device or
	// build on it, or -1. Drawing fills them in, and a click reads them.
	deviceRows, buildRows []int

	memory Memory
	// restored is set once the saved view has been put back, after the first device list.
	restored bool
	// saved is the view last written, so an unchanged one is not written again.
	saved *state.View
	// appsAsked counts the app lists asked for, so a slow one that is no longer wanted is dropped.
	appsAsked int
}

// New returns a dashboard that has not started yet.
func New(ctx context.Context, backend Backend, builds Builds) *Model {
	return &Model{
		ctx:      ctx,
		backend:  backend,
		builds:   builds,
		send:     func(tea.Msg) {},
		now:      time.Now,
		busy:     map[string]string{},
		settling: map[string]settling{},
		lastApp:  map[device.Platform]string{},
		log:      logView{follow: true},
	}
}

// Run shows the dashboard until the user quits.
func Run(ctx context.Context, backend Backend, builds Builds, memory Memory) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	m := New(ctx, backend, builds)
	m.memory = memory
	program := tea.NewProgram(m, tea.WithContext(ctx))
	m.send = program.Send
	_, err := program.Run()
	if errors.Is(err, tea.ErrInterrupted) || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.refresh(), tick())
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return tickMsg{} })
}

// refresh lists the devices again, unless a listing is already running.
func (m *Model) refresh() tea.Cmd {
	if m.refreshing {
		m.refreshAgain = true
		return nil
	}
	m.refreshing = true
	ctx, backend := m.ctx, m.backend
	return func() tea.Msg {
		devices, errs := backend.List(ctx)
		return devicesMsg{devices, errs}
	}
}

// arrange puts devices in display order: simulators, emulators, then phones.
func arrange(list []device.Device) []device.Device {
	sorted := device.Sort(list)
	out := make([]device.Device, 0, len(sorted))
	for _, group := range groups {
		for _, d := range sorted {
			if group.has(d) {
				out = append(out, d)
			}
		}
	}
	return out
}

type group struct {
	title string
	has   func(device.Device) bool
}

var groups = []group{
	{"iOS simulators", func(d device.Device) bool { return d.Kind == device.Simulator }},
	{"Android emulators", func(d device.Device) bool { return d.Kind == device.Emulator }},
	{"Phones and tablets", func(d device.Device) bool { return d.Kind == device.Physical }},
}

func (m *Model) selected() (device.Device, bool) {
	if m.cursor < 0 || m.cursor >= len(m.devices) {
		return device.Device{}, false
	}
	return m.devices[m.cursor], true
}

func (m *Model) setNote(text string, isError bool) {
	m.note, m.noteErr = text, isError
}

// fail reports a failed action in a window, so the reason and the advice stay readable.
func (m *Model) fail(title string, err error) {
	m.setNote(firstLine(err.Error()), true)
	m.modal = &modal{kind: modalMessage, title: title, body: err.Error()}
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyPressMsg:
		if m.modal != nil {
			return m, m.modalKey(msg)
		}
		return m, m.key(msg)
	case tea.MouseWheelMsg:
		if m.modal == nil {
			return m, m.wheel(msg)
		}
	case tea.MouseClickMsg:
		if m.modal == nil && msg.Button == tea.MouseLeft {
			m.click(msg.X, msg.Y)
		}
	case tea.PasteMsg:
		if m.modal != nil && m.modal.kind == modalPrompt {
			m.modal.value += strings.TrimSpace(firstLine(msg.Content))
			m.modal.problem = ""
		}
	case devicesMsg:
		return m, m.setDevices(msg)
	case buildsMsg:
		m.setBuilds(msg)
	case tickMsg:
		m.saveView()
		cmds := []tea.Cmd{tick()}
		interval := refreshIdle
		if m.changing() {
			interval = refreshBusy
		}
		if m.now().Sub(m.lastRefresh) >= interval {
			cmds = append(cmds, m.refresh())
		}
		if m.buildsDue() {
			cmds = append(cmds, m.loadBuilds())
		}
		return m, tea.Batch(cmds...)
	case applyMsg:
		return m, msg(m)
	case logLinesMsg:
		if msg.id == m.log.id {
			m.log.add(msg.lines)
		}
	case logStatusMsg:
		if msg.id == m.log.id {
			m.log.status = msg.text
		}
	case logEndMsg:
		if msg.id == m.log.id {
			m.log.ended, m.log.endErr, m.log.session = true, msg.err, nil
		}
	}
	return m, nil
}

func (m *Model) setDevices(msg devicesMsg) tea.Cmd {
	previous, hadSelection := m.selected()
	m.devices, m.listErrs = arrange(msg.devices), msg.errs
	m.loaded, m.refreshing, m.lastRefresh = true, false, m.now()
	m.settle()
	if m.note == refreshingNote {
		m.setNote("", false)
	}
	if hadSelection {
		for i, d := range m.devices {
			if d.Key() == previous.Key() {
				m.cursor = i
			}
		}
	}
	m.cursor = min(max(m.cursor, 0), max(len(m.devices)-1, 0))
	var cmds []tea.Cmd
	if !m.restored {
		cmd, done := m.restore()
		m.restored = done
		cmds = append(cmds, cmd)
	}
	if m.refreshAgain {
		m.refreshAgain = false
		cmds = append(cmds, m.refresh())
	}
	return tea.Batch(cmds...)
}

// restore puts back the view saved when the dashboard was last left in this folder: the
// selected device, the log settings, and the log itself when its device is still running. A log
// that launches an app is not started again on its own, since that would relaunch the app.
//
// It reports false, to be tried again with the next list, when the saved device is missing from
// a list that a tool failed to give in full.
func (m *Model) restore() (tea.Cmd, bool) {
	if m.memory.Folder == "" {
		return nil, true
	}
	view, ok := m.memory.Store.Load().Views[m.memory.Folder]
	if !ok {
		return nil, true
	}
	if len(m.listErrs) > 0 {
		for _, key := range []string{view.Device, logDevice(view)} {
			if key != "" && !slices.ContainsFunc(m.devices, func(d device.Device) bool { return d.Key() == key }) {
				return nil, false
			}
		}
	}
	m.log.filter, m.log.wrap = view.Filter, view.Wrap
	if view.LevelChosen {
		m.log.level, m.log.levelChosen = view.Level, true
	}
	for i, d := range m.devices {
		if d.Key() == view.Device {
			m.cursor = i
		}
	}
	if view.Log == nil || view.Log.Mode == logs.Console {
		return nil, true
	}
	for _, d := range m.devices {
		if d.Key() == view.Log.Device && d.Ready() {
			m.setNote("Opened the log you left: "+d.Name, false)
			return m.startLogs(logs.Spec{Device: d, Mode: view.Log.Mode, App: view.Log.App}), true
		}
	}
	return nil, true
}

func logDevice(view state.View) string {
	if view.Log == nil {
		return ""
	}
	return view.Log.Device
}

// saveView remembers where the dashboard is, for the next start in this folder. It runs every
// second as well as on quit, because Neovim, or a closed terminal, can end lazymobile without a
// quit. The file is only written when something changed.
func (m *Model) saveView() {
	// Before the saved view is put back, saving would replace it with an empty one.
	if m.memory.Folder == "" || !m.restored {
		return
	}
	view := state.View{Filter: m.log.filter, Level: m.log.level, LevelChosen: m.log.levelChosen, Wrap: m.log.wrap}
	if d, ok := m.selected(); ok {
		view.Device = d.Key()
	}
	if m.log.opened && !m.log.ended {
		view.Log = &state.Log{Device: m.log.spec.Device.Key(), Mode: m.log.spec.Mode, App: m.log.spec.App}
	}
	if m.saved != nil && sameView(*m.saved, view) {
		return
	}
	m.saved = &view
	view.SavedAt = m.now()
	// Remembering is a convenience, so a file that cannot be written is not reported.
	_ = m.memory.Store.Update(func(f *state.File) {
		if f.Views == nil {
			f.Views = map[string]state.View{}
		}
		f.Views[m.memory.Folder] = view
	})
}

func sameView(a, b state.View) bool {
	if (a.Log == nil) != (b.Log == nil) || (a.Log != nil && *a.Log != *b.Log) {
		return false
	}
	a.Log, b.Log = nil, nil
	return a == b
}

// settling is an action that has finished, but whose effect the device list does not show yet.
// `adb emu kill` returns while the emulator is still listed, and a booted emulator appears in adb
// only some seconds after it was started.
type settling struct {
	doing string
	until time.Time
	// done reports whether the device, or its absence, shows the action's effect.
	done func(d device.Device, found bool) bool
}

func isReady(d device.Device, found bool) bool { return found && d.Ready() }

func isStopped(d device.Device, found bool) bool { return !found || d.State == device.Shutdown }

func isGone(_ device.Device, found bool) bool { return !found }

// settle forgets the actions whose effect the list now shows, or that waited too long.
func (m *Model) settle() {
	for key, s := range m.settling {
		var current device.Device
		found := false
		for _, d := range m.devices {
			if d.Key() == key {
				current, found = d, true
				break
			}
		}
		if s.done(current, found) || !m.now().Before(s.until) {
			delete(m.settling, key)
		}
	}
}

// doing is what is being done to a device, or "" when nothing is.
func (m *Model) doing(key string) string {
	if doing := m.busy[key]; doing != "" {
		return doing
	}
	return m.settling[key].doing
}

// changing reports whether any device is between states, which is when the list is worth
// refreshing quickly.
func (m *Model) changing() bool {
	if len(m.busy) > 0 || len(m.settling) > 0 {
		return true
	}
	for _, d := range m.devices {
		if d.State == device.Booting || d.State == device.ShuttingDown {
			return true
		}
	}
	return false
}

// act runs one action on a device in the background and reports how it went. When settled is
// set, the device stays marked and the list is refreshed quickly until settled sees the effect.
func (m *Model) act(d device.Device, doing, done string, settled func(device.Device, bool) bool, run func(ctx context.Context) error) tea.Cmd {
	key := d.Key()
	// Only a command that is still running blocks another. A device that is still settling can be
	// acted on again, such as an emulator that crashed while booting.
	if current := m.busy[key]; current != "" {
		m.setNote(fmt.Sprintf("%s is busy: %s", d.Name, strings.ToLower(current)), true)
		return nil
	}
	delete(m.settling, key)
	m.busy[key] = doing
	m.setNote(fmt.Sprintf("%s %s…", doing, d.Name), false)
	ctx := m.ctx
	return func() tea.Msg {
		err := run(ctx)
		return applyMsg(func(m *Model) tea.Cmd {
			delete(m.busy, key)
			var notQuit *ios.AppNotQuitError
			switch {
			case errors.As(err, &notQuit):
				// The action itself succeeded: say what was left behind, and follow it as usual.
				m.setNote(firstLine(notQuit.Error()), true)
			case err != nil:
				m.fail("Could not finish: "+strings.ToLower(doing)+" "+d.Name, err)
			default:
				m.setNote(done+" "+d.Name, false)
			}
			if err == nil || notQuit != nil {
				if settled != nil {
					m.settling[key] = settling{doing: doing, until: m.now().Add(settleTimeout), done: settled}
				}
			}
			return m.refresh()
		})
	}
}

// background runs work that is not tied to one device, then applies its result.
func (m *Model) background(note string, work func(ctx context.Context) applyMsg) tea.Cmd {
	m.setNote(note, false)
	ctx := m.ctx
	return func() tea.Msg { return work(ctx) }
}

func (m *Model) confirm(title, body string, onYes func(m *Model) tea.Cmd) {
	m.modal = &modal{kind: modalConfirm, title: title, body: body, onYes: onYes}
}

func (m *Model) key(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	switch key {
	case "q", "ctrl+c":
		return m.quit()
	case "?":
		m.modal = &modal{kind: modalMessage, title: "Keys", body: helpText}
		return nil
	case "tab":
		m.cycle(1)
		return nil
	case "shift+tab":
		m.cycle(-1)
		return nil
	case "+", "=":
		m.resize(resizeStep)
		return nil
	case "-":
		m.resize(-resizeStep)
		return nil
	case "z":
		m.zoomed = !m.zoomed
		return nil
	case "esc":
		m.focus = paneDevices
		return nil
	}
	switch m.focus {
	case paneLogs:
		return m.logKey(key)
	case paneBuilds:
		return m.buildKey(key)
	}
	return m.deviceKey(key)
}

// paneAt is the pane drawn at a screen cell.
func (m *Model) paneAt(x, y int) (pane, bool) {
	l := m.layout()
	switch {
	case y >= l.bodyHeight:
		return 0, false
	case x < l.devicesWidth && y >= l.bodyHeight-l.buildsHeight:
		return paneBuilds, true
	case x < l.devicesWidth:
		return paneDevices, true
	case l.logsWidth > 0:
		return paneLogs, true
	}
	return 0, false
}

// click focuses the pane under the pointer, and selects the device or build clicked.
func (m *Model) click(x, y int) {
	p, ok := m.paneAt(x, y)
	if !ok {
		return
	}
	m.focus = p
	l := m.layout()
	// Rows inside a pane start under its top border.
	switch p {
	case paneDevices:
		if i := rowAt(m.deviceRows, y-1); i >= 0 {
			m.cursor = i
		}
	case paneBuilds:
		if i := rowAt(m.buildRows, y-(l.bodyHeight-l.buildsHeight)-1); i >= 0 {
			m.build.cursor = i
		}
	}
}

// owners turns the line of each item into the item on each of the rows shown, which start at
// line first.
func owners(rowOf []int, first, rows int) []int {
	out := make([]int, rows)
	for i := range out {
		out[i] = -1
	}
	for item, line := range rowOf {
		if row := line - first; row >= 0 && row < rows {
			out[row] = item
		}
	}
	return out
}

func rowAt(rows []int, row int) int {
	if row < 0 || row >= len(rows) {
		return -1
	}
	return rows[row]
}

// wheel scrolls the pane under the pointer, without moving the focus.
func (m *Model) wheel(msg tea.MouseWheelMsg) tea.Cmd {
	p, ok := m.paneAt(msg.X, msg.Y)
	if !ok {
		return nil
	}
	button := msg.Button
	// Terminals without sideways wheel events send Shift with the normal wheel instead.
	if msg.Mod.Contains(tea.ModShift) {
		button = map[tea.MouseButton]tea.MouseButton{tea.MouseWheelUp: tea.MouseWheelLeft, tea.MouseWheelDown: tea.MouseWheelRight}[button]
	}
	step := 0
	switch button {
	case tea.MouseWheelUp:
		step = -1
	case tea.MouseWheelDown:
		step = 1
	case tea.MouseWheelLeft, tea.MouseWheelRight:
		// Sliding sideways moves the log view, which is the only pane wider than its window.
		if p == paneLogs && m.log.opened && !m.log.wrap {
			delta := map[tea.MouseButton]int{tea.MouseWheelLeft: -4, tea.MouseWheelRight: 4}[button]
			l := m.layout()
			m.log.slide(delta, l.logsWidth-2, max(l.logRows, 1))
		}
		return nil
	default:
		return nil
	}
	key := map[int]string{-1: "up", 1: "down"}[step]
	switch p {
	case paneLogs:
		if m.log.opened {
			m.log.scroll(3*step, max(m.layout().logRows, 1))
		}
		return nil
	case paneBuilds:
		return m.buildKey(key)
	}
	return m.deviceKey(key)
}

// cycle moves the focus to the next or previous pane.
func (m *Model) cycle(step int) {
	panes := []pane{paneDevices, paneLogs}
	if m.builds != nil {
		panes = []pane{paneDevices, paneBuilds, paneLogs}
	}
	for i, p := range panes {
		if p == m.focus {
			m.focus = panes[(i+step+len(panes))%len(panes)]
			return
		}
	}
	m.focus = paneDevices
}

func (m *Model) deviceKey(key string) tea.Cmd {
	switch key {
	case "up", "k":
		m.cursor = max(m.cursor-1, 0)
		return nil
	case "down", "j":
		m.cursor = min(m.cursor+1, max(len(m.devices)-1, 0))
		return nil
	case "g", "home":
		m.cursor = 0
		return nil
	case "G", "end":
		m.cursor = max(len(m.devices)-1, 0)
		return nil
	case "r":
		m.setNote(refreshingNote, false)
		return m.refresh()
	case "n":
		return m.create()
	case "w":
		return m.wireless()
	}

	d, ok := m.selected()
	if !ok {
		return nil
	}
	virtual := d.Kind != device.Physical
	switch key {
	case "b":
		if virtual {
			return m.act(d, "Booting", "Started", isReady, func(ctx context.Context) error {
				return m.backend.Boot(ctx, d, mobile.BootOptions{})
			})
		}
	case "B":
		if d.Kind == device.Emulator {
			return m.act(d, "Cold booting", "Started", isReady, func(ctx context.Context) error {
				return m.backend.Boot(ctx, d, mobile.BootOptions{Cold: true})
			})
		}
		m.setNote("Cold boot is for Android emulators", true)
	case "s":
		if virtual {
			return m.act(d, "Shutting down", "Shut down", isStopped, func(ctx context.Context) error {
				return m.backend.Shutdown(ctx, d)
			})
		}
	case "e":
		if virtual {
			body := "This removes every app and all data on " + d.Name + ". It cannot be undone."
			if d.Kind == device.Emulator {
				body += "\nThe emulator then starts with its data wiped."
			}
			m.confirm("Erase "+d.Name+"?", body, func(m *Model) tea.Cmd {
				// A simulator is erased while shut down. An emulator is started with its data wiped.
				var settled func(device.Device, bool) bool
				if d.Kind == device.Emulator {
					settled = isReady
				}
				return m.act(d, "Erasing", "Erased", settled, func(ctx context.Context) error { return m.backend.Erase(ctx, d) })
			})
		}
	case "d":
		if virtual {
			m.confirm("Delete "+d.Name+"?", "This deletes "+d.Name+" and everything on it for good.", func(m *Model) tea.Cmd {
				return m.act(d, "Deleting", "Deleted", isGone, func(ctx context.Context) error { return m.backend.Delete(ctx, d) })
			})
		}
	case "x":
		if d.Platform == device.Android && d.Transport == "wifi" {
			return m.act(d, "Disconnecting", "Disconnected", isGone, func(ctx context.Context) error {
				return m.backend.Disconnect(ctx, d)
			})
		}
		m.setNote("Only phones connected over Wi-Fi can be disconnected", true)
	case "y":
		m.setNote("Copied "+d.TargetID(), false)
		return tea.SetClipboard(d.TargetID())
	case "l", "enter":
		return m.openLogs(d)
	}
	if !virtual && strings.Contains("bsed", key) && len(key) == 1 {
		m.setNote(d.Name+" is a real device: it is not booted, shut down, erased or deleted from here", true)
	}
	return nil
}

func (m *Model) logKey(key string) tea.Cmd {
	v := &m.log
	if !v.opened {
		if key == "l" || key == "enter" {
			if d, ok := m.selected(); ok {
				return m.openLogs(d)
			}
		}
		return nil
	}
	rows := max(m.layout().logRows, 1)
	switch key {
	case "up", "k":
		v.scroll(-1, rows)
	case "down", "j":
		v.scroll(1, rows)
	case "pgup", "ctrl+u":
		v.scroll(-rows/2, rows)
	case "pgdown", "ctrl+d", "space":
		v.scroll(rows/2, rows)
	case "g", "home":
		v.first()
	case "G", "end":
		v.follow = true
	case "enter":
		entry := v.entry()
		if len(entry) == 0 {
			return nil
		}
		m.modal = &modal{kind: modalMessage, title: "Log line", body: entryText(entry)}
	case "y":
		entry := v.entry()
		if len(entry) == 0 {
			return nil
		}
		m.setNote("Copied the selected log line", false)
		return tea.SetClipboard(entryText(entry))
	case "left", "h":
		v.slide(-8, m.layout().logsWidth-2, rows)
	case "right", "l":
		v.slide(8, m.layout().logsWidth-2, rows)
	case "0":
		v.left = 0
	case "w":
		v.wrap = !v.wrap
	case "p":
		v.paused = !v.paused
		if !v.paused {
			v.rebuild()
		}
	case "c":
		v.clear()
	case "L":
		v.level = (v.level + 1) % (logs.Fatal + 1)
		v.levelChosen = true
		v.rebuild()
	case "/", "f":
		m.modal = &modal{
			kind: modalPrompt, title: "Filter the log", value: v.filter,
			body: "Show only lines that contain this text. Leave empty to show everything.",
			onSubmit: func(m *Model, value string) tea.Cmd {
				m.log.filter = value
				m.log.rebuild()
				return nil
			},
		}
	case "S":
		return m.saveLog()
	case "x":
		if v.session != nil {
			v.session.Stop()
		}
	case "r":
		return m.startLogs(v.spec)
	}
	return nil
}

func (m *Model) quit() tea.Cmd {
	m.saveView()
	session := m.log.session
	if session == nil {
		return tea.Quit
	}
	// Wait for the log tool to be stopped, or it would be left running after the exit.
	return func() tea.Msg {
		session.Stop()
		_ = session.Wait()
		return tea.Quit()
	}
}

// Logs ---------------------------------------------------------------------------------------

func (m *Model) openLogs(d device.Device) tea.Cmd {
	modes := logs.Modes(d)
	switch len(modes) {
	case 0:
		m.setNote(d.Name+" is not running. Boot or connect it to see its logs", true)
		return nil
	case 1:
		return m.pickApp(d, modes[0])
	}
	labels := make([]string, len(modes))
	for i, mode := range modes {
		labels[i] = mode.Label
	}
	m.modal = &modal{
		kind: modalChoice, title: "Which log of " + d.Name + "?", items: labels,
		onChoose: func(m *Model, index int) tea.Cmd {
			return m.pickApp(d, modes[index])
		},
	}
	return nil
}

// pickApp offers the app ids used before and the apps installed on the device, then opens the
// log. With nothing to offer, it asks for an app id straight away.
func (m *Model) pickApp(d device.Device, mode logs.ModeInfo) tea.Cmd {
	recent := m.memory.Store.Load().Apps[d.Platform]
	if d.Platform == device.IOS && d.Kind == device.Physical {
		m.offerApps(d, mode, state.Choices(recent, nil))
		return nil
	}
	m.setNote("Looking for the apps on "+d.Name+"…", false)
	m.appsAsked++
	asked := m.appsAsked
	ctx, backend := m.ctx, m.backend
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		// Without the installed apps, the ones used before are still offered.
		installed, _ := backend.InstalledApps(ctx, d)
		return applyMsg(func(m *Model) tea.Cmd {
			if m.note == "Looking for the apps on "+d.Name+"…" {
				m.setNote("", false)
			}
			// The answer is only wanted for the latest question, while its device is still the
			// selected one and nothing else has been opened.
			current, ok := m.selected()
			if asked == m.appsAsked && m.modal == nil && ok && current.Key() == d.Key() {
				m.offerApps(d, mode, state.Choices(recent, installed))
			}
			return nil
		})
	}
}

func (m *Model) offerApps(d device.Device, mode logs.ModeInfo, choices []state.AppChoice) {
	if len(choices) == 0 {
		m.askApp(d, mode)
		return
	}
	items := make([]string, 0, len(choices)+2)
	for _, c := range choices {
		var about []string
		if c.Name != "" {
			about = append(about, c.Name)
		}
		if c.Recent {
			about = append(about, "used before")
		}
		items = append(items, c.ID+"   "+strings.Join(about, " · "))
	}
	every := -1
	if !mode.AppRequired {
		every = len(items)
		items = append(items, "Every app")
	}
	typed := len(items)
	items = append(items, "Type an app id…")
	m.modal = &modal{
		kind: modalChoice, title: "Which app on " + d.Name + "?", items: items,
		onChoose: func(m *Model, index int) tea.Cmd {
			switch index {
			case typed:
				m.askApp(d, mode)
				return nil
			case every:
				return m.startLogs(logs.Spec{Device: d, Mode: mode.ID})
			}
			return m.startLogs(logs.Spec{Device: d, Mode: mode.ID, App: choices[index].ID})
		},
	}
}

func (m *Model) askApp(d device.Device, mode logs.ModeInfo) {
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
			return m.startLogs(logs.Spec{Device: d, Mode: mode.ID, App: value})
		},
	}
}

func (m *Model) startLogs(spec logs.Spec) tea.Cmd {
	if m.log.session != nil {
		m.log.session.Stop()
	}
	id := m.log.id + 1
	level := m.log.level
	if !m.log.levelChosen {
		level = defaultLevel(spec)
	}
	m.log = logView{id: id, opened: true, spec: spec, follow: true, level: level, levelChosen: m.log.levelChosen, filter: m.log.filter, wrap: m.log.wrap}
	send := m.send
	session := m.backend.StartLogs(m.ctx, spec, logs.Handlers{
		Lines:  func(lines []logs.Line) { send(logLinesMsg{id, lines}) },
		Status: func(text string) { send(logStatusMsg{id, text}) },
	})
	m.log.session = session
	m.focus = paneLogs
	if spec.App != "" {
		m.lastApp[spec.Device.Platform] = spec.App
		_ = m.memory.Store.Update(func(f *state.File) { f.RememberApp(spec.Device.Platform, spec.App) })
	}
	m.saveView()
	return func() tea.Msg { return logEndMsg{id, session.Wait()} }
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

func (m *Model) saveLog() tea.Cmd {
	name := strings.Map(func(r rune) rune {
		if r == ' ' || r == '/' {
			return '-'
		}
		return r
	}, m.log.spec.Device.Name)
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
			for _, line := range m.log.shown {
				b.WriteString(line.Text)
				b.WriteByte('\n')
			}
			// Logs can hold tokens and personal data, so only the user may read the file.
			if err := os.WriteFile(value, []byte(b.String()), 0o600); err != nil {
				m.fail("Could not save the log", err)
				return nil
			}
			m.setNote(fmt.Sprintf("Saved %d lines to %s", len(m.log.shown), value), false)
			return nil
		},
	}
	return nil
}

// Creating devices -----------------------------------------------------------------------------

func (m *Model) create() tea.Cmd {
	if !m.backend.HasIOS() {
		return m.createAVD()
	}
	m.modal = &modal{
		kind: modalChoice, title: "New device", items: []string{"iOS simulator", "Android emulator"},
		onChoose: func(m *Model, index int) tea.Cmd {
			if index == 0 {
				return m.createSimulator()
			}
			return m.createAVD()
		},
	}
	return nil
}

func (m *Model) message(title, body string) {
	m.modal = &modal{kind: modalMessage, title: title, body: body}
}

func (m *Model) createSimulator() tea.Cmd {
	return m.background("Reading the installed iOS versions…", func(ctx context.Context) applyMsg {
		runtimes, err := m.backend.Runtimes(ctx)
		return func(m *Model) tea.Cmd {
			m.setNote("", false)
			switch {
			case err != nil:
				m.fail("Could not read the iOS versions", err)
				return nil
			case len(runtimes) == 0:
				m.message("No iOS version is installed", "Install one in Xcode > Settings > Components, then try again.")
				return nil
			}
			names := make([]string, len(runtimes))
			for i, r := range runtimes {
				names[i] = r.Name
			}
			m.modal = &modal{
				kind: modalChoice, title: "iOS version", items: names,
				onChoose: func(m *Model, index int) tea.Cmd {
					m.chooseSimulatorType(runtimes[index])
					return nil
				},
			}
			return nil
		}
	})
}

func (m *Model) chooseSimulatorType(runtime ios.Runtime) {
	if len(runtime.DeviceTypes) == 0 {
		m.message("No device types", "Xcode reports no iPhone or iPad for "+runtime.Name+".")
		return
	}
	names := make([]string, len(runtime.DeviceTypes))
	for i, t := range runtime.DeviceTypes {
		names[i] = t.Name
	}
	m.modal = &modal{
		kind: modalChoice, title: "Device for " + runtime.Name, items: names,
		onChoose: func(m *Model, index int) tea.Cmd {
			deviceType := runtime.DeviceTypes[index]
			m.modal = &modal{
				kind: modalPrompt, title: "Name of the new simulator", value: deviceType.Name,
				validate: func(value string) string {
					if value == "" {
						return "A name is needed."
					}
					return ""
				},
				onSubmit: func(m *Model, name string) tea.Cmd {
					return m.background("Creating "+name+"…", func(ctx context.Context) applyMsg {
						err := m.backend.CreateSimulator(ctx, name, deviceType.ID, runtime.ID)
						return created(name, err)
					})
				},
			}
			return nil
		},
	}
}

func created(name string, err error) applyMsg {
	return func(m *Model) tea.Cmd {
		if err != nil {
			m.fail("Could not create "+name, err)
			return nil
		}
		m.setNote("Created "+name, false)
		return m.refresh()
	}
}

func (m *Model) createAVD() tea.Cmd {
	images := m.backend.SystemImages()
	if len(images) == 0 {
		m.message("No Android system image is installed",
			"An emulator needs a system image that runs on this machine.\n"+
				"Install one in Android Studio > SDK Manager > SDK Platforms (tick \"Show Package Details\"), "+
				"or with sdkmanager, then try again.")
		return nil
	}
	labels := make([]string, len(images))
	for i, image := range images {
		labels[i] = image.Label
	}
	m.modal = &modal{
		kind: modalChoice, title: "Android version", items: labels,
		onChoose: func(m *Model, index int) tea.Cmd {
			image := images[index]
			return m.background("Reading the device profiles…", func(ctx context.Context) applyMsg {
				profiles, err := m.backend.DeviceProfiles(ctx)
				return func(m *Model) tea.Cmd {
					m.setNote("", false)
					if err != nil {
						m.fail("Could not read the device profiles", err)
						return nil
					}
					m.chooseProfile(image, profiles)
					return nil
				}
			})
		},
	}
	return nil
}

func (m *Model) chooseProfile(image android.SystemImage, profiles []string) {
	items := append([]string{"Default hardware"}, profiles...)
	m.modal = &modal{
		kind: modalChoice, title: "Hardware profile", items: items,
		onChoose: func(m *Model, index int) tea.Cmd {
			profile := ""
			suggested := "Android_" + image.API
			if index > 0 {
				profile = profiles[index-1]
				suggested = profile + "_API_" + image.API
			}
			m.modal = &modal{
				kind: modalPrompt, title: "Name of the new emulator", value: suggested,
				body: "Letters, digits, dots, dashes and underscores.",
				validate: func(value string) string {
					if !android.ValidAVDName(value) {
						return "Use letters, digits, dots, dashes and underscores, starting with a letter or digit."
					}
					return ""
				},
				onSubmit: func(m *Model, name string) tea.Cmd {
					return m.background("Creating "+name+"…", func(ctx context.Context) applyMsg {
						return created(name, m.backend.CreateAVD(ctx, name, image.ID, profile))
					})
				},
			}
			return nil
		},
	}
}

// Wireless phones ----------------------------------------------------------------------------

const wirelessHelp = "On the phone: Settings > Developer options > Wireless debugging."

func addressProblem(value string) string {
	if !android.ValidAddress(value) {
		return "Type an address and port, such as 192.168.1.5:37123"
	}
	return ""
}

func (m *Model) wireless() tea.Cmd {
	m.modal = &modal{
		kind: modalChoice, title: "Android phone over Wi-Fi",
		items: []string{"Pair a new phone with a pairing code", "Connect to a phone that is already paired"},
		onChoose: func(m *Model, index int) tea.Cmd {
			if index == 0 {
				m.pair()
			} else {
				m.connect()
			}
			return nil
		},
	}
	return nil
}

func (m *Model) pair() {
	m.modal = &modal{
		kind: modalPrompt, title: "Pairing address", validate: addressProblem,
		body: wirelessHelp + " Tap \"Pair device with pairing code\" and type the address and port shown under the code.",
		onSubmit: func(m *Model, address string) tea.Cmd {
			m.modal = &modal{
				kind: modalPrompt, title: "Pairing code", body: "The six digits shown on the phone.",
				validate: func(value string) string {
					if len(value) != 6 || strings.Trim(value, "0123456789") != "" {
						return "The code is six digits."
					}
					return ""
				},
				onSubmit: func(m *Model, code string) tea.Cmd {
					return m.background("Pairing with "+address+"…", func(ctx context.Context) applyMsg {
						err := m.backend.Pair(ctx, address, code)
						return func(m *Model) tea.Cmd {
							if err != nil {
								m.fail("Could not pair", err)
								return nil
							}
							m.setNote("Paired. Now connect to the address on the Wireless debugging screen", false)
							m.connect()
							return nil
						}
					})
				},
			}
			return nil
		},
	}
}

func (m *Model) connect() {
	m.modal = &modal{
		kind: modalPrompt, title: "Connect address", validate: addressProblem,
		body: wirelessHelp + " Type the \"IP address & Port\" shown at the top. It differs from the pairing address.",
		onSubmit: func(m *Model, address string) tea.Cmd {
			return m.background("Connecting to "+address+"…", func(ctx context.Context) applyMsg {
				err := m.backend.Connect(ctx, address)
				return func(m *Model) tea.Cmd {
					if err != nil {
						m.fail("Could not connect", err)
						return nil
					}
					m.setNote("Connected to "+address, false)
					return m.refresh()
				}
			})
		},
	}
}

// errorHint is the advice part of an error, when it has one.
func errorHint(err error) string {
	var e *apperr.Error
	if errors.As(err, &e) {
		return e.Hint
	}
	return ""
}

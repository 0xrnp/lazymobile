// Package tui is the dashboard: devices on the left, the log of one of them on the right.
package tui

import (
	"context"
	"errors"
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"os"
	"path/filepath"
	"reflect"
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
	Uninstall(ctx context.Context, d device.Device, id string) error
	Launch(ctx context.Context, d device.Device, id string, opts mobile.LaunchOptions) error
	Stop(ctx context.Context, d device.Device, id string) error
	OpenURL(ctx context.Context, url string) error
	StartLogs(ctx context.Context, spec logs.Spec, h logs.Handlers) mobile.LogSession
	InstalledApps(ctx context.Context, d device.Device) ([]device.App, error)
	AppInfo(ctx context.Context, d device.Device, id string) (device.App, error)
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

// waitingNote is shown while a log that launches an app waits to be started.
const waitingNote = "A log that launches an app is waiting: press r in it to launch the app"

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
	// logPanes are the log panes, left to right and top to bottom. There is always at least one.
	// logIdx is the one with the focus while focus is on the logs, and lastLog the one that last had
	// it. arrange is how the panes sit.
	logPanes []*logView
	logIdx   int
	lastLog  int
	arrange  arrangement
	// logIDs counts the log streams started, so each has an id of its own.
	logIDs int

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
		logPanes: []*logView{{follow: true}},
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
		if m.modal.kind == modalKeys {
			m.modal.keys.wheel(msg.Button)
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
		if v := m.logByID(msg.id); v != nil {
			v.add(msg.lines)
		}
	case logStatusMsg:
		if v := m.logByID(msg.id); v != nil {
			v.status = msg.text
		}
	case logEndMsg:
		if v := m.logByID(msg.id); v != nil {
			v.ended, v.endErr, v.session = true, msg.err, nil
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
		keys := []string{view.Device, logDevice(view.Log)}
		for i := range view.More {
			keys = append(keys, view.More[i].Device)
		}
		for _, key := range keys {
			if key != "" && !slices.ContainsFunc(m.devices, func(d device.Device) bool { return d.Key() == key }) {
				return nil, false
			}
		}
	}
	first := m.first()
	first.filter, first.wrap = view.Filter, view.Wrap
	if view.LevelChosen {
		first.level, first.levelChosen = view.Level, true
	}
	m.arrange = arrangement(view.Layout)
	for i, d := range m.devices {
		if d.Key() == view.Device {
			m.cursor = i
		}
	}
	var cmds []tea.Cmd
	saved := []*state.Log{view.Log}
	for i := range view.More {
		saved = append(saved, &view.More[i])
	}
	for i, log := range saved {
		if log == nil {
			continue
		}
		for _, d := range m.devices {
			if d.Key() != log.Device || !d.Ready() {
				continue
			}
			// Each log goes in the next pane. The first keeps the settings kept for it in the view, and
			// the others have their own.
			slot := m.slotFor(newLog)
			if i > 0 {
				v := m.logPanes[slot]
				v.filter, v.wrap = log.Filter, log.Wrap
				if log.LevelChosen {
					v.level, v.levelChosen = log.Level, true
				}
			}
			spec := logs.Spec{Device: d, Mode: log.Mode, App: log.App}
			// A log that launches an app waits for r, since starting it would launch the app again.
			if log.Mode == logs.Console {
				m.parkLog(slot, spec)
				m.setNote(waitingNote, false)
			} else {
				m.setNote("Opened the log you left: "+d.Name, false)
				cmds = append(cmds, m.startLogs(slot, spec, ""))
			}
			break
		}
	}
	if m.first().opened {
		m.focusLog(0)
	}
	return tea.Batch(cmds...), true
}

func logDevice(log *state.Log) string {
	if log == nil {
		return ""
	}
	return log.Device
}

// saveView remembers where the dashboard is, for the next start in this folder. It runs every
// second as well as on quit, because Neovim, or a closed terminal, can end lazymobile without a
// quit. The file is only written when something changed.
func (m *Model) saveView() {
	// Before the saved view is put back, saving would replace it with an empty one.
	if m.memory.Folder == "" || !m.restored {
		return
	}
	first := m.first()
	view := state.View{Filter: first.filter, Level: first.level, LevelChosen: first.levelChosen, Wrap: first.wrap, Layout: string(m.arrange)}
	if d, ok := m.selected(); ok {
		view.Device = d.Key()
	}
	if first.opened && !first.ended {
		view.Log = &state.Log{Device: first.spec.Device.Key(), Mode: first.spec.Mode, App: first.spec.App}
	}
	for _, v := range m.logPanes[1:] {
		if v.opened && !v.ended {
			view.More = append(view.More, state.Log{
				Device: v.spec.Device.Key(), Mode: v.spec.Mode, App: v.spec.App,
				Filter: v.filter, Level: v.level, LevelChosen: v.levelChosen, Wrap: v.wrap,
			})
		}
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
	return reflect.DeepEqual(a, b)
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
	return m.keyString(msg.String())
}

// keyString does what a key does on the dashboard. The keys menu runs an action by sending its key
// here, so what it does is what pressing the key does.
func (m *Model) keyString(key string) tea.Cmd {
	if i, _, ok := m.focusedLog(); ok {
		m.lastLog = i
	}
	switch key {
	case "q", "ctrl+c":
		return m.quit()
	case "?":
		m.modal = m.keysModal()
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
	case "v":
		m.arrange = m.arrange.next()
		switch {
		case m.logCount() < 2:
			m.setNote("Press l on another device to open another log. v chooses how they sit", false)
		case m.arrange == arrangeStacked:
			m.setNote("Logs stacked", false)
		case m.arrange == arrangeOne:
			m.setNote("One log at a time: tab shows the next, esc goes to the devices", false)
		default:
			m.setNote("Logs side by side", false)
		}
		return nil
	case "a":
		m.showLogs()
		return nil
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		// A number jumps to that log.
		if i := int(key[0] - '1'); i < m.logCount() {
			m.focusLog(i)
		} else {
			m.setNote(fmt.Sprintf("There is no log %s: %d open", key, m.logCount()), true)
		}
		return nil
	case "esc":
		m.focus, m.zoomed = paneDevices, false
		return nil
	}
	if m.focusOnLog() {
		return m.logKey(key)
	}
	if m.focus == paneBuilds {
		return m.buildKey(key)
	}
	return m.deviceKey(key)
}

// paneAt is the pane drawn at a screen cell.
// For a log pane it also says which one.
func (m *Model) paneAt(x, y int) (pane, int, bool) {
	l := m.layout()
	switch {
	case y >= l.bodyHeight:
		return 0, 0, false
	case x < l.devicesWidth && y >= l.bodyHeight-l.buildsHeight:
		return paneBuilds, 0, true
	case x < l.devicesWidth:
		return paneDevices, 0, true
	}
	for i, b := range l.logs {
		if b.contains(x, y) {
			return paneLogs, i, true
		}
	}
	return 0, 0, false
}

// click focuses the pane under the pointer, and selects the device or build clicked.
func (m *Model) click(x, y int) {
	p, index, ok := m.paneAt(x, y)
	if !ok {
		return
	}
	m.focus = p
	if p == paneLogs {
		m.focusLog(index)
	}
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
	p, index, ok := m.paneAt(msg.X, msg.Y)
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
		if v := m.logPanes[index]; p == paneLogs && v.opened && !v.wrap {
			delta := map[tea.MouseButton]int{tea.MouseWheelLeft: -4, tea.MouseWheelRight: 4}[button]
			b := m.layout().logs[index]
			v.slide(delta, b.width-2, max(b.rows(), 1))
		}
		return nil
	default:
		return nil
	}
	key := map[int]string{-1: "up", 1: "down"}[step]
	if p == paneLogs {
		if v := m.logPanes[index]; v.opened {
			v.scroll(3*step, max(m.layout().logs[index].rows(), 1))
		}
		return nil
	}
	if p == paneBuilds {
		return m.buildKey(key)
	}
	return m.deviceKey(key)
}

// cycle moves the focus to the next or previous pane, through the devices, the builds and every
// log in turn. With one log shown at a time, on the whole screen or beside the devices, it goes
// from log to log instead, and esc leaves for the devices.
func (m *Model) cycle(step int) {
	n := m.logCount()
	if i, _, ok := m.focusedLog(); ok && n > 1 && (m.zoomed || m.arrange == arrangeOne) {
		m.focusLog((i + step + n) % n)
		return
	}
	type stop struct {
		pane  pane
		index int
	}
	stops := []stop{{paneDevices, 0}}
	if m.builds != nil {
		stops = append(stops, stop{paneBuilds, 0})
	}
	for i := range max(n, 1) {
		stops = append(stops, stop{paneLogs, i})
	}
	for i, s := range stops {
		if s.pane == m.focus && (s.pane != paneLogs || s.index == m.logIdx) {
			next := stops[(i+step+len(stops))%len(stops)]
			m.focus = next.pane
			if next.pane == paneLogs {
				m.focusLog(next.index)
			}
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
	case "u":
		return m.pickUninstall(d)
	case "o":
		return m.pickAppAction(d)
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
		return m.openLogs(d, newLog)
	}
	if !virtual && strings.Contains("bsed", key) && len(key) == 1 {
		m.setNote(d.Name+" is a real device: it is not booted, shut down, erased or deleted from here", true)
	}
	return nil
}

func (m *Model) logKey(key string) tea.Cmd {
	slot, v, _ := m.focusedLog()
	if !v.opened {
		if key == "l" || key == "enter" {
			if d, ok := m.selected(); ok {
				return m.openLogs(d, slot)
			}
		}
		return nil
	}
	box := m.layout().logs[slot]
	rows := max(box.rows(), 1)
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
		v.slide(-8, box.width-2, rows)
	case "right", "l":
		v.slide(8, box.width-2, rows)
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
				v.filter = value
				v.rebuild()
				return nil
			},
		}
	case "S":
		return m.saveLog(v)
	case "x":
		if v.session != nil {
			v.session.Stop()
		}
	case "X":
		m.closeLog(slot)
	case "r":
		return m.startLogs(slot, v.spec, "")
	}
	return nil
}

func (m *Model) quit() tea.Cmd {
	m.saveView()
	sessions := m.logSessions()
	if len(sessions) == 0 {
		return tea.Quit
	}
	// Wait for the log tools to be stopped, or they would be left running after the exit.
	return func() tea.Msg {
		for _, session := range sessions {
			session.Stop()
		}
		for _, session := range sessions {
			_ = session.Wait()
		}
		return tea.Quit()
	}
}

// Logs ---------------------------------------------------------------------------------------

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

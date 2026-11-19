// Package tui is the dashboard: devices on the left, the log of one of them on the right.
package tui

import (
	"context"
	"errors"
	"fmt"
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
	Appearance(ctx context.Context, d device.Device) (string, error)
	SetAppearance(ctx context.Context, d device.Device, mode string) error
	SetPermission(ctx context.Context, d device.Device, id, name string, grant bool) error
	ClearData(ctx context.Context, d device.Device, id string) error
	SetLocation(ctx context.Context, d device.Device, lat, lon float64) error
	ClearLocation(ctx context.Context, d device.Device) error
	SetStatusBar(ctx context.Context, d device.Device, clean bool) error
	OpenLink(ctx context.Context, d device.Device, link string) error
	Dev(ctx context.Context, d device.Device, action string, port int) error
	Crash(ctx context.Context, d device.Device, id string) (device.CrashReport, error)
	Screenshot(ctx context.Context, d device.Device, path string) (string, error)
	Report(ctx context.Context, d device.Device, app string, opts mobile.ReportOptions) (mobile.Report, error)
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
	// Version is lazymobile's own version, written in a bug report and compared with the last one
	// seen to show what is new. Changelog is the text of CHANGELOG.md.
	Version   string
	Changelog string
	// LatestVersion finds the newest release, such as "0.5.0", or "" when it is not known or the
	// check is off. Nil means the dashboard does not look.
	LatestVersion func(ctx context.Context) (string, error)
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
	// runSeen is the start of the newest run that was followed or passed over, and followTicks counts
	// the ticks between looks for a newer one.
	runSeen     time.Time
	followTicks int
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
	m.whatsNew()
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
	return tea.Batch(m.refresh(), tick(), m.checkUpdate())
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
		cmds := []tea.Cmd{tick(), m.followRun()}
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
	// A run started since the dashboard was last used here is the one to follow.
	m.runSeen = view.SavedAt
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
		for _, v := range m.logPanes {
			if v.opened && v.spec.App != "" {
				f.RememberFolderApp(m.memory.Folder, v.spec.Device.Platform, v.spec.App)
			}
		}
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
	case "up", "k", "down", "j", "g", "home", "G", "end":
		m.moveCursor(key)
		return nil
	case "r":
		m.setNote(refreshingNote, false)
		return m.refresh()
	case "n":
		return m.create()
	case "w":
		return m.wireless()
	}
	return m.deviceAction(key)
}

func (m *Model) moveCursor(key string) {
	switch key {
	case "up", "k":
		m.cursor = max(m.cursor-1, 0)
	case "down", "j":
		m.cursor = min(m.cursor+1, max(len(m.devices)-1, 0))
	case "g", "home":
		m.cursor = 0
	case "G", "end":
		m.cursor = max(len(m.devices)-1, 0)
	}
}

// deviceAction runs a key that acts on the selected device.
func (m *Model) deviceAction(key string) tea.Cmd {
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
	case "m":
		return m.toggleAppearance(d)
	case "f":
		return m.askLocation(d)
	case "c", "C":
		return m.statusBar(d, key == "c")
	case "i":
		return m.pickBuild(d)
	case "R":
		return m.projectActions(d)
	case "p":
		return m.screenshot(d)
	case "t":
		return m.openLink(d)
	case "D":
		return m.devHelpers(d)
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
	case "e", "E":
		forward := key == "e"
		if !v.jumpError(forward, rows) {
			if forward {
				m.setNote("No error below this line", false)
			} else {
				m.setNote("No error above this line", false)
			}
		}
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
			body: "Show lines that contain every word, ignoring case. -word leaves out lines with it, /regex/ is a\nregular expression, and \"two words\" keeps a space. Leave empty to show everything.",
			validate: func(value string) string {
				if _, err := logs.ParseQuery(value); err != nil {
					return err.Error()
				}
				return ""
			},
			onSubmit: func(m *Model, value string) tea.Cmd {
				v.filter = value
				v.rebuild()
				return nil
			},
		}
	case "P":
		m.presetsModal(v)
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

// errorHint is the advice part of an error, when it has one.
func errorHint(err error) string {
	var e *apperr.Error
	if errors.As(err, &e) {
		return e.Hint
	}
	return ""
}

package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/eas"
)

// Builds is the Expo project the dashboard was started in.
type Builds interface {
	Name() string
	Profiles() []string
	StartCommand(profile, platform string) []string
	List(ctx context.Context, limit int) ([]eas.Build, error)
	Download(ctx context.Context, id string) (string, error)
	Cancel(ctx context.Context, id string) error
	Start(ctx context.Context, profile, platform string) error
}

const (
	buildLimit = 20
	// eas takes seconds to start, so builds are refreshed much less often than devices.
	buildsRefreshActive = 20 * time.Second
	buildsRefreshIdle   = 2 * time.Minute
)

// buildsView is the state of the builds pane.
type buildsView struct {
	// asked is false until the user loads the builds. eas runs the project's own code and uses
	// the Expo login, so it is never started just because the dashboard opened.
	asked   bool
	loading bool
	list    []eas.Build
	err     error
	cursor  int
	last    time.Time
}

type buildsMsg struct {
	list []eas.Build
	err  error
}

func (m *Model) loadBuilds() tea.Cmd {
	v := &m.build
	if m.builds == nil || v.loading {
		return nil
	}
	v.asked, v.loading = true, true
	ctx, builds := m.ctx, m.builds
	return func() tea.Msg {
		list, err := builds.List(ctx, buildLimit)
		return buildsMsg{list, err}
	}
}

func (m *Model) setBuilds(msg buildsMsg) {
	v := &m.build
	selected := ""
	if v.cursor < len(v.list) {
		selected = v.list[v.cursor].ID
	}
	v.loading, v.last, v.err = false, m.now(), msg.err
	if msg.err == nil {
		v.list = msg.list
	}
	for i, b := range v.list {
		if b.ID == selected {
			v.cursor = i
		}
	}
	v.cursor = min(max(v.cursor, 0), max(len(v.list)-1, 0))
}

// buildsDue reports whether the builds should be refreshed now.
func (m *Model) buildsDue() bool {
	v := &m.build
	if m.builds == nil || !v.asked || v.loading {
		return false
	}
	interval := buildsRefreshIdle
	for _, b := range v.list {
		if b.Active() {
			interval = buildsRefreshActive
		}
	}
	return m.now().Sub(v.last) >= interval
}

func (m *Model) selectedBuild() (eas.Build, bool) {
	v := &m.build
	if v.cursor < 0 || v.cursor >= len(v.list) {
		return eas.Build{}, false
	}
	return v.list[v.cursor], true
}

func (m *Model) buildKey(key string) tea.Cmd {
	v := &m.build
	if !v.asked {
		if key == "enter" || key == "r" || key == "l" {
			return m.loadBuilds()
		}
		return nil
	}
	switch key {
	case "up", "k":
		v.cursor = max(v.cursor-1, 0)
		return nil
	case "down", "j":
		v.cursor = min(v.cursor+1, max(len(v.list)-1, 0))
		return nil
	case "g", "home":
		v.cursor = 0
		return nil
	case "G", "end":
		v.cursor = max(len(v.list)-1, 0)
		return nil
	case "r":
		m.setNote("Loading builds…", false)
		return m.loadBuilds()
	case "n":
		return m.newBuild()
	}

	b, ok := m.selectedBuild()
	if !ok {
		return nil
	}
	switch key {
	case "enter", "l":
		m.message("Build "+shortID(b.ID), buildDetails(b, m.now()))
	case "o":
		if b.URL == "" {
			m.setNote("eas did not report this build's web page", true)
			return nil
		}
		return m.background("Opening the build page…", func(ctx context.Context) applyMsg {
			err := m.backend.OpenURL(ctx, b.URL)
			return func(m *Model) tea.Cmd {
				if err != nil {
					m.fail("Could not open the browser", err)
				} else {
					m.setNote("Opened "+b.URL, false)
				}
				return nil
			}
		})
	case "x":
		if !b.Active() {
			m.setNote("Only a build that is waiting or running can be cancelled", true)
			return nil
		}
		m.confirm("Cancel this build?", fmt.Sprintf("%s %s build %s will be stopped on Expo's servers.", b.Platform, b.Profile, shortID(b.ID)),
			func(m *Model) tea.Cmd {
				return m.background("Cancelling the build…", func(ctx context.Context) applyMsg {
					err := m.builds.Cancel(ctx, b.ID)
					return func(m *Model) tea.Cmd {
						if err != nil {
							m.fail("Could not cancel the build", err)
							return nil
						}
						m.setNote("Cancelled build "+shortID(b.ID), false)
						return m.loadBuilds()
					}
				})
			})
	case "i":
		return m.installBuild(b)
	}
	return nil
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// installBuild asks which running device should get the build, then downloads and installs it.
func (m *Model) installBuild(b eas.Build) tea.Cmd {
	if ok, reason := b.Installable(); !ok {
		m.message("This build cannot be installed", reason)
		return nil
	}
	var targets []device.Device
	for _, d := range m.devices {
		if !d.Ready() || string(d.Platform) != b.Platform {
			continue
		}
		// A simulator build runs only in a simulator.
		if d.Platform == device.IOS && d.Kind != device.Simulator {
			continue
		}
		targets = append(targets, d)
	}
	if len(targets) == 0 {
		kind := "Android emulator or phone"
		if b.Platform == "ios" {
			kind = "iOS simulator"
		}
		m.message("No device to install on", "This build needs a running "+kind+". Boot or connect one, then try again.")
		return nil
	}
	names := make([]string, len(targets))
	for i, d := range targets {
		names[i] = d.Name
	}
	m.modal = &modal{
		kind: modalChoice, title: "Install on which device?", items: names,
		onChoose: func(m *Model, index int) tea.Cmd {
			d := targets[index]
			return m.act(d, "Installing on", "Installed the build on", nil, func(ctx context.Context) error {
				path, err := m.builds.Download(ctx, b.ID)
				if err != nil {
					return err
				}
				return m.backend.Install(ctx, d, path)
			})
		},
	}
	return nil
}

// newBuild asks for a profile and platform, shows the exact command, and starts the build.
func (m *Model) newBuild() tea.Cmd {
	profiles := m.builds.Profiles()
	if len(profiles) == 0 {
		m.message("No build profiles", "eas.json in "+m.builds.Name()+" has no \"build\" profiles.")
		return nil
	}
	m.modal = &modal{
		kind: modalChoice, title: "Build profile", items: profiles,
		onChoose: func(m *Model, index int) tea.Cmd {
			profile := profiles[index]
			m.modal = &modal{
				kind: modalChoice, title: "Platform for the " + profile + " build", items: []string{"Android", "iOS"},
				onChoose: func(m *Model, index int) tea.Cmd {
					m.confirmBuild(profile, []string{"android", "ios"}[index])
					return nil
				},
			}
			return nil
		},
	}
	return nil
}

func (m *Model) confirmBuild(profile, platform string) {
	argv := m.builds.StartCommand(profile, platform)
	if argv == nil {
		m.message("eas was not found", "Install it with: npm install --global eas-cli")
		return
	}
	body := fmt.Sprintf("This starts a %s build for %s on Expo's servers. It uses build time on your Expo account.\n\nIt runs:\n%s",
		profile, platform, strings.Join(argv, " "))
	start := func(m *Model) tea.Cmd {
		return m.background("Starting the "+profile+" build for "+platform+"…", func(ctx context.Context) applyMsg {
			err := m.builds.Start(ctx, profile, platform)
			return func(m *Model) tea.Cmd {
				if err != nil {
					m.fail("Could not start the build", err)
					return nil
				}
				m.setNote("Started the "+profile+" build for "+platform, false)
				return m.loadBuilds()
			}
		})
	}
	// A production build is the one that reaches users, so it takes more than one key.
	if strings.Contains(strings.ToLower(profile), "prod") {
		m.modal = &modal{
			kind: modalPrompt, title: "Start a " + profile + " build?",
			body: body + "\n\nType the profile name to confirm.",
			validate: func(value string) string {
				if value != profile {
					return "Type " + profile + " exactly, or press esc to cancel."
				}
				return ""
			},
			onSubmit: func(m *Model, _ string) tea.Cmd { return start(m) },
		}
		return
	}
	m.confirm("Start this build?", body, start)
}

func buildStatus(b eas.Build) string {
	switch b.Status {
	case eas.StatusFinished:
		return "finished"
	case eas.StatusErrored:
		return "failed"
	case eas.StatusCanceled:
		return "cancelled"
	case eas.StatusInProgress:
		return "building"
	case eas.StatusInQueue, eas.StatusNew:
		return "in queue"
	case eas.StatusPendingCancel:
		return "cancelling"
	}
	return strings.ToLower(b.Status)
}

func buildMark(b eas.Build) string {
	switch {
	case b.Active():
		return yellow.Render("◐")
	case b.Status == eas.StatusFinished:
		return green.Render("✓")
	case b.Status == eas.StatusErrored:
		return red.Render("✗")
	}
	return dim.Render("○")
}

// ago is a short "how long ago", such as "5m" or "3d".
func ago(now, then time.Time) string {
	if then.IsZero() {
		return ""
	}
	d := now.Sub(then)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func buildDetails(b eas.Build, now time.Time) string {
	var lines []string
	add := func(label, value string) {
		if value != "" {
			lines = append(lines, fmt.Sprintf("%-10s %s", label, value))
		}
	}
	add("Status", buildStatus(b))
	add("Platform", b.Platform)
	add("Profile", b.Profile)
	version := b.AppVersion
	if b.AppBuildVersion != "" {
		version = strings.TrimSpace(version + " (" + b.AppBuildVersion + ")")
	}
	add("Version", version)
	add("Commit", strings.TrimSpace(shortID(b.CommitHash)+" "+b.CommitMessage))
	add("Message", b.Message)
	add("Started by", b.StartedBy)
	if !b.CreatedAt.IsZero() {
		add("Created", b.CreatedAt.Local().Format("2 Jan 15:04")+" ("+ago(now, b.CreatedAt)+" ago)")
	}
	if !b.CompletedAt.IsZero() && !b.CreatedAt.IsZero() {
		add("Took", b.CompletedAt.Sub(b.CreatedAt).Round(time.Second).String())
	}
	add("Error", b.Error)
	add("Id", b.ID)
	add("Page", b.URL)
	if ok, reason := b.Installable(); !ok && b.Status == eas.StatusFinished {
		lines = append(lines, "", reason)
	}
	return strings.Join(lines, "\n")
}

func (m *Model) buildsTitle() string {
	return "Builds: " + m.builds.Name()
}

// buildLines draws the builds pane for this much room inside its border.
func (m *Model) buildLines(width, height int) []string {
	v := &m.build
	switch {
	case !v.asked:
		return []string{
			"", " Press enter to load the builds of " + m.builds.Name() + ".",
			dim.Render(" This runs eas with your Expo login."),
		}
	case v.loading && v.list == nil && v.err == nil:
		return []string{"", dim.Render(" Loading builds…")}
	}
	var lines []string
	if v.err != nil {
		for i, part := range strings.Split(ansi.Wordwrap(firstLine(v.err.Error()), max(width-4, 10), " "), "\n") {
			mark := "   "
			if i == 0 {
				mark = " ! "
			}
			lines = append(lines, red.Render(mark+part))
		}
		if hint := errorHint(v.err); hint != "" {
			lines = append(lines, dim.Render("   "+hint))
		}
		lines = append(lines, "")
	}
	if len(v.list) == 0 && v.err == nil {
		lines = append(lines, "", " No builds yet.", dim.Render(" Press n to start one."))
	}

	const platformWidth, statusWidth, ageWidth = 7, 10, 4
	profileWidth := max(width-4-platformWidth-statusWidth-ageWidth-3, 4)
	cursorLine := 0
	now := m.now()
	for i, b := range v.list {
		text := fit(b.Platform, platformWidth) + " " + fit(b.Profile, profileWidth) + " " +
			fit(buildStatus(b), statusWidth) + " " + fit(ago(now, b.CreatedAt), ageWidth)
		selected := i == v.cursor
		switch {
		case selected && m.focus == paneBuilds:
			cursorLine = len(lines)
			text = chosen.Render(text)
		case selected:
			cursorLine = len(lines)
			text = bold.Render(text)
		}
		lines = append(lines, " "+buildMark(b)+" "+text)
	}
	if len(lines) > height && height > 0 {
		first := min(max(cursorLine-height/2, 0), len(lines)-height)
		lines = lines[first : first+height]
	}
	return lines
}

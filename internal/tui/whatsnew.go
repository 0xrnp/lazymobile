package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/0xrnp/lazymobile/internal/changelog"
	"github.com/0xrnp/lazymobile/internal/state"
)

// maxNewVersions is how many versions' notes are shown after an upgrade over several versions.
const maxNewVersions = 3

// whatsNew shows the notes of what changed since the last release the dashboard was started with,
// once. A first start, a build in between releases and a version that is not newer than the last
// seen show nothing. The notes are built into the program, so no network is used.
func (m *Model) whatsNew() {
	version := m.memory.Version
	if !changelog.Released(version) {
		return
	}
	seen := m.memory.Store.Load().SeenVersion
	_ = m.memory.Store.Update(func(f *state.File) {
		if changelog.Compare(version, f.SeenVersion) > 0 {
			f.SeenVersion = changelog.Normalize(version)
		}
	})
	entries := changelog.Between(changelog.Parse(m.memory.Changelog), seen, version)
	if len(entries) == 0 {
		return
	}
	more := ""
	if len(entries) > maxNewVersions {
		entries, more = entries[:maxNewVersions], "\n\nOlder versions: lazymobile changelog --all"
	}
	texts := make([]string, len(entries))
	for i, e := range entries {
		texts[i] = e.Text()
	}
	m.message("What's new in lazymobile "+changelog.Normalize(version), strings.Join(texts, "\n\n")+more)
}

// checkUpdate looks for a newer release in the background, and says so in a note when there is one.
// Only a release looks, not a build in between, and the look is made at most once a day.
func (m *Model) checkUpdate() tea.Cmd {
	version := m.memory.Version
	if m.memory.LatestVersion == nil || !changelog.Released(version) {
		return nil
	}
	ctx, latest := m.ctx, m.memory.LatestVersion
	return func() tea.Msg {
		newest, err := latest(ctx)
		return applyMsg(func(m *Model) tea.Cmd {
			// A failed look is left alone: it says nothing the user can act on.
			if err == nil && changelog.Released(newest) && changelog.Compare(newest, version) > 0 && m.note == "" {
				m.setNote("lazymobile "+changelog.Normalize(newest)+" is out, you have "+changelog.Normalize(version)+
					". brew upgrade lazymobile, or github.com/0xrnp/lazymobile/releases", false)
			}
			return nil
		})
	}
}

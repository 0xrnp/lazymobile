package tui

import (
	"strings"

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

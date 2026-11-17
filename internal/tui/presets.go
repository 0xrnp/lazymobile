package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/0xrnp/lazymobile/internal/state"
)

// presetsModal lists the filters saved for the app of log v. Enter applies one, x forgets it, and
// the last row saves the filter and level the log has now.
func (m *Model) presetsModal(v *logView) {
	key := state.PresetKey(v.spec.Device.Platform, v.spec.App)
	saved := m.memory.Store.Load().Presets[key]
	items := make([]string, 0, len(saved)+1)
	notes := make([]string, 0, len(saved)+1)
	for _, p := range saved {
		text := p.Filter
		if text == "" {
			text = "(no filter)"
		}
		note := ""
		if p.Level != 0 {
			note = p.Level.String() + "+"
		}
		items, notes = append(items, text), append(notes, note)
	}
	current := state.Preset{Filter: v.filter, Level: v.level}
	items, notes = append(items, "Save the current filter"), append(notes, presetNote(current))
	app := v.spec.App
	if app == "" {
		app = "every app"
	}
	m.modal = &modal{
		kind: modalChoice, title: "Filters for " + app, items: items, notes: notes,
		extraHint: "x: forget",
		onKey: func(m *Model, key string, index int) tea.Cmd {
			if key == "x" && index < len(saved) {
				_ = m.memory.Store.Update(func(f *state.File) { f.DeletePreset(presetKey(v), saved[index]) })
				m.presetsModal(v)
			}
			return nil
		},
		onChoose: func(m *Model, index int) tea.Cmd {
			if index < len(saved) {
				v.filter, v.level, v.levelChosen = saved[index].Filter, saved[index].Level, true
				v.rebuild()
				return nil
			}
			if current == (state.Preset{}) {
				m.message("Nothing to save", "Set a filter with / or a level with L first.")
				return nil
			}
			_ = m.memory.Store.Update(func(f *state.File) { f.SavePreset(presetKey(v), current) })
			m.setNote("Saved the filter for "+app, false)
			return nil
		},
	}
}

func presetKey(v *logView) string {
	return state.PresetKey(v.spec.Device.Platform, v.spec.App)
}

// presetNote describes a preset in a few words.
func presetNote(p state.Preset) string {
	var parts []string
	if p.Filter != "" {
		parts = append(parts, p.Filter)
	}
	if p.Level != 0 {
		parts = append(parts, p.Level.String()+"+")
	}
	return strings.Join(parts, "  ")
}

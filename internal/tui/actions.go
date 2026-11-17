package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/0xrnp/lazymobile/internal/actions"
	"github.com/0xrnp/lazymobile/internal/device"
)

// projectActions lets the user run one of the project's own commands from .lazymobile.json on d.
// The file is read on each press, so a change shows without restarting. Nothing runs before the
// exact command has been shown and confirmed.
func (m *Model) projectActions(d device.Device) tea.Cmd {
	all, err := actions.Load(m.memory.Folder)
	if err != nil {
		m.fail("Could not read the project actions", err)
		return nil
	}
	list := actions.For(all, d)
	if len(list) == 0 {
		m.message("No project actions", "Add \"actions\" to "+actions.ConfigFile+" in your project, for example:\n\n"+
			`{"actions": [{"name": "Flutter run", "command": ["flutter", "run", "-d", "{device_id}"]}]}`+
			"\n\n{device_id}, {platform} and {app} are replaced. See the README.")
		return nil
	}
	names := make([]string, len(list))
	notes := make([]string, len(list))
	for i, a := range list {
		names[i], notes[i] = a.Name, strings.Join(a.Command, " ")
	}
	m.modal = &modal{
		kind: modalChoice, title: "Run on " + d.Name, items: names, notes: notes,
		onChoose: func(m *Model, index int) tea.Cmd {
			m.confirmAction(list[index], d)
			return nil
		},
	}
	return nil
}

func (m *Model) confirmAction(a actions.Action, d device.Device) {
	argv, err := a.Resolve(d, m.lastApp[d.Platform])
	if err != nil {
		m.fail("Cannot run "+a.Name, err)
		return
	}
	body := fmt.Sprintf("This runs the command below on %s, with this terminal, until it ends.\n\nIt runs:\n%s\n\nIn:\n%s\n\n"+
		"The command comes from %s. Run it only if you trust this project.",
		d.Name, strings.Join(argv, " "), a.Dir, actions.ConfigFile)
	m.confirm("Run "+a.Name+"?", body, func(m *Model) tea.Cmd {
		return tea.ExecProcess(a.Prepare(m.ctx, argv), func(err error) tea.Msg {
			return applyMsg(func(m *Model) tea.Cmd {
				if err != nil {
					m.setNote(a.Name+" ended: "+firstLine(err.Error()), true)
				} else {
					m.setNote(a.Name+" finished", false)
				}
				return m.refresh()
			})
		})
	})
}

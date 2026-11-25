package tui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/0xrnp/lazymobile/internal/device"
)

// askMedia asks for the path of a photo or video, and adds it to the selected simulator or emulator.
func (m *Model) askMedia(d device.Device) tea.Cmd {
	if problem := virtualOnlyRunning(d); problem != "" {
		m.setNote(problem, true)
		return nil
	}
	m.modal = &modal{
		kind: modalPrompt, title: "Add a photo or video to " + d.Name,
		body: "The path of a file on this computer, such as ~/Pictures/receipt.png.",
		validate: func(value string) string {
			if strings.TrimSpace(value) == "" {
				return "Type a path."
			}
			return ""
		},
		onSubmit: func(m *Model, value string) tea.Cmd {
			backend := m.backend
			path := strings.TrimSpace(value)
			return m.act(d, "Adding the file to", "Added the file to", nil, func(ctx context.Context) error {
				_, err := backend.AddMedia(ctx, d, []string{expandHome(path)})
				return err
			})
		},
	}
	return nil
}

// clipboardFeature is why the clipboard of d cannot be set, or "" when it can: only a running
// simulator has a command for it.
func clipboardFeature(d device.Device) string {
	switch {
	case !d.Ready():
		return d.Name + " is not running"
	case d.Platform != device.IOS || d.Kind != device.Simulator:
		return "Only an iOS simulator's clipboard can be set from here"
	}
	return ""
}

// askClipboard asks for text, and puts it on the selected simulator's clipboard.
func (m *Model) askClipboard(d device.Device) tea.Cmd {
	if problem := clipboardFeature(d); problem != "" {
		m.setNote(problem, true)
		return nil
	}
	m.modal = &modal{
		kind: modalPrompt, title: "Copy text to " + d.Name, body: "It is put on the simulator's clipboard, ready to paste into a field.",
		validate: func(value string) string {
			if value == "" {
				return "Type some text."
			}
			return ""
		},
		onSubmit: func(m *Model, value string) tea.Cmd {
			backend := m.backend
			return m.act(d, "Copying text to", "Copied the text to", nil, func(ctx context.Context) error {
				return backend.SetClipboard(ctx, d, value)
			})
		},
	}
	return nil
}

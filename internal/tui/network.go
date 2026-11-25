package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/0xrnp/lazymobile/internal/device"
)

// networkMode is one way to set an emulator's network, in the order the window lists them.
type networkMode struct{ mode, label string }

var networkModes = []networkMode{
	{"offline", "No connection (airplane mode)"},
	{"gsm", "GSM: 14 kbit/s, 150 to 550 ms delay"},
	{"edge", "EDGE: 237 kbit/s, 80 to 400 ms delay"},
	{"3g", "3G (UMTS): 1.9 Mbit/s, 35 to 200 ms delay"},
	{"lte", "LTE: 173 Mbit/s"},
	{"online", "Back to normal"},
}

// networkFeature is why the network of d cannot be changed, or "" when it can: only a running
// emulator has a console to ask.
func networkFeature(d device.Device) string {
	switch {
	case !d.Ready():
		return d.Name + " is not running"
	case d.Platform != device.Android || d.Kind != device.Emulator:
		return "Only an Android emulator can be given a slow or missing network"
	}
	return ""
}

// networkChoices offers the network modes of the selected Android emulator.
func (m *Model) networkChoices(d device.Device) tea.Cmd {
	if problem := networkFeature(d); problem != "" {
		m.setNote(problem, true)
		return nil
	}
	labels := make([]string, len(networkModes))
	for i, n := range networkModes {
		labels[i] = n.label
	}
	m.modal = &modal{
		kind: modalChoice, title: "Network of " + d.Name, items: labels,
		onChoose: func(m *Model, index int) tea.Cmd {
			mode := networkModes[index].mode
			backend := m.backend
			return m.act(d, "Changing the network of", "Changed the network of", nil, func(ctx context.Context) error {
				return backend.SetNetwork(ctx, d, mode)
			})
		},
	}
	return nil
}

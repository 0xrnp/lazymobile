package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/mobile"
)

// devHelpers offers what helps an app in development on an Android device: reach the dev server
// on this computer, open the app's developer menu, and reload it.
func (m *Model) devHelpers(d device.Device) tea.Cmd {
	if problem := devFeature(d); problem != "" {
		m.setNote(problem, true)
		return nil
	}
	type helper struct{ label, action, doing, done string }
	helpers := []helper{
		{"Reach this computer on port 8081 (Metro)", "reverse", "Forwarding port 8081 from", "Forwarded port 8081 from"},
		{"Open the developer menu", "menu", "Opening the developer menu on", "Opened the developer menu on"},
		{"Reload the app", "reload", "Reloading the app on", "Reloaded the app on"},
	}
	labels := make([]string, len(helpers))
	for i, h := range helpers {
		labels[i] = h.label
	}
	m.modal = &modal{
		kind: modalChoice, title: "Help the app on " + d.Name, items: labels,
		onChoose: func(m *Model, index int) tea.Cmd {
			h := helpers[index]
			backend := m.backend
			return m.act(d, h.doing, h.done, nil, func(ctx context.Context) error {
				return backend.Dev(ctx, d, h.action, mobile.DefaultDevPort)
			})
		},
	}
	return nil
}

// devFeature is why d cannot be helped this way, or "" when it can.
func devFeature(d device.Device) string {
	switch {
	case !d.Ready():
		return d.Name + " is not running"
	case d.Platform != device.Android:
		return d.Name + " is not an Android device: only Android can be sent a key from here"
	}
	return ""
}

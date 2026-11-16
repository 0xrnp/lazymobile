package tui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/0xrnp/lazymobile/internal/android"
)

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

package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"
	"github.com/0xrnp/lazymobile/internal/android"
	"github.com/0xrnp/lazymobile/internal/ios"
)

func (m *Model) create() tea.Cmd {
	if !m.backend.HasIOS() {
		return m.createAVD()
	}
	m.modal = &modal{
		kind: modalChoice, title: "New device", items: []string{"iOS simulator", "Android emulator"},
		onChoose: func(m *Model, index int) tea.Cmd {
			if index == 0 {
				return m.createSimulator()
			}
			return m.createAVD()
		},
	}
	return nil
}

func (m *Model) message(title, body string) {
	m.modal = &modal{kind: modalMessage, title: title, body: body}
}

func (m *Model) createSimulator() tea.Cmd {
	return m.background("Reading the installed iOS versions…", func(ctx context.Context) applyMsg {
		runtimes, err := m.backend.Runtimes(ctx)
		return func(m *Model) tea.Cmd {
			m.setNote("", false)
			switch {
			case err != nil:
				m.fail("Could not read the iOS versions", err)
				return nil
			case len(runtimes) == 0:
				m.message("No iOS version is installed", "Install one in Xcode > Settings > Components, then try again.")
				return nil
			}
			names := make([]string, len(runtimes))
			for i, r := range runtimes {
				names[i] = r.Name
			}
			m.modal = &modal{
				kind: modalChoice, title: "iOS version", items: names,
				onChoose: func(m *Model, index int) tea.Cmd {
					m.chooseSimulatorType(runtimes[index])
					return nil
				},
			}
			return nil
		}
	})
}

func (m *Model) chooseSimulatorType(runtime ios.Runtime) {
	if len(runtime.DeviceTypes) == 0 {
		m.message("No device types", "Xcode reports no iPhone or iPad for "+runtime.Name+".")
		return
	}
	names := make([]string, len(runtime.DeviceTypes))
	for i, t := range runtime.DeviceTypes {
		names[i] = t.Name
	}
	m.modal = &modal{
		kind: modalChoice, title: "Device for " + runtime.Name, items: names,
		onChoose: func(m *Model, index int) tea.Cmd {
			deviceType := runtime.DeviceTypes[index]
			m.modal = &modal{
				kind: modalPrompt, title: "Name of the new simulator", value: deviceType.Name,
				validate: func(value string) string {
					if value == "" {
						return "A name is needed."
					}
					return ""
				},
				onSubmit: func(m *Model, name string) tea.Cmd {
					return m.background("Creating "+name+"…", func(ctx context.Context) applyMsg {
						err := m.backend.CreateSimulator(ctx, name, deviceType.ID, runtime.ID)
						return created(name, err)
					})
				},
			}
			return nil
		},
	}
}

func created(name string, err error) applyMsg {
	return func(m *Model) tea.Cmd {
		if err != nil {
			m.fail("Could not create "+name, err)
			return nil
		}
		m.setNote("Created "+name, false)
		return m.refresh()
	}
}

func (m *Model) createAVD() tea.Cmd {
	images := m.backend.SystemImages()
	if len(images) == 0 {
		m.message("No Android system image is installed",
			"An emulator needs a system image that runs on this machine.\n"+
				"Install one in Android Studio > SDK Manager > SDK Platforms (tick \"Show Package Details\"), "+
				"or with sdkmanager, then try again.")
		return nil
	}
	labels := make([]string, len(images))
	for i, image := range images {
		labels[i] = image.Label
	}
	m.modal = &modal{
		kind: modalChoice, title: "Android version", items: labels,
		onChoose: func(m *Model, index int) tea.Cmd {
			image := images[index]
			return m.background("Reading the device profiles…", func(ctx context.Context) applyMsg {
				profiles, err := m.backend.DeviceProfiles(ctx)
				return func(m *Model) tea.Cmd {
					m.setNote("", false)
					if err != nil {
						m.fail("Could not read the device profiles", err)
						return nil
					}
					m.chooseProfile(image, profiles)
					return nil
				}
			})
		},
	}
	return nil
}

func (m *Model) chooseProfile(image android.SystemImage, profiles []string) {
	items := append([]string{"Default hardware"}, profiles...)
	m.modal = &modal{
		kind: modalChoice, title: "Hardware profile", items: items,
		onChoose: func(m *Model, index int) tea.Cmd {
			profile := ""
			suggested := "Android_" + image.API
			if index > 0 {
				profile = profiles[index-1]
				suggested = profile + "_API_" + image.API
			}
			m.modal = &modal{
				kind: modalPrompt, title: "Name of the new emulator", value: suggested,
				body: "Letters, digits, dots, dashes and underscores.",
				validate: func(value string) string {
					if !android.ValidAVDName(value) {
						return "Use letters, digits, dots, dashes and underscores, starting with a letter or digit."
					}
					return ""
				},
				onSubmit: func(m *Model, name string) tea.Cmd {
					return m.background("Creating "+name+"…", func(ctx context.Context) applyMsg {
						return created(name, m.backend.CreateAVD(ctx, name, image.ID, profile))
					})
				},
			}
			return nil
		},
	}
}

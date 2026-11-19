package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/ios"
)

// listedUnavailable is how many unavailable simulators the confirmation names before "and N more".
const listedUnavailable = 8

func unavailableBytes(list []ios.Unavailable) int64 {
	var total int64
	for _, u := range list {
		total += u.DataBytes
	}
	return total
}

// unavailableLine is the sentence the devices pane shows: "2 simulators Xcode can no longer run use 1.9 GB".
func unavailableLine(list []ios.Unavailable) string {
	text := fmt.Sprintf("%d simulators Xcode can no longer run", len(list))
	if len(list) == 1 {
		text = "1 simulator Xcode can no longer run"
	}
	if total := unavailableBytes(list); total > 0 {
		verb := "use"
		if len(list) == 1 {
			verb = "uses"
		}
		text += " " + verb + " " + device.FormatBytes(total)
	}
	return text
}

// unavailableTitle is the question's title: "Delete 10 unavailable simulators (2.0 GB)?".
func unavailableTitle(list []ios.Unavailable) string {
	title := fmt.Sprintf("Delete %d unavailable simulator", len(list))
	if len(list) != 1 {
		title += "s"
	}
	if total := unavailableBytes(list); total > 0 {
		title += " (" + device.FormatBytes(total) + ")"
	}
	return title + "?"
}

// askDeleteUnavailable asks before deleting the simulators Xcode can no longer run. They are
// listed with their sizes, since the data in them cannot be had back.
func (m *Model) askDeleteUnavailable() tea.Cmd {
	list := m.unavailable
	if len(list) == 0 {
		m.setNote("There are no unavailable simulators", false)
		return nil
	}
	var body strings.Builder
	body.WriteString("Their runtime is gone, as after an Xcode update, so they cannot start. Deleting them frees the disk, and their data cannot be had back.\n")
	for i, u := range list {
		if i == listedUnavailable {
			fmt.Fprintf(&body, "\n  and %d more", len(list)-i)
			break
		}
		line := u.Name
		if u.Platform != "" || u.OSVersion != "" {
			line += ", " + strings.TrimSpace(u.Platform+" "+u.OSVersion)
		}
		if u.DataBytes > 0 {
			line += ", " + device.FormatBytes(u.DataBytes)
		}
		body.WriteString("\n  " + line)
	}
	ids := make([]string, len(list))
	for i, u := range list {
		ids[i] = u.ID
	}
	m.confirm(unavailableTitle(list), body.String(), func(m *Model) tea.Cmd {
		backend := m.backend
		return m.background("Deleting the unavailable simulators…", func(ctx context.Context) applyMsg {
			// Only what was named is deleted: the backend refuses if there are more by now.
			deleted, err := backend.DeleteUnavailable(ctx, ids)
			return applyMsg(func(m *Model) tea.Cmd {
				if err != nil {
					m.fail("Could not delete the unavailable simulators", err)
					return m.refresh()
				}
				text := fmt.Sprintf("Deleted %d unavailable simulator", len(deleted))
				if len(deleted) != 1 {
					text += "s"
				}
				if freed := unavailableBytes(deleted); freed > 0 {
					text += ", freed " + device.FormatBytes(freed)
				}
				m.setNote(text, false)
				return m.refresh()
			})
		})
	})
	return nil
}

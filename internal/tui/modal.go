package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type modalKind int

const (
	modalMessage modalKind = iota
	modalConfirm
	modalPrompt
	modalChoice
	modalKeys
)

// modal is a small window over the dashboard that takes all keys until it closes.
type modal struct {
	kind  modalKind
	title string
	// body explains what is being asked or reported.
	body string

	// Confirm.
	onYes func(m *Model) tea.Cmd

	// Prompt.
	value string
	// validate returns what is wrong with the value, or "".
	validate func(value string) string
	problem  string
	onSubmit func(m *Model, value string) tea.Cmd

	// Choice.
	items    []string
	cursor   int
	onChoose func(m *Model, index int) tea.Cmd
	// onKey, when set, sees the keys the list does not use. extraHint says what they do.
	onKey     func(m *Model, key string, cursor int) tea.Cmd
	extraHint string

	// Keys.
	keys *keysMenu

	// top is the first body line shown, for a message that is taller than the screen.
	top int
}

// innerWidth is the room inside the modal's border on a screen this wide.
func innerWidth(width int) int {
	return min(max(width-8, 20), 76)
}

// bodyLines is the body wrapped to fit inside the border.
func (md *modal) bodyLines(inner int) []string {
	if md.body == "" {
		return nil
	}
	var lines []string
	for _, paragraph := range strings.Split(md.body, "\n") {
		for _, line := range strings.Split(ansi.Wordwrap(paragraph, inner-2, " "), "\n") {
			lines = append(lines, " "+line)
		}
	}
	return lines
}

// messageRows is how many body lines of a message fit: the border, a blank line and the hint
// take four rows.
func messageRows(height int) int {
	return max(height-4, 1)
}

// maxTop is how far a message can be scrolled. It is zero when the whole message fits.
func (md *modal) maxTop(width, height int) int {
	if md.kind != modalMessage {
		return 0
	}
	return max(len(md.bodyLines(innerWidth(width)))-messageRows(height), 0)
}

// key handles a key press while the modal is open.
func (m *Model) modalKey(msg tea.KeyPressMsg) tea.Cmd {
	md := m.modal
	key := msg.String()
	switch md.kind {
	case modalMessage:
		height := m.layout().bodyHeight
		last := md.maxTop(m.width, height)
		page := max(messageRows(height)/2, 1)
		step, scrolls := map[string]int{
			"up": -1, "k": -1, "down": 1, "j": 1,
			"pgup": -page, "ctrl+u": -page, "pgdown": page, "ctrl+d": page, "space": page,
			"g": -last, "home": -last, "G": last, "end": last,
		}[key]
		if last == 0 || !scrolls {
			m.modal = nil
			return nil
		}
		md.top = min(max(min(md.top, last)+step, 0), last)
	case modalConfirm:
		switch key {
		case "y", "Y":
			m.modal = nil
			return md.onYes(m)
		case "n", "N", "esc", "q", "ctrl+c":
			m.modal = nil
			m.setNote("Cancelled", false)
		}
	case modalPrompt:
		switch key {
		case "esc", "ctrl+c":
			m.modal = nil
		case "enter":
			value := strings.TrimSpace(md.value)
			if md.validate != nil {
				if md.problem = md.validate(value); md.problem != "" {
					return nil
				}
			}
			m.modal = nil
			return md.onSubmit(m, value)
		case "backspace":
			if runes := []rune(md.value); len(runes) > 0 {
				md.value = string(runes[:len(runes)-1])
			}
			md.problem = ""
		case "ctrl+u":
			md.value, md.problem = "", ""
		default:
			if text := msg.Key().Text; text != "" {
				md.value += text
				md.problem = ""
			}
		}
	case modalKeys:
		return m.keysMenuKey(msg)
	case modalChoice:
		switch key {
		case "esc", "q", "ctrl+c":
			m.modal = nil
		case "up", "k":
			md.cursor = max(md.cursor-1, 0)
		case "down", "j":
			md.cursor = min(md.cursor+1, len(md.items)-1)
		case "g", "home":
			md.cursor = 0
		case "G", "end":
			md.cursor = len(md.items) - 1
		case "enter":
			m.modal = nil
			return md.onChoose(m, md.cursor)
		default:
			if md.onKey != nil {
				return md.onKey(m, key, md.cursor)
			}
		}
	}
	return nil
}

// render draws the modal's window for a width by height screen. The caller places it.
func (md *modal) render(width, height int) []string {
	inner := innerWidth(width)
	if md.kind == modalKeys {
		return md.keys.render(inner, height)
	}
	lines := md.bodyLines(inner)
	last := md.maxTop(width, height)
	top := min(md.top, last)
	if last > 0 {
		lines = lines[top : top+messageRows(height)]
	}
	if len(lines) > 0 {
		lines = append(lines, "")
	}

	var hint string
	switch md.kind {
	case modalMessage:
		hint = "any key: close"
		if last > 0 {
			hint = fmt.Sprintf("%d%%   j k: scroll   esc: close", top*100/last)
		}
	case modalConfirm:
		hint = "y: yes   n: no"
	case modalPrompt:
		lines = append(lines, " "+cyan.Render("> ")+ansi.TruncateLeft(md.value, max(ansi.StringWidth(md.value)-(inner-6), 0), "")+chosen.Render(" "))
		if md.problem != "" {
			for _, line := range strings.Split(ansi.Wordwrap(md.problem, inner-2, " "), "\n") {
				lines = append(lines, " "+red.Render(line))
			}
		}
		lines = append(lines, "")
		hint = "enter: ok   esc: cancel"
	case modalChoice:
		// Keep the cursor in view when the list is longer than the window.
		rows := max(min(len(md.items), height-8-len(lines)), 1)
		first := min(max(md.cursor-rows/2, 0), max(len(md.items)-rows, 0))
		for i := first; i < first+rows && i < len(md.items); i++ {
			row := fit(" "+md.items[i], inner-1)
			if i == md.cursor {
				row = chosen.Render(row)
			}
			lines = append(lines, row)
		}
		lines = append(lines, "")
		hint = "enter: choose   esc: cancel"
		if md.extraHint != "" {
			hint = "enter: choose   " + md.extraHint + "   esc: cancel"
		}
		if len(md.items) > rows {
			hint += "   " + fmt.Sprintf("%d of %d", md.cursor+1, len(md.items))
		}
	}
	lines = append(lines, " "+dim.Render(hint))

	return box(md.title, lines, inner+2, min(len(lines)+2, height), true)
}

// overlay draws a window centred over the screen behind it. The screen stays visible but
// dimmed, so the window stands out and it is clear the keys go to it.
func overlay(background, window []string, width int) []string {
	if len(window) == 0 {
		return background
	}
	windowWidth := ansi.StringWidth(window[0])
	left := max((width-windowWidth)/2, 0)
	right := max(width-left-windowWidth, 0)
	top := max((len(background)-len(window))/2, 0)
	out := make([]string, len(background))
	for i, line := range background {
		plain := fit(ansi.Strip(line), width)
		if i < top || i >= top+len(window) {
			out[i] = dim.Render(plain)
			continue
		}
		// A wide character cut in half is dropped on the left and kept on the right, so the left
		// is padded back and the right is trimmed until it fits.
		before := fit(ansi.Truncate(plain, left, ""), left)
		after := ansi.TruncateLeft(plain, left+windowWidth, "")
		for ansi.StringWidth(after) > right {
			_, size := utf8.DecodeRuneInString(after)
			after = after[size:]
		}
		after = strings.Repeat(" ", max(right-ansi.StringWidth(after), 0)) + after
		out[i] = dim.Render(before) + window[i-top] + dim.Render(after)
	}
	return out
}

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
	// notes, when set, has a dim note for each item, shown after it.
	notes []string
	// filterable lets `/` narrow the list by text. The last keep items stay in the list whatever
	// the text is, for choices such as "Type an app id…".
	filterable bool
	keep       int
	filter     string
	filtering  bool

	// Keys.
	keys *keysMenu

	// top is the first body line shown, for a message that is taller than the screen.
	top int
}

// visible is the indexes of the items shown: those that match the filter, then the kept ones.
func (md *modal) visible() []int {
	out := make([]int, 0, len(md.items))
	matching := len(md.items) - md.keep
	find := strings.ToLower(md.filter)
	for i, item := range md.items {
		if i >= matching || find == "" {
			out = append(out, i)
			continue
		}
		text := item
		if i < len(md.notes) {
			text += " " + md.notes[i]
		}
		if strings.Contains(strings.ToLower(text), find) {
			out = append(out, i)
		}
	}
	return out
}

// setFilter changes the filter and puts the cursor back on the first match.
func (md *modal) setFilter(filter string) {
	md.filter, md.cursor = filter, 0
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
		// A word longer than the line, such as a frame of a stack trace, is broken rather than cut,
		// so none of it is lost. A tab, which starts each frame of a Java trace, is a fixed indent.
		paragraph = strings.ReplaceAll(paragraph, "\t", "    ")
		for _, line := range strings.Split(ansi.Wrap(paragraph, inner-2, " "), "\n") {
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
		return m.choiceKey(msg)
	}
	return nil
}

// choiceKey handles a key press in a list of choices.
func (m *Model) choiceKey(msg tea.KeyPressMsg) tea.Cmd {
	md := m.modal
	key := msg.String()
	shown := md.visible()
	choose := func() tea.Cmd {
		if md.cursor >= len(shown) {
			return nil
		}
		m.modal = nil
		return md.onChoose(m, shown[md.cursor])
	}
	if md.filtering {
		switch key {
		case "esc":
			md.setFilter("")
			md.filtering = false
		case "enter":
			return choose()
		case "up":
			md.cursor = max(md.cursor-1, 0)
		case "down":
			md.cursor = min(md.cursor+1, len(shown)-1)
		case "backspace":
			if runes := []rune(md.filter); len(runes) > 0 {
				md.setFilter(string(runes[:len(runes)-1]))
			}
		case "ctrl+u":
			md.setFilter("")
		case "ctrl+c":
			m.modal = nil
		default:
			if text := msg.Key().Text; text != "" {
				md.setFilter(md.filter + text)
			} else if md.onKey != nil && md.cursor < len(shown) {
				return md.onKey(m, key, shown[md.cursor])
			}
		}
		return nil
	}
	switch key {
	case "esc":
		if md.filter != "" {
			md.setFilter("")
			return nil
		}
		m.modal = nil
	case "q", "ctrl+c":
		m.modal = nil
	case "/":
		if md.filterable {
			md.filtering = true
		}
	case "up", "k":
		md.cursor = max(md.cursor-1, 0)
	case "down", "j":
		md.cursor = min(md.cursor+1, len(shown)-1)
	case "g", "home":
		md.cursor = 0
	case "G", "end":
		md.cursor = len(shown) - 1
	case "enter":
		return choose()
	default:
		if md.onKey != nil && md.cursor < len(shown) {
			return md.onKey(m, key, shown[md.cursor])
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
		shown := md.visible()
		switch {
		case md.filtering:
			lines = append(lines, " "+cyan.Render("/ ")+ansi.TruncateLeft(md.filter, max(ansi.StringWidth(md.filter)-(inner-6), 0), "")+chosen.Render(" "))
		case md.filter != "":
			lines = append(lines, " "+dim.Render("filter: ")+md.filter)
		}
		if len(shown) == md.keep && md.filter != "" {
			lines = append(lines, " "+dim.Render("Nothing matches "+md.filter))
		}
		// Keep the cursor in view when the list is longer than the window.
		rows := max(min(len(shown), height-8-len(lines)), 1)
		cursor := min(md.cursor, max(len(shown)-1, 0))
		first := min(max(cursor-rows/2, 0), max(len(shown)-rows, 0))
		for pos := first; pos < first+rows && pos < len(shown); pos++ {
			i := shown[pos]
			note := ""
			if i < len(md.notes) && md.notes[i] != "" {
				note = "  " + md.notes[i]
			}
			if pos == cursor {
				lines = append(lines, chosen.Render(fit(" "+md.items[i]+note, inner-1)))
				continue
			}
			row := " " + highlight(md.items[i], md.filter, plain)
			if note != "" {
				row += "  " + highlight(strings.TrimPrefix(note, "  "), md.filter, dim)
			}
			lines = append(lines, fit(row, inner-1))
		}
		lines = append(lines, "")
		hint = "enter: choose"
		if md.filterable && !md.filtering {
			hint += "   /: filter"
		}
		if md.extraHint != "" {
			hint += "   " + md.extraHint
		}
		if md.filtering {
			hint += "   esc: clear"
		} else {
			hint += "   esc: cancel"
		}
		if len(shown) > rows {
			hint += "   " + fmt.Sprintf("%d of %d", cursor+1, len(shown))
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

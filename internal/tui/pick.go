package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// ErrCancelled is what Pick returns when the user backs out.
var ErrCancelled = errors.New("cancelled")

// PickItem is one line of a list to choose from: what it is, and a few words about it.
type PickItem struct {
	Label  string
	Detail string
}

// maxPickRows is how many lines of a long list are shown at once.
const maxPickRows = 12

// Pick shows a list in the terminal, where the command was typed and not in a full screen, and
// returns the index of the line the user chose. The cursor starts on start. It returns
// ErrCancelled when the user presses esc, q or ctrl+c.
func Pick(ctx context.Context, title string, items []PickItem, start int) (int, error) {
	if len(items) == 0 {
		return 0, errors.New("nothing to choose from")
	}
	p := &picker{title: title, items: items, cursor: min(max(start, 0), len(items)-1)}
	final, err := tea.NewProgram(p, tea.WithContext(ctx)).Run()
	if err != nil {
		if errors.Is(err, tea.ErrInterrupted) {
			return 0, ErrCancelled
		}
		return 0, err
	}
	p = final.(*picker)
	if p.cancelled {
		return 0, ErrCancelled
	}
	return p.cursor, nil
}

// picker is the list that Pick shows.
type picker struct {
	title     string
	items     []PickItem
	cursor    int
	done      bool
	cancelled bool
}

// Init implements tea.Model.
func (p *picker) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (p *picker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return p, nil
	}
	switch key.String() {
	case "enter":
		p.done = true
		return p, tea.Quit
	case "esc", "q", "ctrl+c":
		p.done, p.cancelled = true, true
		return p, tea.Quit
	case "down", "j", "tab", "ctrl+n":
		p.cursor = (p.cursor + 1) % len(p.items)
	case "up", "k", "shift+tab", "ctrl+p":
		p.cursor = (p.cursor + len(p.items) - 1) % len(p.items)
	case "g", "home":
		p.cursor = 0
	case "G", "end":
		p.cursor = len(p.items) - 1
	default:
		// A number picks that line.
		if n := key.String(); len(n) == 1 && n[0] >= '1' && n[0] <= '9' && int(n[0]-'1') < len(p.items) {
			p.cursor = int(n[0] - '1')
			p.done = true
			return p, tea.Quit
		}
	}
	return p, nil
}

// View implements tea.Model. Once a line is chosen, nothing is left on the screen, so the command's
// own output follows directly.
func (p *picker) View() tea.View {
	if p.done {
		return tea.NewView("")
	}
	var b strings.Builder
	b.WriteString(bold.Render(p.title) + "\n")
	first := min(max(p.cursor-maxPickRows/2, 0), max(len(p.items)-maxPickRows, 0))
	last := min(first+maxPickRows, len(p.items))
	if first > 0 {
		b.WriteString(dim.Render("  …") + "\n")
	}
	for i := first; i < last; i++ {
		item := p.items[i]
		number := " "
		if i < 9 {
			number = fmt.Sprint(i + 1)
		}
		line := number + " " + item.Label
		if item.Detail != "" {
			line += "  " + dim.Render(item.Detail)
		}
		if i == p.cursor {
			line = bold.Render("> ") + chosen.Render(number+" "+item.Label) + "  " + dim.Render(item.Detail)
		} else {
			line = "  " + line
		}
		b.WriteString(line + "\n")
	}
	if last < len(p.items) {
		b.WriteString(dim.Render("  …") + "\n")
	}
	b.WriteString(dim.Render("j k or arrows move, enter or a number chooses, esc cancels"))
	return tea.NewView(b.String())
}

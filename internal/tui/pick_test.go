package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func pickerWith(n int) *picker {
	items := make([]PickItem, n)
	for i := range items {
		items[i] = PickItem{Label: string(rune('a' + i)), Detail: "d"}
	}
	return &picker{title: "Choose", items: items}
}

func send(p *picker, keys ...tea.KeyPressMsg) {
	for _, k := range keys {
		p.Update(k)
	}
}

func TestPickerMovesWrapsAndChooses(t *testing.T) {
	p := pickerWith(3)
	send(p, tea.KeyPressMsg{Code: tea.KeyUp})
	if p.cursor != 2 {
		t.Errorf("up from the top should wrap to the last line, cursor = %d", p.cursor)
	}
	send(p, tea.KeyPressMsg{Code: tea.KeyDown}, tea.KeyPressMsg{Code: 'j', Text: "j"}, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !p.done || p.cancelled || p.cursor != 1 {
		t.Errorf("done %v cancelled %v cursor %d", p.done, p.cancelled, p.cursor)
	}
}

func TestPickerNumberChoosesThatLineAndIgnoresOnesThatAreNotThere(t *testing.T) {
	p := pickerWith(3)
	send(p, tea.KeyPressMsg{Code: '9', Text: "9"})
	if p.done {
		t.Error("9 chose a line that is not there")
	}
	send(p, tea.KeyPressMsg{Code: '2', Text: "2"})
	if !p.done || p.cursor != 1 {
		t.Errorf("done %v cursor %d", p.done, p.cursor)
	}
}

func TestPickerCancels(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{{Code: tea.KeyEscape}, {Code: 'q', Text: "q"}, {Code: 'c', Mod: tea.ModCtrl}} {
		p := pickerWith(2)
		send(p, key)
		if !p.cancelled {
			t.Errorf("%v did not cancel", key)
		}
	}
}

func TestPickerLeavesNothingOnTheScreenOnceChosenAndWindowsLongLists(t *testing.T) {
	p := pickerWith(30)
	p.cursor = 20
	view := p.View().Content
	if lines := strings.Count(view, "\n"); lines > maxPickRows+4 {
		t.Errorf("%d lines for a list of 30", lines)
	}
	if !strings.Contains(view, "u ") && !strings.Contains(view, "u\x1b") {
		t.Errorf("the chosen line (u) is not shown: %q", view)
	}
	p.done = true
	if got := p.View().Content; got != "" {
		t.Errorf("a finished picker still draws %q", got)
	}
}

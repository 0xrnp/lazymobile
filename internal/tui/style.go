package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/logs"
)

// Colours are the terminal's own 16, so the dashboard follows the user's theme.
var (
	green  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	yellow = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	red    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	cyan   = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	dim    = lipgloss.NewStyle().Faint(true)
	bold   = lipgloss.NewStyle().Bold(true)
	chosen = lipgloss.NewStyle().Reverse(true)
)

// clean makes device output safe to draw. Logs come from apps and devices, so they can hold
// tabs and terminal escape sequences, which must never reach the user's terminal as commands.
func clean(text string) string {
	text = ansi.Strip(text)
	var b strings.Builder
	for _, r := range text {
		switch {
		case r == '\t':
			b.WriteString("    ")
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0):
			// Other control characters are dropped.
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// fit cuts or pads text to exactly width columns.
func fit(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if w := ansi.StringWidth(text); w > width {
		text = ansi.Truncate(text, width, "…")
	}
	if pad := width - ansi.StringWidth(text); pad > 0 {
		text += strings.Repeat(" ", pad)
	}
	return text
}

// box draws lines inside a border with a title. The result is exactly width by height.
func box(title string, lines []string, width, height int, focused bool) []string {
	if width < 4 || height < 2 {
		return make([]string, max(height, 0))
	}
	edge := dim
	if focused {
		edge = cyan
	}
	inner := width - 2
	label := " " + title + " "
	if ansi.StringWidth(label) > inner-1 {
		label = ansi.Truncate(label, max(inner-1, 0), "")
	}
	top := edge.Render("╭─") + bold.Render(label) + edge.Render(strings.Repeat("─", max(inner-1-ansi.StringWidth(label), 0))+"╮")
	out := make([]string, 0, height)
	out = append(out, top)
	for i := 0; i < height-2; i++ {
		content := ""
		if i < len(lines) {
			content = lines[i]
		}
		out = append(out, edge.Render("│")+fit(content, inner)+edge.Render("│"))
	}
	return append(out, edge.Render("╰"+strings.Repeat("─", inner)+"╯"))
}

// stateMark is the coloured dot in front of a device.
func stateMark(d device.Device, busy bool) string {
	switch {
	case busy, d.State == device.Booting, d.State == device.ShuttingDown:
		return yellow.Render("◐")
	case d.Ready():
		return green.Render("●")
	case d.State == device.Unauthorized, d.State == device.Offline:
		return red.Render("✗")
	}
	return dim.Render("○")
}

func levelStyle(level logs.Level) lipgloss.Style {
	switch level {
	case logs.Fatal, logs.Error:
		return red
	case logs.Warning:
		return yellow
	case logs.Debug, logs.Verbose:
		return dim
	}
	return lipgloss.NewStyle()
}

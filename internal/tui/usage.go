package tui

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
)

// usageEvery is how often an app's CPU and memory are sampled while they are shown.
const usageEvery = 2 * time.Second

// appUsage is the CPU and memory of a log pane's app, shown in its heading after M.
type appUsage struct {
	on, busy bool
	// gone is set while the app is not running, such as during a restart. Sampling goes on.
	gone bool
	last device.Usage
	at   time.Time
	// round counts the times M turned this on, so that a sample from before it was turned off and on
	// again is dropped.
	round int
	// cpu is the share of one core used between the last two samples, in percent, or -1 before
	// there are two.
	cpu float64
	// jank is the frame stats of an Android app, which are read with each sample. nil until the
	// first one, and after a failure, which only hides them.
	jank *device.Jank
	// resetJank asks the next sample to start the frame count again.
	resetJank bool
}

// label is what the heading shows.
func (u appUsage) label() string {
	if u.gone {
		return "app not running"
	}
	if u.at.IsZero() {
		return "CPU and memory…"
	}
	cpu := "CPU …"
	if u.cpu >= 0 {
		cpu = fmt.Sprintf("CPU %.0f%%", u.cpu)
	}
	label := fmt.Sprintf("%s · %d MB", cpu, (u.last.Memory+512*1024)/(1024*1024))
	switch {
	case u.jank == nil:
	case u.jank.Frames == 0:
		label += " · jank –"
	default:
		label += fmt.Sprintf(" · jank %.0f%%", u.jank.Percent)
	}
	return label
}

// resetFrames starts the frame count of log pane v again, while its CPU and memory are shown.
func (m *Model) resetFrames(v *logView) {
	switch {
	case !v.usage.on:
		m.setNote("Press M first to see the app's CPU, memory and frames", true)
	case v.spec.Device.Platform != device.Android:
		m.setNote("Frame stats are only for Android", true)
	default:
		v.usage.resetJank = true
		v.usage.jank = &device.Jank{}
		m.setNote("The frame count starts again", false)
	}
}

// toggleUsage shows or hides the CPU and memory of the app of log pane v.
func (m *Model) toggleUsage(v *logView) tea.Cmd {
	round := v.usage.round + 1
	if v.usage.on {
		v.usage = appUsage{round: round}
		return nil
	}
	if v.spec.App == "" {
		m.setNote("Open the log of one app to see its CPU and memory", true)
		return nil
	}
	v.usage = appUsage{on: true, cpu: -1, round: round}
	return m.sampleUsage(v)
}

// sampleUsages samples each shown app whose last sample is old enough. The tick calls it.
func (m *Model) sampleUsages() tea.Cmd {
	var cmds []tea.Cmd
	for _, v := range m.logPanes {
		if v.usage.on && !v.usage.busy && m.now().Sub(v.usage.at) >= usageEvery {
			cmds = append(cmds, m.sampleUsage(v))
		}
	}
	return tea.Batch(cmds...)
}

func (m *Model) sampleUsage(v *logView) tea.Cmd {
	v.usage.busy = true
	ctx, backend, id, d, app, pid, round := m.ctx, m.backend, v.id, v.spec.Device, v.spec.App, v.usage.last.PID, v.usage.round
	reset := v.usage.resetJank
	v.usage.resetJank = false
	return func() tea.Msg {
		usage, err := backend.AppUsage(ctx, d, app, pid)
		var jank *device.Jank
		if err == nil && d.Platform == device.Android {
			if j, jerr := backend.AppJank(ctx, d, app, reset); jerr == nil && !reset {
				jank = &j
			} else if jerr == nil {
				jank = &device.Jank{}
			}
		}
		return applyMsg(func(m *Model) tea.Cmd {
			v := m.logByID(id)
			if v == nil || !v.usage.on || v.usage.round != round {
				return nil
			}
			v.usage.busy = false
			now := m.now()
			if apperr.CodeOf(err) == apperr.InvalidState {
				v.usage = appUsage{on: true, gone: true, at: now, cpu: -1, round: round}
				return nil
			}
			if err != nil {
				v.usage = appUsage{round: round}
				m.setNote(firstLine(err.Error()), true)
				return nil
			}
			v.usage.gone = false
			v.usage.cpu = -1
			if wall := now.Sub(v.usage.at); usage.PID == v.usage.last.PID && !v.usage.at.IsZero() && wall > 0 {
				v.usage.cpu = max(float64(usage.CPU-v.usage.last.CPU)/float64(wall)*100, 0)
			}
			v.usage.jank = jank
			v.usage.last, v.usage.at = usage, now
			return nil
		})
	}
}

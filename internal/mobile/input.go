package mobile

import (
	"context"
	"fmt"
	"strings"

	"github.com/0xrnp/lazymobile/internal/android"
	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
)

// KeyNames are the keys PressKey takes.
func KeyNames() []string { return android.KeyNames() }

// inputDevice is d when it can be touched: an Android emulator that runs. Apple's simctl has no
// command to tap, type or swipe, so a simulator cannot be, and a phone is left alone.
func inputDevice(d device.Device) error {
	if d.Platform == device.IOS {
		return apperr.New(apperr.Unsupported, "Cannot touch the screen of %s from here", d.Name).
			WithHint("Apple's simctl has no tap, type or swipe command. An iOS simulator can be driven with XCUITest or Maestro.")
	}
	return nil
}

// Screen reads what is on the screen of a running Android emulator, with where to tap each thing.
func (m *Manager) Screen(ctx context.Context, d device.Device) (device.Screen, error) {
	if err := inputDevice(d); err != nil {
		return device.Screen{}, err
	}
	return m.Android.Screen(ctx, d)
}

// Tap touches the screen at x, y, in pixels.
func (m *Manager) Tap(ctx context.Context, d device.Device, x, y int) error {
	if err := inputDevice(d); err != nil {
		return err
	}
	if x < 0 || y < 0 || x > 20000 || y > 20000 {
		return apperr.New(apperr.InvalidState, "%d,%d is not a place on a screen", x, y)
	}
	return m.Android.Tap(ctx, d, x, y)
}

// TapElement touches the one thing on the screen that says or is called target: its text, its
// label or its id. It is an error when nothing matches, or when several do, naming them.
func (m *Manager) TapElement(ctx context.Context, d device.Device, target string) (device.Element, error) {
	if err := inputDevice(d); err != nil {
		return device.Element{}, err
	}
	screen, err := m.Android.Screen(ctx, d)
	if err != nil {
		return device.Element{}, err
	}
	el, err := FindElement(screen, target)
	if err != nil {
		return device.Element{}, err
	}
	return el, m.Android.Tap(ctx, d, el.X, el.Y)
}

// FindElement picks the element that target names. A whole match of the text, label or id (ignoring
// case) wins over a part of one, and tappable elements over the rest.
func FindElement(screen device.Screen, target string) (device.Element, error) {
	want := strings.ToLower(strings.TrimSpace(target))
	if want == "" {
		return device.Element{}, apperr.New(apperr.InvalidState, "Say which text, label or id to touch")
	}
	matches := func(el device.Element, whole bool) bool {
		for _, s := range []string{el.Text, el.Desc, el.ID, el.ID[strings.LastIndex(el.ID, "/")+1:]} {
			s = strings.ToLower(s)
			if s != "" && (s == want || (!whole && strings.Contains(s, want))) {
				return true
			}
		}
		return false
	}
	for _, whole := range []bool{true, false} {
		var found []device.Element
		for _, el := range screen.Elements {
			if matches(el, whole) {
				found = append(found, el)
			}
		}
		// Of several, the ones that can be touched are the ones meant.
		if len(found) > 1 {
			var tappable []device.Element
			for _, el := range found {
				if el.Tappable {
					tappable = append(tappable, el)
				}
			}
			if len(tappable) > 0 {
				found = tappable
			}
		}
		switch len(found) {
		case 0:
			continue
		case 1:
			return found[0], nil
		}
		var names []string
		for _, el := range found[:min(len(found), 6)] {
			names = append(names, fmt.Sprintf("%q at %d,%d", firstOf(el.Text, el.Desc, el.ID), el.X, el.Y))
		}
		return device.Element{}, apperr.New(apperr.InvalidState, "%d things match %q", len(found), target).
			WithHint("Tap one by its place: %s", strings.Join(names, "; "))
	}
	return device.Element{}, apperr.New(apperr.NotFound, "Nothing on the screen is called %q", target).
		WithHint("Read the screen again with ui_tree: it may have changed, or the text may be drawn without a label.")
}

func firstOf(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// Swipe drags from one point to another over ms milliseconds (300 when 0).
func (m *Manager) Swipe(ctx context.Context, d device.Device, x1, y1, x2, y2, ms int) error {
	if err := inputDevice(d); err != nil {
		return err
	}
	if ms == 0 {
		ms = 300
	}
	if ms < 50 || ms > 5000 {
		return apperr.New(apperr.InvalidState, "A swipe takes between 50 and 5000 milliseconds, not %d", ms)
	}
	for _, v := range []int{x1, y1, x2, y2} {
		if v < 0 || v > 20000 {
			return apperr.New(apperr.InvalidState, "%d is not a place on a screen", v)
		}
	}
	return m.Android.Swipe(ctx, d, x1, y1, x2, y2, ms)
}

// TypeText types into the field that has the focus.
func (m *Manager) TypeText(ctx context.Context, d device.Device, text string) error {
	if err := inputDevice(d); err != nil {
		return err
	}
	return m.Android.TypeText(ctx, d, text)
}

// PressKey presses a key such as back, home or enter.
func (m *Manager) PressKey(ctx context.Context, d device.Device, name string) error {
	if err := inputDevice(d); err != nil {
		return err
	}
	return m.Android.Key(ctx, d, name)
}

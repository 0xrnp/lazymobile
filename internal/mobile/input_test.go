package mobile

import (
	"context"
	"strings"
	"testing"

	"github.com/0xrnp/lazymobile/internal/android"
	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/run/runtest"
)

var loginScreen = device.Screen{Width: 1080, Height: 2400, Elements: []device.Element{
	{Text: "Sign in", ID: "com.example:id/login", X: 300, Y: 1050, Tappable: true},
	{Text: "Sign in with Google", X: 300, Y: 1300, Tappable: true},
	{Text: "Sign in help", X: 300, Y: 1500},
	{Desc: "Menu", X: 50, Y: 50, Tappable: true},
	{Text: "Save", X: 100, Y: 200, Tappable: true},
	{Text: "Save", X: 100, Y: 400, Tappable: true},
}}

func TestFindElementPrefersAWholeMatchAndTappableThings(t *testing.T) {
	for target, want := range map[string]int{"Sign in": 1050, "sign IN": 1050, "login": 1050, "com.example:id/login": 1050, "menu": 50, "google": 1300} {
		el, err := FindElement(loginScreen, target)
		if err != nil || el.Y != want {
			t.Errorf("%q: %+v, %v", target, el, err)
		}
	}
	// "help" is a part of one thing that cannot be touched, and it is the only match.
	if el, err := FindElement(loginScreen, "help"); err != nil || el.Y != 1500 {
		t.Errorf("help: %+v, %v", el, err)
	}
	_, err := FindElement(loginScreen, "Save")
	if apperr.CodeOf(err) != apperr.InvalidState || !strings.Contains(err.Error(), "2 things match") {
		t.Errorf("two Saves: %v", err)
	}
	if _, err := FindElement(loginScreen, "Delete account"); apperr.CodeOf(err) != apperr.NotFound {
		t.Errorf("nothing: %v", err)
	}
	if _, err := FindElement(loginScreen, "  "); err == nil {
		t.Error("an empty target was accepted")
	}
}

func TestASimulatorAndAPhoneAreNotTouched(t *testing.T) {
	m := New(&runtest.Fake{}, android.Options{}, "darwin")
	sim := device.Device{ID: "S", Name: "iPhone 17", Platform: device.IOS, Kind: device.Simulator, State: device.Booted}
	if err := m.Tap(context.Background(), sim, 1, 1); apperr.CodeOf(err) != apperr.Unsupported || !strings.Contains(err.Error(), "simctl") {
		t.Errorf("simulator: %v", err)
	}
	if _, err := m.Screen(context.Background(), sim); apperr.CodeOf(err) != apperr.Unsupported {
		t.Errorf("screen: %v", err)
	}
}

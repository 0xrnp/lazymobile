package mobile

import (
	"testing"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
)

var fleet = []device.Device{
	{ID: "A1", Name: "iPhone 16", Platform: device.IOS, Kind: device.Simulator, State: device.Shutdown},
	{ID: "A2", Name: "iPhone 16 Pro", Platform: device.IOS, Kind: device.Simulator, State: device.Booted},
	{ID: "P1", Name: "Pixel 9a", Platform: device.Android, Kind: device.Emulator, State: device.Shutdown},
	{ID: "R1", Name: "Rudra's iPhone", Platform: device.IOS, Kind: device.Physical, State: device.Connected},
}

func names(list []device.Device) []string {
	var out []string
	for _, d := range list {
		out = append(out, d.Name)
	}
	return out
}

func TestSelectGlobAndPartialNames(t *testing.T) {
	cases := []struct {
		patterns []string
		want     string
	}{
		{[]string{"iPhone*"}, "iPhone 16,iPhone 16 Pro"},
		{[]string{"IPHONE 16?PRO"}, "iPhone 16 Pro"},
		{[]string{"pixel"}, "Pixel 9a"},
		{[]string{"iPhone 16"}, "iPhone 16"},
		{[]string{"iPhone*", "Pixel*", "iPhone 16"}, "iPhone 16,iPhone 16 Pro,Pixel 9a"},
		{[]string{"iPhone"}, "iPhone 16,iPhone 16 Pro,Rudra's iPhone"},
		{[]string{"R1"}, "Rudra's iPhone"},
	}
	for _, c := range cases {
		got, err := selectFrom(fleet, c.patterns)
		if err != nil || joinNames(got) != c.want {
			t.Errorf("%v = %v, %v; want %s", c.patterns, names(got), err, c.want)
		}
	}
}

func joinNames(list []device.Device) string {
	out := ""
	for i, n := range names(list) {
		if i > 0 {
			out += ","
		}
		out += n
	}
	return out
}

func TestSelectRefusesAPatternThatMatchesNothing(t *testing.T) {
	_, err := selectFrom(fleet, []string{"iPhone*", "Galaxy*"})
	if apperr.CodeOf(err) != apperr.NotFound {
		t.Errorf("err = %v, want not_found", err)
	}
}

func TestSkips(t *testing.T) {
	sim, emu, phone := fleet[1], fleet[2], fleet[3]
	if BootSkip(fleet[0]) != "" || BootSkip(sim) == "" || BootSkip(phone) == "" {
		t.Errorf("boot skips wrong: %q %q %q", BootSkip(fleet[0]), BootSkip(sim), BootSkip(phone))
	}
	cases := []struct {
		d      device.Device
		path   string
		phones bool
		skip   bool
	}{
		{sim, "/b/App.app", false, false},
		{sim, "/b/App.app/", false, false},
		{sim, "/b/app.apk", false, true},
		{emu, "/b/app.apk", false, true}, // not running
		{phone, "/b/App.app", false, true},
		{phone, "/b/App.app", true, false},
	}
	for _, c := range cases {
		if got := InstallSkip(c.d, c.path, c.phones) != ""; got != c.skip {
			t.Errorf("InstallSkip(%s, %s, %v) skipped = %v, want %v", c.d.Name, c.path, c.phones, got, c.skip)
		}
	}
	emu.State = device.Booted
	if InstallSkip(emu, "/b/app.apk", false) != "" {
		t.Errorf("a running emulator should take an apk")
	}
}

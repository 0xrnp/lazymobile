package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/logs"
	"github.com/0xrnp/lazymobile/internal/state"
)

// clockStart is the time the test harness starts its clock at.
var clockStart = time.Unix(1_800_000_000, 0)

var iphone2 = device.Device{ID: "SIM-3", Name: "iPhone 17 Pro", Platform: device.IOS, Kind: device.Simulator, State: device.Booted, OSVersion: "27.2", Source: "simctl"}

// followed starts the dashboard in a folder where com.example.app's log was left open on iphone, then
// has `lazymobile run` start a run on d at when.
func followed(t *testing.T, d device.Device, when time.Time, devices ...device.Device) *harness {
	t.Helper()
	memory := memoryIn(t)
	err := memory.Store.Update(func(f *state.File) {
		f.Views = map[string]state.View{memory.Folder: {
			Device: iphone.Key(), SavedAt: clockStart.Add(-time.Hour),
			Log: &state.Log{Device: iphone.Key(), Mode: logs.System, App: "com.example.app"},
		}}
	})
	if err != nil {
		t.Fatal(err)
	}
	h := startWith(t, memory, devices...)
	h.wantCalls("logs SIM-1 mode=system app=com.example.app", 1)
	if err := memory.Store.Update(func(f *state.File) { f.RememberRunStarted(memory.Folder, d.Key(), when) }); err != nil {
		t.Fatal(err)
	}
	return h
}

// look is two ticks' worth of looking at the state file.
func (h *harness) look() {
	h.t.Helper()
	for range followEvery {
		h.run(h.m.followRun())
	}
}

func TestADashboardFollowsARunOnAnotherDeviceAndOpensTheFoldersAppLog(t *testing.T) {
	h := followed(t, iphone2, clockStart.Add(-time.Minute), iphone, iphone2)
	h.look()
	if d, _ := h.m.selected(); d.Key() != iphone2.Key() {
		t.Errorf("selected %s", d.Name)
	}
	h.wantCalls("logs SIM-3 mode=system app=com.example.app", 1)
	h.wantScreen("Following the run on iPhone 17 Pro")
	// It follows a run once.
	h.look()
	h.look()
	h.wantCalls("logs", 2)
}

func TestFollowingARunOnTheDeviceWithItsLogOpenOnlySelectsIt(t *testing.T) {
	h := followed(t, iphone, clockStart.Add(-time.Minute), iphone, iphone2)
	h.look()
	h.wantCalls("logs", 1)
	h.wantScreen("Following the run on iPhone 17")
}

func TestFollowingARunWithoutAKnownAppAsksForOne(t *testing.T) {
	h := followed(t, pixel, clockStart.Add(-time.Minute), iphone, pixel)
	h.look()
	if d, _ := h.m.selected(); d.Key() != pixel.Key() {
		t.Errorf("selected %s", d.Name)
	}
	h.wantCalls("logs", 1)
	h.wantScreen("Following the run on Pixel 9a: press l and pick the app")
}

func TestFollowingARunOnAPhoneDoesNotOpenALogByItself(t *testing.T) {
	h := followed(t, usbPhone, clockStart.Add(-time.Minute), iphone, usbPhone)
	h.look()
	if d, _ := h.m.selected(); d.Key() != usbPhone.Key() {
		t.Errorf("selected %s", d.Name)
	}
	h.wantCalls("logs", 1)
}

func TestARunThatIsOldOrWasBeforeTheDashboardWasUsedIsNotFollowed(t *testing.T) {
	// An hour old.
	h := followed(t, iphone2, clockStart.Add(-time.Hour), iphone, iphone2)
	h.look()
	h.wantCalls("logs", 1)
	if d, _ := h.m.selected(); d.Key() != iphone.Key() {
		t.Errorf("selected %s", d.Name)
	}
	// Recent, but before the dashboard was last used here: the saved view is newer.
	memory := memoryIn(t)
	err := memory.Store.Update(func(f *state.File) {
		f.Views = map[string]state.View{memory.Folder: {Device: iphone.Key(), SavedAt: clockStart.Add(-time.Minute)}}
		f.RememberRunStarted(memory.Folder, iphone2.Key(), clockStart.Add(-2*time.Minute))
	})
	if err != nil {
		t.Fatal(err)
	}
	h = startWith(t, memory, iphone, iphone2)
	h.look()
	if d, _ := h.m.selected(); d.Key() != iphone.Key() {
		t.Errorf("an earlier run was followed: selected %s", d.Name)
	}
}

func TestARunIsFollowedOnceTheDeviceIsListedButNotForever(t *testing.T) {
	h := followed(t, pixel, clockStart.Add(-10*time.Second), iphone)
	h.look()
	if d, _ := h.m.selected(); d.Key() != iphone.Key() {
		t.Errorf("selected %s before the device was listed", d.Name)
	}
	h.m.devices = append(h.m.devices, pixel)
	h.look()
	if d, _ := h.m.selected(); d.Key() != pixel.Key() {
		t.Errorf("selected %s after the device was listed", d.Name)
	}

	// A device that never shows is given up on after a minute.
	h = followed(t, pixel, clockStart.Add(-2*time.Minute), iphone)
	h.look()
	h.m.devices = append(h.m.devices, pixel)
	h.look()
	if d, _ := h.m.selected(); d.Key() != iphone.Key() {
		t.Errorf("a run from two minutes ago was followed late: selected %s", d.Name)
	}
}

func TestAnOpenWindowIsNotMovedByARun(t *testing.T) {
	h := followed(t, iphone2, clockStart.Add(-time.Minute), iphone, iphone2)
	h.m.message("Some window", "Something to read")
	h.look()
	if strings.Contains(h.screen(), "Following the run") {
		t.Error("a run was followed under an open window")
	}
	h.press("esc")
	h.look()
	h.wantScreen("Following the run on iPhone 17 Pro")
}

var pixel2 = device.Device{ID: "Pixel_Tablet", Name: "Pixel Tablet", Platform: device.Android, Kind: device.Emulator, State: device.Booted, OSVersion: "16", Serial: "emulator-5556", Source: "emulator"}

func TestTheAppOfAnOpenLogIsRememberedForTheFolder(t *testing.T) {
	memory := memoryIn(t)
	h := startWith(t, memory, pixel, pixel2)
	h.openLogs(pixel, "com.example.app")
	h.m.saveView()
	if got := memory.Store.Load().Runs[memory.Folder].Apps[device.Android]; got != "com.example.app" {
		t.Errorf("the app was not remembered: %q", got)
	}
	// Another folder does not know it.
	if got := memory.Store.Load().Runs["/somewhere/else"].Apps[device.Android]; got != "" {
		t.Errorf("another folder remembered %q", got)
	}
}

func TestARunOpensTheLogOfTheAppRememberedForTheFolderEvenWithNoLogOpen(t *testing.T) {
	memory := memoryIn(t)
	err := memory.Store.Update(func(f *state.File) {
		f.RememberFolderApp(memory.Folder, device.Android, "com.example.app")
		f.RememberRunStarted(memory.Folder, pixel2.Key(), clockStart.Add(-time.Minute))
	})
	if err != nil {
		t.Fatal(err)
	}
	h := startWith(t, memory, pixel, pixel2)
	h.wantCalls("logs", 0)
	h.look()
	h.wantCalls("logs Pixel_Tablet mode=logcat app=com.example.app", 1)
	h.wantScreen("Following the run on Pixel Tablet: the log of com.example.app")
}

package tui

import (
	"errors"
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/0xrnp/lazymobile/internal/ios"
)

func twoUnavailable() []ios.Unavailable {
	return []ios.Unavailable{
		{ID: "U1", Name: "iPhone 18 Pro", Platform: "iOS", OSVersion: "27.0", DataBytes: 1_854_255_104, Reason: "runtime profile not found"},
		{ID: "U2", Name: "iPhone 18 Pro Max", Platform: "iOS", OSVersion: "27.0", DataBytes: 18_337_792},
	}
}

func TestTheDevicesPaneShowsEachDevicesDataSize(t *testing.T) {
	big := iphone
	big.DataBytes = 1_854_255_104
	emu := pixel
	emu.DataBytes = 6_600_000_000
	h := start(t, big, emu)
	h.wantScreen("iPhone 17", "1.9 GB", "Pixel 9a", "6.6 GB")
	h.selectDevice(big)
	h.wantScreen("Data   1.9 GB on disk")
	h.selectDevice(emu)
	h.wantScreen("Data   6.6 GB on disk")
	// A device with no size known shows none, and its row still lines up.
	h2 := start(t, iphone, pixel)
	h2.wantNoScreen("GB", "on disk")
}

func TestSimulatorsXcodeCannotRunAreCountedAndOfferedForDeletion(t *testing.T) {
	h := startWith(t, memoryIn(t), iphone)
	h.backend.unavailable = twoUnavailable()
	h.press("r")
	h.wantScreen("2 simulators Xcode can no longer run use 1.9 GB", "Press U to delete them.")
	// They are not in the list: only the one that can run is.
	h.wantNoScreen("iPhone 18 Pro")

	h.press("U")
	h.wantScreen("Delete 2 unavailable simulators (1.9 GB)?", "iPhone 18 Pro, iOS 27.0, 1.9 GB",
		"iPhone 18 Pro Max, iOS 27.0, 18 MB", "cannot be had back")
	// Saying no deletes nothing.
	h.press("n")
	h.wantCalls("delete unavailable U1,U2", 0)
	if h.m.modal != nil {
		t.Error("the question stayed open")
	}

	h.press("U", "y")
	// Only the simulators that were named are to be deleted.
	h.wantCalls("delete unavailable U1,U2", 1)
	h.wantNote("Deleted 2 unavailable simulators, freed 1.9 GB")
	h.wantNoScreen("Press U to delete them.", "remove 2 unavailable")
	// Pressing it again says there is nothing to do.
	h.press("U")
	h.wantNote("There are no unavailable simulators")
}

func TestTheQuestionNamesSimulatorsOfEverySystem(t *testing.T) {
	h := startWith(t, memoryIn(t), iphone)
	h.backend.unavailable = []ios.Unavailable{
		{ID: "W1", Name: "Apple Watch Series 10", Platform: "watchOS", OSVersion: "11.2", DataBytes: 5_000_000},
		{ID: "F1", Name: "Future Thing"},
	}
	h.press("r", "U")
	h.wantScreen("Apple Watch Series 10, watchOS 11.2, 5 MB", "Future Thing")
}

func TestTheQuestionNamesSomeAndCountsTheRest(t *testing.T) {
	h := startWith(t, memoryIn(t), iphone)
	for i := 1; i <= 11; i++ {
		h.backend.unavailable = append(h.backend.unavailable, ios.Unavailable{ID: fmt.Sprint("U", i), Name: fmt.Sprintf("Old %d", i), Platform: "iOS", OSVersion: "26.0", DataBytes: 1_000_000_000})
	}
	h.press("r", "U")
	h.wantScreen("Delete 11 unavailable simulators (11.0 GB)?", "Old 1, iOS 26.0, 1.0 GB", "Old 8, iOS 26.0, 1.0 GB", "and 3 more")
	h.wantNoScreen("Old 9,")
}

func TestOneUnavailableSimulatorIsSaidInTheSingular(t *testing.T) {
	h := startWith(t, memoryIn(t), iphone)
	h.backend.unavailable = twoUnavailable()[1:]
	h.press("r")
	h.wantScreen("1 simulator Xcode can no longer run uses 18 MB")
	h.press("U", "y")
	h.wantNote("Deleted 1 unavailable simulator, freed 18 MB")
}

func TestAFailedDeletionOfUnavailableSimulatorsSaysWhy(t *testing.T) {
	h := startWith(t, memoryIn(t), iphone)
	h.backend.unavailable = twoUnavailable()
	h.backend.fail["delete unavailable"] = errors.New("simctl is busy")
	h.press("r", "U", "y")
	h.wantScreen("Could not delete the unavailable simulators", "simctl is busy")
	// What is still there stays offered.
	h.press("esc")
	h.wantScreen("Press U to delete them.")
}

func TestUOnADashboardWithNothingUnavailableSaysSoAndHasNoHint(t *testing.T) {
	h := start(t, iphone)
	h.wantNoScreen("Press U", "unavailable")
	h.press("U")
	h.wantNote("There are no unavailable simulators")
	if h.m.modal != nil {
		t.Error("a question was asked with nothing to delete")
	}
}

func TestTheFooterOffersTheDeletionWhereThereIsRoom(t *testing.T) {
	h := startWith(t, memoryIn(t), iphone)
	h.backend.unavailable = twoUnavailable()
	h.m.Update(tea.WindowSizeMsg{Width: 200, Height: 30})
	h.press("r")
	h.wantScreen("U remove 2 unavailable")
}

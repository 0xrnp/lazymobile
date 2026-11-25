package mobile

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
)

func TestAddMediaChecksEveryFileBeforeAskingAnyTool(t *testing.T) {
	m, fake := uninstallManager(t)
	ctx := context.Background()
	dir := t.TempDir()
	photo, empty, doc := filepath.Join(dir, "a.png"), filepath.Join(dir, "empty.png"), filepath.Join(dir, "a.pdf")
	for path, content := range map[string]string{photo: "png", empty: "", doc: "pdf"} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	simulator := device.Device{ID: "SIM", Name: "iPhone 17", Platform: device.IOS, Kind: device.Simulator, State: device.Booted}
	stopped := simulator
	stopped.State = device.Shutdown
	iphone := device.Device{ID: "UDID", Name: "My iPhone", Platform: device.IOS, Kind: device.Physical, State: device.Connected}
	for name, tc := range map[string]struct {
		d     device.Device
		paths []string
		code  apperr.Code
	}{
		"nothing":          {simulator, nil, apperr.InvalidState},
		"a missing file":   {simulator, []string{photo, filepath.Join(dir, "missing.png")}, apperr.NotFound},
		"an empty file":    {simulator, []string{empty}, apperr.InvalidState},
		"a folder":         {simulator, []string{dir}, apperr.InvalidState},
		"a document":       {simulator, []string{doc}, apperr.InvalidState},
		"a stopped device": {stopped, []string{photo}, apperr.InvalidState},
		"a real iPhone":    {iphone, []string{photo}, apperr.Unsupported},
	} {
		if _, err := m.AddMedia(ctx, tc.d, tc.paths); apperr.CodeOf(err) != tc.code {
			t.Errorf("%s: %v", name, err)
		}
	}
	if len(fake.Calls()) != 0 {
		t.Errorf("a tool ran: %v", fake.Calls())
	}
	if _, err := m.AddMedia(ctx, simulator, []string{photo}); err != nil || !fake.Called("simctl addmedia SIM "+photo) {
		t.Errorf("a photo: %v, calls %v", err, fake.Calls())
	}
}

func TestTheClipboardIsOnlyForASimulatorAndNotTooLong(t *testing.T) {
	m, fake := uninstallManager(t)
	ctx := context.Background()
	emulator := device.Device{ID: "Pixel_9a", Name: "Pixel 9a", Platform: device.Android, Kind: device.Emulator, State: device.Booted, Serial: "emulator-5554"}
	simulator := device.Device{ID: "SIM", Name: "iPhone 17", Platform: device.IOS, Kind: device.Simulator, State: device.Booted}
	if err := m.SetClipboard(ctx, emulator, "x"); apperr.CodeOf(err) != apperr.Unsupported {
		t.Errorf("an emulator: %v", err)
	}
	if err := m.SetClipboard(ctx, simulator, string(make([]byte, maxClipboard+1))); apperr.CodeOf(err) != apperr.InvalidState {
		t.Errorf("long text: %v", err)
	}
	if len(fake.Calls()) != 0 {
		t.Errorf("a tool ran: %v", fake.Calls())
	}
	if err := m.SetClipboard(ctx, simulator, "hello"); err != nil || !fake.Called("simctl pbcopy SIM") {
		t.Errorf("simulator: %v, calls %v", err, fake.Calls())
	}
}

package mobile

import (
	"context"
	"errors"
	"testing"

	"github.com/0xrnp/lazymobile/internal/android"
	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/run/runtest"
)

func manager(t *testing.T, goos string) (*Manager, *runtest.Fake) {
	t.Helper()
	fake := &runtest.Fake{}
	opts := android.Options{
		OS: goos, Home: t.TempDir(), Getenv: func(string) string { return "" },
		LookPath:     func(string) (string, error) { return "", errors.New("not found") },
		SDKFallbacks: []string{}, JavaFallbacks: []string{},
	}
	return New(fake, opts, goos), fake
}

func TestRealDevicesAreLeftAlone(t *testing.T) {
	m, fake := manager(t, "darwin")
	ctx := context.Background()
	for _, phone := range []device.Device{
		{ID: "UDID", Name: "My iPhone", Platform: device.IOS, Kind: device.Physical, State: device.Connected},
		{ID: "R58M", Name: "My Galaxy", Platform: device.Android, Kind: device.Physical, State: device.Connected},
	} {
		for name, err := range map[string]error{
			"boot":     m.Boot(ctx, phone, BootOptions{}),
			"shutdown": m.Shutdown(ctx, phone),
			"erase":    m.Erase(ctx, phone),
			"delete":   m.Delete(ctx, phone),
		} {
			if apperr.CodeOf(err) != apperr.Unsupported {
				t.Errorf("%s %s: error = %v, want unsupported", name, phone.Name, err)
			}
		}
	}
	if len(fake.Calls()) != 0 {
		t.Errorf("tools ran for a real device: %v", fake.Calls())
	}
}

func TestLinuxDoesNotLookForXcode(t *testing.T) {
	m, fake := manager(t, "linux")
	_, errs := m.List(context.Background())
	if fake.Called("xcrun") {
		t.Error("ran xcrun on Linux")
	}
	for _, err := range errs {
		if apperr.CodeOf(err) != apperr.ToolMissing {
			t.Errorf("unexpected error: %v", err)
		}
	}
}

func TestWipingASimulatorPointsAtErase(t *testing.T) {
	m, _ := manager(t, "darwin")
	sim := device.Device{ID: "UDID", Name: "iPhone 16", Platform: device.IOS, Kind: device.Simulator}
	err := m.Boot(context.Background(), sim, BootOptions{Wipe: true})
	if apperr.CodeOf(err) != apperr.Unsupported {
		t.Errorf("error = %v", err)
	}
}

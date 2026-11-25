package mobile

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/0xrnp/lazymobile/internal/android"
	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
)

// iosMediaTypes are what simctl addmedia takes: photos, videos, and contacts.
var iosMediaTypes = []string{".jpg", ".jpeg", ".png", ".gif", ".heic", ".tiff", ".bmp", ".mp4", ".mov", ".m4v", ".vcf"}

// maxClipboard is the longest text SetClipboard takes.
const maxClipboard = 1 << 20

// MediaTypes lists the file extensions AddMedia takes on d's platform.
func MediaTypes(d device.Device) []string {
	if d.Platform == device.IOS {
		return iosMediaTypes
	}
	return android.MediaTypes()
}

// AddMedia puts photos and videos from this computer into the library of a running simulator or
// emulator: a simulator also takes contacts (.vcf). Every file is checked before any is added.
// It returns where each one went on an emulator, and nothing for a simulator. A real phone's gallery
// is left alone.
func (m *Manager) AddMedia(ctx context.Context, d device.Device, paths []string) ([]string, error) {
	if err := virtualDevice(d, "its photos and videos"); err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, apperr.New(apperr.InvalidState, "There is nothing to add")
	}
	full := make([]string, len(paths))
	for i, path := range paths {
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, apperr.New(apperr.InvalidState, "Not a usable path: %s", path)
		}
		info, err := os.Stat(abs)
		switch {
		case err != nil:
			return nil, apperr.New(apperr.NotFound, "There is nothing at %s", path)
		case !info.Mode().IsRegular() || info.Size() == 0:
			return nil, apperr.New(apperr.InvalidState, "%s is not a file with something in it", path)
		case !slices.Contains(MediaTypes(d), strings.ToLower(filepath.Ext(abs))):
			return nil, apperr.New(apperr.InvalidState, "%s takes %s, not %s", d.Name, strings.Join(MediaTypes(d), " "), filepath.Ext(abs))
		}
		full[i] = abs
	}
	if d.Platform == device.IOS {
		return nil, m.IOS.AddMedia(ctx, d, full)
	}
	placed := make([]string, 0, len(full))
	for _, path := range full {
		target, err := m.Android.AddMedia(ctx, d, path)
		if err != nil {
			return placed, err
		}
		placed = append(placed, target)
	}
	return placed, nil
}

// SetClipboard puts text on a running simulator's clipboard. An Android emulator is refused: it
// has no command to set one, only an app on the device can.
func (m *Manager) SetClipboard(ctx context.Context, d device.Device, text string) error {
	if err := clipboardDevice(d); err != nil {
		return err
	}
	if len(text) > maxClipboard {
		return apperr.New(apperr.InvalidState, "That text is longer than %d bytes", maxClipboard)
	}
	return m.IOS.SetClipboard(ctx, d, text)
}

// Clipboard reads the text on a running simulator's clipboard.
func (m *Manager) Clipboard(ctx context.Context, d device.Device) (string, error) {
	if err := clipboardDevice(d); err != nil {
		return "", err
	}
	return m.IOS.Clipboard(ctx, d)
}

func clipboardDevice(d device.Device) error {
	if err := virtualDevice(d, "its clipboard"); err != nil {
		return err
	}
	if d.Platform == device.Android {
		return apperr.New(apperr.Unsupported, "An Android emulator's clipboard cannot be set from here").
			WithHint("Android has no shell command for it. Type text into the focused field with `adb shell input text`.")
	}
	return nil
}

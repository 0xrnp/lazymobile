package mobile

import (
	"context"
	"path"
	"strings"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
)

// Select returns every device the patterns name, each once, in the order they are first matched.
// A pattern with * ? or [ is a glob on the whole name ("iPhone*"). Any other pattern means what it
// does for Find, but every device it matches counts, not only a unique one. A pattern that
// matches nothing is an error, so a typo in a test matrix is not skipped silently.
func (m *Manager) Select(ctx context.Context, patterns []string) ([]device.Device, error) {
	devices, errs := m.List(ctx)
	picked, err := selectFrom(devices, patterns)
	if err != nil && len(errs) > 0 {
		if e, ok := err.(*apperr.Error); ok {
			e.Hint = "Some tools failed: " + errs[0].Error()
		}
	}
	return picked, err
}

func selectFrom(devices []device.Device, patterns []string) ([]device.Device, error) {
	var picked []device.Device
	seen := map[string]bool{}
	for _, pattern := range patterns {
		matches := matchPattern(devices, pattern)
		if len(matches) == 0 {
			return nil, apperr.New(apperr.NotFound, "No device matches %q", pattern).
				WithHint("Run `lazymobile list` to see the devices.")
		}
		for _, d := range matches {
			if !seen[d.Key()] {
				seen[d.Key()] = true
				picked = append(picked, d)
			}
		}
	}
	return picked, nil
}

func matchPattern(devices []device.Device, pattern string) []device.Device {
	if !strings.ContainsAny(pattern, "*?[") {
		return device.Match(devices, pattern)
	}
	glob := strings.ToLower(strings.TrimSpace(pattern))
	var out []device.Device
	for _, d := range devices {
		if ok, _ := path.Match(glob, strings.ToLower(d.Name)); ok {
			out = append(out, d)
		}
	}
	return out
}

// BootSkip says why a device is left alone by a boot of several, or "" when it should be booted.
func BootSkip(d device.Device) string {
	switch {
	case d.Kind == device.Physical:
		return "a real device cannot be booted from here"
	case d.Ready():
		return "already running"
	}
	return ""
}

// InstallSkip says why a build is not installed on d when installing on several, or "" when it
// should be. A real phone is skipped unless phones is set, so a build never lands on someone's own
// phone because it was running.
func InstallSkip(d device.Device, path string, phones bool) string {
	lower := strings.ToLower(strings.TrimRight(path, "/"))
	switch {
	case !d.Ready():
		return "not running"
	case d.Kind == device.Physical && !phones:
		return "a real device: add --phones to include it"
	case d.Platform == device.Android && !strings.HasSuffix(lower, ".apk"):
		return "Android needs an .apk"
	case d.Platform == device.IOS && !strings.HasSuffix(lower, ".app"):
		return "iOS needs an .app folder"
	}
	return ""
}

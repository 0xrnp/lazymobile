package state

import (
	"slices"

	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/logs"
)

// Preset is a log filter worth keeping: the filter text and the minimum level.
type Preset struct {
	Filter string     `json:"filter,omitempty"`
	Level  logs.Level `json:"level,omitempty"`
}

// PresetKey is what presets are kept under: one app on one platform, or every app of a platform
// when app is empty.
func PresetKey(platform device.Platform, app string) string {
	return string(platform) + ":" + app
}

// SavePreset keeps p first for the app. The same filter and level is kept once.
func (f *File) SavePreset(key string, p Preset) {
	if f.Presets == nil {
		f.Presets = map[string][]Preset{}
	}
	list := slices.DeleteFunc(slices.Clone(f.Presets[key]), func(old Preset) bool { return old == p })
	f.Presets[key] = append([]Preset{p}, list...)
}

// DeletePreset forgets p for the app.
func (f *File) DeletePreset(key string, p Preset) {
	list := slices.DeleteFunc(slices.Clone(f.Presets[key]), func(old Preset) bool { return old == p })
	if len(list) == 0 {
		delete(f.Presets, key)
		return
	}
	f.Presets[key] = list
}

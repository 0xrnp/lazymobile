package state

import (
	"path/filepath"
	"testing"

	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/logs"
)

func TestPresetsAreKeptPerAppNewestFirstAndOnce(t *testing.T) {
	store := Store{Path: filepath.Join(t.TempDir(), "state.json")}
	key := PresetKey(device.Android, "com.example.app")
	other := PresetKey(device.IOS, "com.example.app")
	a, b := Preset{Filter: "net -heartbeat"}, Preset{Filter: "crash", Level: logs.Error}
	for _, p := range []Preset{a, b, a} {
		_ = store.Update(func(f *File) { f.SavePreset(key, p) })
	}
	_ = store.Update(func(f *File) { f.SavePreset(other, b) })

	got := store.Load().Presets
	if len(got[key]) != 2 || got[key][0] != a || got[key][1] != b {
		t.Errorf("android presets = %v, want a then b", got[key])
	}
	if len(got[other]) != 1 {
		t.Errorf("another platform's presets = %v", got[other])
	}

	_ = store.Update(func(f *File) { f.DeletePreset(key, a) })
	if got := store.Load().Presets[key]; len(got) != 1 || got[0] != b {
		t.Errorf("after delete = %v", got)
	}
	_ = store.Update(func(f *File) { f.DeletePreset(key, b) })
	if _, there := store.Load().Presets[key]; there {
		t.Errorf("an empty list should be dropped")
	}
}

func TestPresetsAreLimitedPerApp(t *testing.T) {
	store := Store{Path: filepath.Join(t.TempDir(), "state.json")}
	key := PresetKey(device.Android, "")
	for i := 0; i < maxPresets+5; i++ {
		_ = store.Update(func(f *File) { f.SavePreset(key, Preset{Filter: string(rune('a' + i))}) })
	}
	if got := store.Load().Presets[key]; len(got) != maxPresets || got[0].Filter != string(rune('a'+maxPresets+4)) {
		t.Errorf("kept %d presets, first %v", len(got), got[0])
	}
}

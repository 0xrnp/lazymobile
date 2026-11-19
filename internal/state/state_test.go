package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/0xrnp/lazymobile/internal/device"
)

func TestAppIDsAreKeptNewestFirstWithoutRepeats(t *testing.T) {
	var f File
	for _, id := range []string{"com.a", "com.b", "com.a"} {
		f.RememberApp(device.IOS, id)
	}
	f.RememberApp(device.Android, "com.c")
	f.RememberApp(device.IOS, "")
	if got := f.Apps[device.IOS]; !slices.Equal(got, []string{"com.a", "com.b"}) {
		t.Errorf("iOS apps = %v", got)
	}
	if got := f.Apps[device.Android]; !slices.Equal(got, []string{"com.c"}) {
		t.Errorf("Android apps = %v", got)
	}
}

func TestTheFileIsPrivateAndReadBack(t *testing.T) {
	store := Store{Path: filepath.Join(t.TempDir(), "lazymobile", "state.json")}
	err := store.Update(func(f *File) {
		f.RememberApp(device.IOS, "com.example.app")
		f.Views = map[string]View{"/work": {Device: "ios:simulator:SIM-1", Log: &Log{Device: "ios:simulator:SIM-1", Mode: "system", App: "com.example.app"}, Wrap: true}}
	})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(store.Path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("file mode = %o, want 600", mode)
	}
	f := store.Load()
	if f.Apps[device.IOS][0] != "com.example.app" || !f.Views["/work"].Wrap || f.Views["/work"].Log.App != "com.example.app" {
		t.Errorf("read back %+v", f)
	}
	entries, _ := os.ReadDir(filepath.Dir(store.Path))
	if len(entries) != 1 {
		t.Errorf("left behind %d files in the folder", len(entries))
	}
}

func TestABrokenOrMissingFileIsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if f := (Store{Path: path}).Load(); f.Apps != nil || f.Views != nil {
		t.Errorf("missing file gave %+v", f)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := Store{Path: path}
	if f := store.Load(); f.Apps != nil {
		t.Errorf("broken file gave %+v", f)
	}
	// The next change replaces the broken file.
	if err := store.Update(func(f *File) { f.RememberApp(device.Android, "com.x") }); err != nil {
		t.Fatal(err)
	}
	if got := store.Load().Apps[device.Android]; !slices.Equal(got, []string{"com.x"}) {
		t.Errorf("after update: %v", got)
	}
}

func TestTheZeroStoreRemembersNothing(t *testing.T) {
	var store Store
	if err := store.Update(func(f *File) { f.RememberApp(device.IOS, "com.x") }); err != nil {
		t.Fatal(err)
	}
	if f := store.Load(); f.Apps != nil {
		t.Errorf("got %+v", f)
	}
}

func TestListsStayShort(t *testing.T) {
	store := Store{Path: filepath.Join(t.TempDir(), "state.json")}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	err := store.Update(func(f *File) {
		for i := range maxApps + 5 {
			f.RememberApp(device.IOS, fmt.Sprintf("com.app%d", i))
		}
		f.Views = map[string]View{}
		for i := range maxViews + 5 {
			f.Views[fmt.Sprintf("/folder%d", i)] = View{SavedAt: start.Add(time.Duration(i) * time.Hour)}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	f := store.Load()
	if len(f.Apps[device.IOS]) != maxApps || f.Apps[device.IOS][0] != fmt.Sprintf("com.app%d", maxApps+4) {
		t.Errorf("apps = %v", f.Apps[device.IOS])
	}
	if len(f.Views) != maxViews {
		t.Errorf("%d views kept, want %d", len(f.Views), maxViews)
	}
	if _, kept := f.Views["/folder0"]; kept {
		t.Error("the oldest view was kept")
	}
	if _, kept := f.Views[fmt.Sprintf("/folder%d", maxViews+4)]; !kept {
		t.Error("the newest view was dropped")
	}
}

func TestChoicesPutRecentAppsFirst(t *testing.T) {
	got := Choices([]string{"com.b", "com.z"}, []device.App{{ID: "com.a", Name: "A"}, {ID: "com.b", Name: "B"}})
	want := []AppChoice{
		{ID: "com.b", Name: "B", Recent: true, Installed: true},
		{ID: "com.z", Recent: true},
		{ID: "com.a", Name: "A", Installed: true},
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestChoicesCarryTheVersionOfInstalledApps(t *testing.T) {
	installed := []device.App{
		{ID: "com.a", Name: "A", Version: "1.4.2", Build: "87"},
		{ID: "com.b", Name: "B", Version: "2.0.0"},
	}
	got := Choices([]string{"com.a", "com.gone"}, installed)
	want := []AppChoice{
		{ID: "com.a", Name: "A", Version: "1.4.2", Build: "87", Recent: true, Installed: true},
		{ID: "com.gone", Recent: true},
		{ID: "com.b", Name: "B", Version: "2.0.0", Installed: true},
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestVersionLabel(t *testing.T) {
	for _, c := range []struct {
		version, build, want string
	}{
		{"1.4.2", "87", "1.4.2 (87)"},
		{"1.4.2", "", "1.4.2"},
		{"", "87", "(87)"},
		{"", "", ""},
	} {
		if got := (AppChoice{Version: c.version, Build: c.build}).VersionLabel(); got != c.want {
			t.Errorf("VersionLabel(%q, %q) = %q, want %q", c.version, c.build, got, c.want)
		}
	}
}

func TestDefaultPathFollowsXDG(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/xdg")
	if got := DefaultPath(); got != "/xdg/lazymobile/state.json" {
		t.Errorf("got %s", got)
	}
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/home/someone")
	if got := DefaultPath(); got != "/home/someone/.local/state/lazymobile/state.json" {
		t.Errorf("got %s", got)
	}
}

func TestRunsAreRememberedPerFolderAndKeptShort(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store := Store{Path: path}
	if err := store.Update(func(f *File) { f.RememberRun("/work", "ios:simulator:SIM-1") }); err != nil {
		t.Fatal(err)
	}
	if got := store.Load().Runs["/work"].Device; got != "ios:simulator:SIM-1" {
		t.Errorf("got %q", got)
	}
	if err := store.Update(func(f *File) {
		f.RememberRun("", "x")
		f.RememberRun("/empty", "")
		for i := range maxViews + 5 {
			f.RememberRun(fmt.Sprintf("/folder-%d", i), "android:emulator:Pixel")
		}
	}); err != nil {
		t.Fatal(err)
	}
	got := store.Load().Runs
	if len(got) != maxViews || got["/empty"].Device != "" || got[""].Device != "" {
		t.Errorf("%d runs kept: %v", len(got), got)
	}
}

func TestTheRunSplitAndTheHiddenLogAreRememberedApartAndWithoutLosingTheRest(t *testing.T) {
	var f File
	f.RememberRunStarted("/work/app", "emulator-5554", time.Unix(100, 0))
	f.RememberRunSplit("/work/app", 450)
	f.RememberRunLogHidden("/work/app", true)
	run := f.Runs["/work/app"]
	if run.LogShare != 450 || !run.LogHidden || run.Last != "emulator-5554" {
		t.Errorf("run = %+v", run)
	}
	f.RememberRunLogHidden("/work/app", false)
	if run := f.Runs["/work/app"]; run.LogShare != 450 || run.LogHidden {
		t.Errorf("run = %+v", run)
	}
	// An empty folder is left alone, and an old file without the fields reads as the defaults.
	f.RememberRunLogHidden("", true)
	if len(f.Runs) != 1 {
		t.Errorf("runs = %v", f.Runs)
	}
	var old File
	if err := json.Unmarshal([]byte(`{"runs":{"/work/app":{"device":"x"}}}`), &old); err != nil {
		t.Fatal(err)
	}
	if run := old.Runs["/work/app"]; run.LogShare != 0 || run.LogHidden {
		t.Errorf("old run = %+v", run)
	}
}

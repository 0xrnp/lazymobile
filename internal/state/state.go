// Package state remembers things between runs: the app ids used before, and where the dashboard
// was left in each folder. Both are conveniences, so a file that cannot be read or written is
// ignored rather than reported.
package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/logs"
)

const (
	// maxApps is how many app ids are kept per platform.
	maxApps = 10
	// maxPresets is how many filters are kept per app.
	maxPresets = 10
	// maxViews is how many folders keep a dashboard view. The oldest go first.
	maxViews = 30
)

// File is everything that is remembered.
type File struct {
	// Apps are the app ids used, newest first, per platform.
	Apps map[device.Platform][]string `json:"apps,omitempty"`
	// Installs are the builds installed, newest first, per platform.
	Installs map[device.Platform][]string `json:"installs,omitempty"`
	// Presets are saved log filters, newest first, per platform and app id (see PresetKey).
	Presets map[string][]Preset `json:"presets,omitempty"`
	// Views are where the dashboard was left, by folder.
	Views map[string]View `json:"views,omitempty"`
	// Runs are the device `lazymobile run` last used, by folder.
	Runs map[string]Run `json:"runs,omitempty"`
	// SeenVersion is the newest release the dashboard was started with, so what is new is shown
	// once after an upgrade.
	SeenVersion string `json:"seen_version,omitempty"`
	// UpdateCheck is the last look for a newer release.
	UpdateCheck UpdateCheck `json:"update_check,omitzero"`
}

// UpdateCheck is when the newest release was last asked for, and what it was.
type UpdateCheck struct {
	At     time.Time `json:"at"`
	Latest string    `json:"latest,omitempty"`
}

// Run is the device a folder's app was last run on.
type Run struct {
	// Device is the key of the device to use next time: a simulator or an emulator, never a phone.
	Device  string    `json:"device"`
	SavedAt time.Time `json:"saved_at"`
	// Last is the key of the device of the latest run, a phone included, and Started is when that run
	// began. A dashboard opened in the folder follows it.
	Last    string    `json:"last,omitempty"`
	Started time.Time `json:"started,omitzero"`
	// Profile is the way to run that was chosen last, when there are several.
	Profile string `json:"profile,omitempty"`
	// Apps are the app a log was last opened for in the folder, by platform, so a followed run can
	// open its log without being asked.
	Apps map[device.Platform]string `json:"apps,omitempty"`
	// LogShare is how much of the width the log takes beside the tool in lazymobile run --here, in
	// thousandths, once it was moved. Zero is the default.
	LogShare int `json:"log_share,omitempty"`
}

// RememberRunSplit records how much of the width the log took beside the tool, in thousandths.
func (f *File) RememberRunSplit(folder string, share int) {
	if folder == "" {
		return
	}
	if f.Runs == nil {
		f.Runs = map[string]Run{}
	}
	run := f.Runs[folder]
	run.LogShare = share
	f.Runs[folder] = run
}

// RememberRunProfile records the way to run the folder's app that was chosen.
func (f *File) RememberRunProfile(folder, profile string) {
	if folder == "" || profile == "" {
		return
	}
	if f.Runs == nil {
		f.Runs = map[string]Run{}
	}
	run := f.Runs[folder]
	run.Profile = profile
	f.Runs[folder] = run
}

// RememberFolderApp records the app a log was opened for in a folder, on a platform.
func (f *File) RememberFolderApp(folder string, platform device.Platform, app string) {
	if folder == "" || app == "" {
		return
	}
	if f.Runs == nil {
		f.Runs = map[string]Run{}
	}
	run := f.Runs[folder]
	if run.Apps == nil {
		run.Apps = map[device.Platform]string{}
	}
	run.Apps[platform] = app
	f.Runs[folder] = run
}

// RememberRun records the device a folder's app was run on.
func (f *File) RememberRun(folder, deviceKey string) {
	if folder == "" || deviceKey == "" {
		return
	}
	if f.Runs == nil {
		f.Runs = map[string]Run{}
	}
	run := f.Runs[folder]
	run.Device, run.SavedAt = deviceKey, time.Now()
	f.Runs[folder] = run
}

// View is where the dashboard was left.
type View struct {
	// Device is the key of the selected device.
	Device string `json:"device,omitempty"`
	// Log is the first log stream that was open, if any, and Filter, Level, LevelChosen and Wrap are
	// its settings. More are the other logs that were open, each with its settings in the Log
	// itself. Layout is how the logs sit: "" for side by side, "stacked" for one above the other, or
	// "one" for one at a time.
	Log    *Log   `json:"log,omitempty"`
	More   []Log  `json:"more,omitempty"`
	Layout string `json:"layout,omitempty"`

	Filter string     `json:"filter,omitempty"`
	Level  logs.Level `json:"level,omitempty"`
	// LevelChosen is whether the level was set by hand, rather than a stream's default.
	LevelChosen bool      `json:"level_chosen,omitempty"`
	Wrap        bool      `json:"wrap,omitempty"`
	SavedAt     time.Time `json:"saved_at"`
}

// Log is a log stream that can be opened again.
type Log struct {
	Device string    `json:"device"`
	Mode   logs.Mode `json:"mode"`
	App    string    `json:"app,omitempty"`

	// The settings of a log that is not the first. The first keeps its own in View, as it did
	// before there could be more than one.
	Filter      string     `json:"filter,omitempty"`
	Level       logs.Level `json:"level,omitempty"`
	LevelChosen bool       `json:"level_chosen,omitempty"`
	Wrap        bool       `json:"wrap,omitempty"`
}

// Store reads and writes the file at Path. The zero Store, with no path, remembers nothing.
type Store struct {
	Path string
}

// DefaultPath is $XDG_STATE_HOME/lazymobile/state.json, or ~/.local/state/lazymobile/state.json.
// It is "" when neither can be worked out.
func DefaultPath() string {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return ""
		}
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, "lazymobile", "state.json")
}

// Load returns what is remembered. A missing, unreadable or broken file is an empty one.
func (s Store) Load() File {
	var f File
	if s.Path == "" {
		return f
	}
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return f
	}
	if json.Unmarshal(data, &f) != nil {
		return File{}
	}
	return f
}

// Update changes what is remembered. The file is replaced in one step, so a crash or another
// lazymobile writing at the same time cannot leave half a file. It is readable only by its
// owner, because filters and app ids can say what someone is working on.
func (s Store) Update(change func(*File)) error {
	if s.Path == "" {
		return nil
	}
	f := s.Load()
	change(&f)
	f.prune()
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".state-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.Path)
}

// RememberApp puts an app id first in its platform's list.
func (f *File) RememberApp(platform device.Platform, id string) {
	if id == "" {
		return
	}
	if f.Apps == nil {
		f.Apps = map[device.Platform][]string{}
	}
	ids := slices.DeleteFunc(slices.Clone(f.Apps[platform]), func(old string) bool { return old == id })
	f.Apps[platform] = append([]string{id}, ids...)
}

// RememberInstall puts a build path first in the list for platform.
func (f *File) RememberInstall(platform device.Platform, path string) {
	if path == "" {
		return
	}
	if f.Installs == nil {
		f.Installs = map[device.Platform][]string{}
	}
	paths := slices.DeleteFunc(slices.Clone(f.Installs[platform]), func(old string) bool { return old == path })
	f.Installs[platform] = append([]string{path}, paths...)
}

// RememberRunStarted records that a run began on the device with this key, for a dashboard in the
// folder to follow. It keeps the device remembered for next time.
func (f *File) RememberRunStarted(folder, deviceKey string, now time.Time) {
	if folder == "" || deviceKey == "" {
		return
	}
	if f.Runs == nil {
		f.Runs = map[string]Run{}
	}
	run := f.Runs[folder]
	run.Last, run.Started = deviceKey, now
	f.Runs[folder] = run
}

// prune keeps the lists short.
func (f *File) prune() {
	for platform, paths := range f.Installs {
		if len(paths) > maxApps {
			f.Installs[platform] = paths[:maxApps]
		}
	}
	for key, list := range f.Presets {
		if len(list) > maxPresets {
			f.Presets[key] = list[:maxPresets]
		}
	}
	for platform, ids := range f.Apps {
		if len(ids) > maxApps {
			f.Apps[platform] = ids[:maxApps]
		}
	}
	if len(f.Views) > maxViews {
		folders := make([]string, 0, len(f.Views))
		for folder := range f.Views {
			folders = append(folders, folder)
		}
		sort.Slice(folders, func(i, j int) bool { return f.Views[folders[i]].SavedAt.After(f.Views[folders[j]].SavedAt) })
		for _, folder := range folders[maxViews:] {
			delete(f.Views, folder)
		}
	}
	if len(f.Runs) > maxViews {
		folders := make([]string, 0, len(f.Runs))
		for folder := range f.Runs {
			folders = append(folders, folder)
		}
		sort.Slice(folders, func(i, j int) bool { return f.Runs[folders[i]].SavedAt.After(f.Runs[folders[j]].SavedAt) })
		for _, folder := range folders[maxViews:] {
			delete(f.Runs, folder)
		}
	}
}

// AppChoice is an app id that can be offered, with where it came from.
type AppChoice struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
	Build   string `json:"build,omitempty"`
	// Recent is whether it was used before, and Installed whether it is on the device.
	Recent    bool `json:"recent"`
	Installed bool `json:"installed"`
}

// VersionLabel is "1.4.2 (87)", or whichever of the two is known, or "".
func (c AppChoice) VersionLabel() string {
	return device.App{Version: c.Version, Build: c.Build}.VersionLabel()
}

// Choices lists the recent app ids first, newest first, then the installed apps that are not
// among them, in the order given.
func Choices(recent []string, installed []device.App) []AppChoice {
	byID := map[string]device.App{}
	for _, app := range installed {
		byID[app.ID] = app
	}
	var choices []AppChoice
	seen := map[string]bool{}
	for _, id := range recent {
		if seen[id] {
			continue
		}
		seen[id] = true
		app, isInstalled := byID[id]
		choices = append(choices, AppChoice{ID: id, Name: app.Name, Version: app.Version, Build: app.Build, Recent: true, Installed: isInstalled})
	}
	for _, app := range installed {
		if seen[app.ID] {
			continue
		}
		seen[app.ID] = true
		choices = append(choices, AppChoice{ID: app.ID, Name: app.Name, Version: app.Version, Build: app.Build, Installed: true})
	}
	return choices
}

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
	// maxViews is how many folders keep a dashboard view. The oldest go first.
	maxViews = 30
)

// File is everything that is remembered.
type File struct {
	// Apps are the app ids used, newest first, per platform.
	Apps map[device.Platform][]string `json:"apps,omitempty"`
	// Views are where the dashboard was left, by folder.
	Views map[string]View `json:"views,omitempty"`
}

// View is where the dashboard was left.
type View struct {
	// Device is the key of the selected device.
	Device string `json:"device,omitempty"`
	// Log is the log stream that was open, if any.
	Log *Log `json:"log,omitempty"`

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

// prune keeps the lists short.
func (f *File) prune() {
	for platform, ids := range f.Apps {
		if len(ids) > maxApps {
			f.Apps[platform] = ids[:maxApps]
		}
	}
	if len(f.Views) <= maxViews {
		return
	}
	folders := make([]string, 0, len(f.Views))
	for folder := range f.Views {
		folders = append(folders, folder)
	}
	sort.Slice(folders, func(i, j int) bool { return f.Views[folders[i]].SavedAt.After(f.Views[folders[j]].SavedAt) })
	for _, folder := range folders[maxViews:] {
		delete(f.Views, folder)
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

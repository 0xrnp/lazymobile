// Package devicesets reads named groups of devices, such as a test matrix, from .lazymobile.json:
//
//	{"device_sets": {"matrix": ["iPhone 17", "Pixel 9a", "Pixel Tablet*"]}}
//
// A set only names devices. It never holds a command, so a file from someone else's repository
// can choose which devices a command runs on but not what runs.
package devicesets

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/0xrnp/lazymobile/internal/apperr"
)

// ConfigFile is the optional per-project settings file.
const ConfigFile = ".lazymobile.json"

// Sets maps a set's name to its device patterns, as `lazymobile boot --all` takes them.
type Sets map[string][]string

// Load reads the sets from start and each folder above it up to the repository root. When two
// files use the same name, the one nearer to start wins.
func Load(start string) (Sets, error) {
	sets := Sets{}
	for dir := start; ; dir = filepath.Dir(dir) {
		path := filepath.Join(dir, ConfigFile)
		if content, err := os.ReadFile(path); err == nil {
			var file struct {
				Sets map[string][]string `json:"device_sets"`
			}
			if err := json.Unmarshal(content, &file); err != nil {
				return nil, apperr.New(apperr.ParseFailed, "%s is not valid JSON: %v", path, err)
			}
			for name, patterns := range file.Sets {
				if len(patterns) == 0 {
					return nil, apperr.New(apperr.ParseFailed, "%s: the device set %q is empty", path, name)
				}
				for _, p := range patterns {
					if strings.TrimSpace(p) == "" {
						return nil, apperr.New(apperr.ParseFailed, "%s: the device set %q has an empty name", path, name)
					}
				}
				if _, taken := sets[name]; !taken {
					sets[name] = patterns
				}
			}
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil || dir == filepath.Dir(dir) {
			break
		}
	}
	return sets, nil
}

// Patterns returns the named set, or a not_found error that lists the sets there are.
func (s Sets) Patterns(name string) ([]string, error) {
	if patterns, ok := s[name]; ok {
		return patterns, nil
	}
	names := make([]string, 0, len(s))
	for n := range s {
		names = append(names, n)
	}
	sort.Strings(names)
	err := apperr.New(apperr.NotFound, "No device set is named %q", name)
	if len(names) == 0 {
		return nil, err.WithHint(`Add one to %s, for example {"device_sets": {"matrix": ["iPhone 17", "Pixel 9a"]}}.`, ConfigFile)
	}
	return nil, err.WithHint("The sets are: %s", strings.Join(names, ", "))
}

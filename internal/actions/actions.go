// Package actions reads the project's own commands from .lazymobile.json, such as "flutter run",
// and fills in the device they run on. It never runs anything: the file can come from a repository
// someone else wrote, so whoever calls Resolve must show the command and ask first.
package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
)

// ConfigFile is the optional per-project settings file. The eas package reads its own key from it.
const ConfigFile = ".lazymobile.json"

// Action is one command from the file.
type Action struct {
	Name string `json:"name"`
	// Command is an argument list, never a shell string. "{device_id}", "{platform}" and "{app}"
	// are replaced in every argument except the first.
	Command []string `json:"command"`
	// Platform limits the action to "android" or "ios". Empty means both.
	Platform device.Platform `json:"platform,omitempty"`
	// Dir is the folder of the file that defined the action, set when the file is read. The command
	// runs there.
	Dir string `json:"dir,omitempty"`
}

var placeholder = regexp.MustCompile(`\{[^{}]*\}`)

var known = map[string]bool{"{device_id}": true, "{platform}": true, "{app}": true}

// Load reads the actions from start and each folder above it up to the repository root. When two
// files use the same name, the one nearer to start wins.
func Load(start string) ([]Action, error) {
	var list []Action
	seen := map[string]bool{}
	for dir := start; ; dir = filepath.Dir(dir) {
		found, err := readFile(dir)
		if err != nil {
			return nil, err
		}
		for _, a := range found {
			if !seen[a.Name] {
				seen[a.Name] = true
				list = append(list, a)
			}
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil || dir == filepath.Dir(dir) {
			break
		}
	}
	return list, nil
}

func readFile(dir string) ([]Action, error) {
	path := filepath.Join(dir, ConfigFile)
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, nil
	}
	var file struct {
		Actions []Action `json:"actions"`
	}
	if err := json.Unmarshal(content, &file); err != nil {
		return nil, apperr.New(apperr.ParseFailed, "%s is not valid JSON: %v", path, err)
	}
	names := map[string]bool{}
	for i := range file.Actions {
		a := &file.Actions[i]
		a.Dir = dir
		if err := a.validate(); err != nil {
			return nil, apperr.New(apperr.ParseFailed, "%s: action %d: %v", path, i+1, err)
		}
		if names[a.Name] {
			return nil, apperr.New(apperr.ParseFailed, "%s: two actions are named %q", path, a.Name)
		}
		names[a.Name] = true
	}
	return file.Actions, nil
}

func (a Action) validate() error {
	if strings.TrimSpace(a.Name) == "" {
		return fmt.Errorf("it has no name")
	}
	if len(a.Command) == 0 || a.Command[0] == "" {
		return fmt.Errorf("%q has no command", a.Name)
	}
	if placeholder.MatchString(a.Command[0]) {
		return fmt.Errorf("%q: the program name cannot contain a placeholder", a.Name)
	}
	for _, arg := range a.Command[1:] {
		for _, p := range placeholder.FindAllString(arg, -1) {
			if !known[p] {
				return fmt.Errorf("%q uses %s, which is not one of {device_id}, {platform} and {app}", a.Name, p)
			}
		}
	}
	if a.Platform != "" && a.Platform != device.Android && a.Platform != device.IOS {
		return fmt.Errorf("%q: platform must be \"android\" or \"ios\", not %q", a.Name, a.Platform)
	}
	return nil
}

// For returns the actions that can run on d, in file order.
func For(list []Action, d device.Device) []Action {
	var out []Action
	for _, a := range list {
		if a.Platform == "" || a.Platform == d.Platform {
			out = append(out, a)
		}
	}
	return out
}

// Find returns the action with this name.
func Find(list []Action, name string) (Action, error) {
	for _, a := range list {
		if a.Name == name {
			return a, nil
		}
	}
	return Action{}, apperr.New(apperr.NotFound, "no action is named %q", name).
		WithHint("Actions are listed in %s. Run `lazymobile action --list` to see them.", ConfigFile)
}

// Resolve returns the argument list with the device and app filled in. app may be empty unless the
// command uses {app}.
func (a Action) Resolve(d device.Device, app string) ([]string, error) {
	if a.Platform != "" && a.Platform != d.Platform {
		return nil, apperr.New(apperr.Unsupported, "%q is for %s, and %s is %s", a.Name, a.Platform, d.Name, d.Platform)
	}
	argv := make([]string, len(a.Command))
	argv[0] = a.Command[0]
	for i, arg := range a.Command[1:] {
		if strings.Contains(arg, "{app}") && app == "" {
			return nil, apperr.New(apperr.Usage, "%q needs an app id for {app}", a.Name).
				WithHint("Pass one with --app, for example --app com.example.app.")
		}
		argv[i+1] = strings.NewReplacer(
			"{device_id}", d.TargetID(),
			"{platform}", string(d.Platform),
			"{app}", app,
		).Replace(arg)
	}
	return argv, nil
}

// Prepare readies argv to run in the folder of the file that defined the action, with the
// terminal's own input and output left to the caller. Stopping it sends an interrupt first, so a
// tool like flutter can shut down its own children.
func (a Action) Prepare(ctx context.Context, argv []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = a.Dir
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 10 * time.Second
	return cmd
}

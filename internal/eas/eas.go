// Package eas reads and starts Expo Application Services builds through the project's own eas
// command. It never talks to Expo directly, so it uses the login the developer already has.
package eas

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/run"
)

const (
	listTimeout = 90 * time.Second
	// Downloading a build and starting one both upload or download a lot.
	transferTimeout = 15 * time.Minute
	actionTimeout   = 2 * time.Minute

	// ConfigFile is the optional per-project settings file.
	ConfigFile = ".lazymobile.json"

	installHint = "Install it with: npm install --global eas-cli"
)

// Project is an Expo project that uses EAS.
type Project struct {
	// Dir is the folder that holds eas.json. Commands run there.
	Dir string
	// Command is how to run the eas CLI, such as ["eas"]. Empty when it was not found.
	Command []string
	// Profiles are the build profiles in eas.json, in alphabetical order.
	Profiles []string
	// BuildCommand, when set, starts a build instead of `eas build`. "{profile}" and "{platform}"
	// are replaced. Projects that guard builds behind their own scripts use this.
	BuildCommand []string
	// Configured is set when Command came from the settings file, which can be part of a
	// repository someone else wrote, and not from the project's own copy or PATH.
	Configured bool
}

// config is the "eas" part of the settings file.
type config struct {
	EAS struct {
		Command      []string `json:"command"`
		BuildCommand []string `json:"build_command"`
	} `json:"eas"`
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// findDir looks for eas.json in start and its parents, then one level down in the usual
// monorepo layout (apps/<name>/eas.json).
func findDir(start string) string {
	for dir := start; ; dir = filepath.Dir(dir) {
		if exists(filepath.Join(dir, "eas.json")) {
			return dir
		}
		// A repository root is as far up as a project can be.
		if exists(filepath.Join(dir, ".git")) || dir == filepath.Dir(dir) {
			break
		}
	}
	matches, _ := filepath.Glob(filepath.Join(start, "apps", "*", "eas.json"))
	sort.Strings(matches)
	if len(matches) > 0 {
		return filepath.Dir(matches[0])
	}
	return ""
}

// Detect finds the Expo project for the folder start. lookPath finds a program on PATH; pass
// nil for the real one.
func Detect(start string, lookPath func(string) (string, error)) (Project, bool) {
	dir := findDir(start)
	if dir == "" {
		return Project{}, false
	}
	project := Project{Dir: dir}

	var easJSON struct {
		Build map[string]json.RawMessage `json:"build"`
	}
	if content, err := os.ReadFile(filepath.Join(dir, "eas.json")); err == nil && json.Unmarshal(content, &easJSON) == nil {
		for profile := range easJSON.Build {
			project.Profiles = append(project.Profiles, profile)
		}
		sort.Strings(project.Profiles)
	}

	// Settings next to eas.json win over settings in the folder lazymobile was started from.
	for _, folder := range []string{dir, start} {
		var c config
		if content, err := os.ReadFile(filepath.Join(folder, ConfigFile)); err == nil && json.Unmarshal(content, &c) == nil {
			if len(project.Command) == 0 && len(c.EAS.Command) > 0 {
				project.Command, project.Configured = c.EAS.Command, true
			}
			if len(project.BuildCommand) == 0 {
				project.BuildCommand = c.EAS.BuildCommand
			}
		}
	}
	if len(project.Command) > 0 {
		return project, true
	}

	// The copy the project pins comes before a global one, so the versions match.
	if local := filepath.Join(dir, "node_modules", ".bin", "eas"); exists(local) {
		project.Command = []string{local}
		return project, true
	}
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if global, err := lookPath("eas"); err == nil {
		project.Command = []string{global}
	}
	return project, true
}

// Build statuses, as EAS names them.
const (
	StatusNew           = "NEW"
	StatusInQueue       = "IN_QUEUE"
	StatusInProgress    = "IN_PROGRESS"
	StatusPendingCancel = "PENDING_CANCEL"
	StatusErrored       = "ERRORED"
	StatusFinished      = "FINISHED"
	StatusCanceled      = "CANCELED"
)

// Build is one EAS build.
type Build struct {
	ID string `json:"id"`
	// Status is one of the Status constants.
	Status string `json:"status"`
	// Platform is "android" or "ios".
	Platform string `json:"platform"`
	Profile  string `json:"profile"`
	// Distribution is "INTERNAL", "SIMULATOR" or "STORE".
	Distribution string `json:"distribution,omitempty"`
	// Simulator is true for an iOS build that runs in the simulator.
	Simulator       bool      `json:"simulator"`
	AppVersion      string    `json:"app_version,omitempty"`
	AppBuildVersion string    `json:"app_build_version,omitempty"`
	CommitHash      string    `json:"commit_hash,omitempty"`
	CommitMessage   string    `json:"commit_message,omitempty"`
	Message         string    `json:"message,omitempty"`
	StartedBy       string    `json:"started_by,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	CompletedAt     time.Time `json:"completed_at,omitzero"`
	// Error is why an errored build failed.
	Error string `json:"error,omitempty"`
	// HasArchive is true when the app itself can be downloaded.
	HasArchive bool `json:"has_archive"`
	// URL is the build's page on expo.dev, when the account and project are known.
	URL string `json:"url,omitempty"`
}

// Active reports whether the build is still waiting or running.
func (b Build) Active() bool {
	switch b.Status {
	case StatusNew, StatusInQueue, StatusInProgress, StatusPendingCancel:
		return true
	}
	return false
}

// Installable says whether the build can be put on a simulator or emulator, and why not.
func (b Build) Installable() (bool, string) {
	switch {
	case b.Status != StatusFinished:
		return false, "The build has not finished."
	case !b.HasArchive:
		return false, "The build has no app to download. It may have expired."
	case b.Platform == "ios" && !b.Simulator:
		return false, "This iOS build is for real devices. Only simulator builds can be installed from here."
	case b.Platform == "android" && b.Distribution == "STORE":
		return false, "This is a store build (.aab), which cannot be installed directly."
	}
	return true, ""
}

type rawBuild struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	Platform     string `json:"platform"`
	BuildProfile string `json:"buildProfile"`
	Distribution string `json:"distribution"`
	IsSimulator  bool   `json:"isForIosSimulator"`
	AppVersion   string `json:"appVersion"`
	AppBuild     string `json:"appBuildVersion"`
	CommitHash   string `json:"gitCommitHash"`
	CommitMsg    string `json:"gitCommitMessage"`
	Message      string `json:"message"`
	CreatedAt    string `json:"createdAt"`
	CompletedAt  string `json:"completedAt"`
	Error        *struct {
		Message string `json:"message"`
	} `json:"error"`
	Artifacts *struct {
		ApplicationArchiveURL string `json:"applicationArchiveUrl"`
		BuildURL              string `json:"buildUrl"`
	} `json:"artifacts"`
	Actor *struct {
		DisplayName string `json:"displayName"`
	} `json:"initiatingActor"`
	App *struct {
		Slug         string `json:"slug"`
		OwnerAccount *struct {
			Name string `json:"name"`
		} `json:"ownerAccount"`
	} `json:"app"`
}

func parseTime(value string) time.Time {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return t
}

// ParseBuilds reads the output of `eas build:list --json`.
func ParseBuilds(output string) ([]Build, error) {
	// The JSON is the whole of stdout, but a wrapper script may print a line before it.
	start := strings.Index(output, "[")
	if start < 0 {
		return nil, apperr.New(apperr.ParseFailed, "Could not read the build list from eas")
	}
	var raw []json.RawMessage
	if err := json.Unmarshal([]byte(output[start:]), &raw); err != nil {
		return nil, apperr.New(apperr.ParseFailed, "Could not read the build list from eas")
	}
	builds := make([]Build, 0, len(raw))
	for _, item := range raw {
		var r rawBuild
		// One entry in an unexpected shape must not hide the others.
		if json.Unmarshal(item, &r) != nil || r.ID == "" {
			continue
		}
		b := Build{
			ID: r.ID, Status: r.Status, Platform: strings.ToLower(r.Platform), Profile: r.BuildProfile,
			Distribution: r.Distribution, Simulator: r.IsSimulator, AppVersion: r.AppVersion, AppBuildVersion: r.AppBuild,
			CommitHash: r.CommitHash, CommitMessage: firstLine(r.CommitMsg), Message: firstLine(r.Message),
			CreatedAt: parseTime(r.CreatedAt), CompletedAt: parseTime(r.CompletedAt),
		}
		if r.Error != nil {
			b.Error = r.Error.Message
		}
		if r.Artifacts != nil {
			b.HasArchive = r.Artifacts.ApplicationArchiveURL != "" || r.Artifacts.BuildURL != ""
		}
		if r.Actor != nil {
			b.StartedBy = r.Actor.DisplayName
		}
		if r.App != nil && r.App.OwnerAccount != nil && r.App.Slug != "" && r.App.OwnerAccount.Name != "" {
			b.URL = fmt.Sprintf("https://expo.dev/accounts/%s/projects/%s/builds/%s", r.App.OwnerAccount.Name, r.App.Slug, r.ID)
		}
		builds = append(builds, b)
	}
	return builds, nil
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return line
}

// Client runs eas for one project.
type Client struct {
	Runner  run.Runner
	Project Project
}

// Name is the project folder's name, for titles.
func (c *Client) Name() string {
	return filepath.Base(c.Project.Dir)
}

// ConfiguredCommand is the eas command named in .lazymobile.json, or nil when eas is found the
// usual way. Such a command is the file's to choose, so it is shown and confirmed before it runs.
func (c *Client) ConfiguredCommand() []string {
	if c.Project.Configured {
		return c.Project.Command
	}
	return nil
}

// Available reports whether an eas command was found.
func (c *Client) Available() bool { return len(c.Project.Command) > 0 }

// LoginCommand is the command that logs in to Expo, or nil when eas was not found.
func (c *Client) LoginCommand() []string {
	if !c.Available() {
		return nil
	}
	return append(append([]string(nil), c.Project.Command...), "login")
}

// LoggedIn asks eas who is logged in. It is false, with no error, when nobody is.
func (c *Client) LoggedIn(ctx context.Context) (bool, error) {
	_, _, err := c.eas(ctx, listTimeout, "whoami")
	if err == nil {
		return true, nil
	}
	if e, ok := err.(*apperr.Error); ok && e.Code == apperr.CommandFailed && strings.Contains(e.Hint, "eas login") {
		return false, nil
	}
	return false, err
}

// Profiles are the project's build profiles.
func (c *Client) Profiles() []string {
	return c.Project.Profiles
}

func (c *Client) eas(ctx context.Context, timeout time.Duration, args ...string) (run.Result, []string, error) {
	if len(c.Project.Command) == 0 {
		return run.Result{}, nil, apperr.New(apperr.ToolMissing, "The eas command was not found").WithHint(installHint)
	}
	argv := append(append([]string(nil), c.Project.Command...), args...)
	res := c.Runner.Run(ctx, argv, run.Options{Timeout: timeout, Dir: c.Project.Dir})
	return res, argv, c.explain(apperr.FromResult(res, argv, installHint), res)
}

// explain adds advice to the failures a developer is most likely to meet.
func (c *Client) explain(err error, res run.Result) error {
	e, ok := err.(*apperr.Error)
	if !ok || e.Code != apperr.CommandFailed {
		return err
	}
	output := res.Stderr + "\n" + res.Stdout
	// eas prints update notices and advice first, so the first line is rarely the reason.
	if reason := lastError(output); reason != "" {
		e.Message = "eas failed: " + reason
	}
	text := strings.ToLower(output)
	switch {
	case strings.Contains(text, "eas login") || strings.Contains(text, "not logged in"):
		e.Message, e.Hint = "You are not logged in to Expo", "Run: eas login"
	case strings.Contains(text, "eas init") || strings.Contains(text, "project is not configured"):
		e.Hint = "This project is not linked to EAS yet. Run: eas init"
	case strings.Contains(text, "config --json exited with non-zero code"):
		// eas sets EXPO_NO_DOTENV, so a config that validates variables kept in .env fails under eas
		// while a plain `npx expo config` works.
		e.Hint = "The project's app config could not be loaded. Run `EXPO_NO_DOTENV=1 npx expo config` in " + c.Project.Dir +
			" to see why. Either its dependencies are not installed, or it needs variables from .env, which eas never reads:" +
			" set them in your shell, or in the command in .lazymobile.json."
	}
	return e
}

// lastError is the text of the last "Error: ..." line eas printed, or "".
func lastError(output string) string {
	var last string
	for _, line := range strings.Split(output, "\n") {
		if reason, found := strings.CutPrefix(strings.TrimSpace(line), "Error: "); found && reason != "" {
			last = reason
		}
	}
	return last
}

// List returns the newest builds of the project.
func (c *Client) List(ctx context.Context, limit int) ([]Build, error) {
	res, _, err := c.eas(ctx, listTimeout, "build:list", "--json", "--non-interactive", "--limit", fmt.Sprint(limit))
	if err != nil {
		return nil, err
	}
	return ParseBuilds(res.Stdout)
}

// Download fetches a simulator or emulator build and returns where the app is.
func (c *Client) Download(ctx context.Context, id string) (string, error) {
	res, argv, err := c.eas(ctx, transferTimeout, "build:download", "--build-id", id, "--json", "--non-interactive")
	if err != nil {
		return "", err
	}
	var out struct {
		Path string `json:"path"`
	}
	start := strings.Index(res.Stdout, "{")
	if start < 0 || json.Unmarshal([]byte(res.Stdout[start:]), &out) != nil || out.Path == "" {
		return "", &apperr.Error{Code: apperr.ParseFailed, Message: "eas did not say where it saved the build", Argv: argv}
	}
	return out.Path, nil
}

// Cancel stops a build that is waiting or running.
func (c *Client) Cancel(ctx context.Context, id string) error {
	_, _, err := c.eas(ctx, actionTimeout, "build:cancel", id, "--non-interactive")
	return err
}

// StartCommand is the command Start runs, so it can be shown before it is run.
func (c *Client) StartCommand(profile, platform string) []string {
	if len(c.Project.BuildCommand) > 0 {
		replace := strings.NewReplacer("{profile}", profile, "{platform}", platform)
		argv := make([]string, len(c.Project.BuildCommand))
		for i, part := range c.Project.BuildCommand {
			argv[i] = replace.Replace(part)
		}
		return argv
	}
	if len(c.Project.Command) == 0 {
		return nil
	}
	return append(append([]string(nil), c.Project.Command...),
		"build", "--profile", profile, "--platform", platform, "--non-interactive", "--no-wait")
}

// Start begins a build and returns without waiting for it to finish. platform is "android" or
// "ios". It costs build time on the Expo account, so callers confirm first.
func (c *Client) Start(ctx context.Context, profile, platform string) error {
	argv := c.StartCommand(profile, platform)
	if argv == nil {
		return apperr.New(apperr.ToolMissing, "The eas command was not found").WithHint(installHint)
	}
	res := c.Runner.Run(ctx, argv, run.Options{Timeout: transferTimeout, Dir: c.Project.Dir})
	return c.explain(apperr.FromResult(res, argv, installHint), res)
}

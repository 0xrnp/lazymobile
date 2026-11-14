package eas

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/run"
	"github.com/0xrnp/lazymobile/internal/run/runtest"
)

// Made-up builds in the shape `eas build:list --json` prints (eas-cli 24).
const buildList = `[
  {"id": "11111111-aaaa-4bbb-8ccc-000000000001", "status": "FINISHED", "platform": "ANDROID",
   "buildProfile": "development", "distribution": "INTERNAL", "isForIosSimulator": false,
   "appVersion": "1.4.0", "appBuildVersion": "42", "gitCommitHash": "abc1234", "gitCommitMessage": "Fix blank feed\n\nLonger text",
   "createdAt": "2026-11-14T08:00:00.000Z", "completedAt": "2026-11-14T08:12:30.000Z",
   "artifacts": {"buildUrl": "https://example.invalid/a.apk", "applicationArchiveUrl": "https://example.invalid/a.apk"},
   "initiatingActor": {"displayName": "dev-one"},
   "app": {"slug": "sample-app", "ownerAccount": {"name": "sample-org"}}},
  {"id": "11111111-aaaa-4bbb-8ccc-000000000002", "status": "IN_PROGRESS", "platform": "IOS",
   "buildProfile": "preview", "distribution": "SIMULATOR", "isForIosSimulator": true,
   "createdAt": "2026-11-14T09:00:00.000Z", "completedAt": null, "artifacts": null, "error": null},
  {"id": "11111111-aaaa-4bbb-8ccc-000000000003", "status": "ERRORED", "platform": "IOS",
   "buildProfile": "production", "distribution": "STORE", "isForIosSimulator": false,
   "createdAt": "2026-11-13T09:00:00.000Z", "error": {"errorCode": "X", "message": "Provisioning profile has expired"}},
  {"status": "FINISHED", "platform": "ANDROID"},
  "not a build"
]`

func TestParseBuilds(t *testing.T) {
	builds, err := ParseBuilds(buildList)
	if err != nil {
		t.Fatal(err)
	}
	if len(builds) != 3 {
		t.Fatalf("got %d builds, want 3 (damaged entries are skipped)", len(builds))
	}
	first := builds[0]
	if first.Platform != "android" || first.Profile != "development" || first.Status != StatusFinished ||
		first.AppVersion != "1.4.0" || first.CommitMessage != "Fix blank feed" || first.StartedBy != "dev-one" || !first.HasArchive {
		t.Errorf("first = %+v", first)
	}
	if want := "https://expo.dev/accounts/sample-org/projects/sample-app/builds/11111111-aaaa-4bbb-8ccc-000000000001"; first.URL != want {
		t.Errorf("url = %q, want %q", first.URL, want)
	}
	if got := first.CompletedAt.Sub(first.CreatedAt).String(); got != "12m30s" {
		t.Errorf("duration = %s", got)
	}
	if !builds[1].Active() || builds[1].URL != "" || !builds[1].CompletedAt.IsZero() {
		t.Errorf("second = %+v", builds[1])
	}
	if builds[2].Error != "Provisioning profile has expired" || builds[2].Active() {
		t.Errorf("third = %+v", builds[2])
	}
}

func TestParseBuildsToleratesAndRejects(t *testing.T) {
	if builds, err := ParseBuilds("npm warn something\n[]"); err != nil || len(builds) != 0 {
		t.Errorf("a line before the JSON: builds %v, err %v", builds, err)
	}
	for _, text := range []string{"", "Not logged in", "[oops"} {
		if _, err := ParseBuilds(text); apperr.CodeOf(err) != apperr.ParseFailed {
			t.Errorf("ParseBuilds(%q) error = %v, want parse_failed", text, err)
		}
	}
}

func TestInstallable(t *testing.T) {
	tests := []struct {
		name  string
		build Build
		want  bool
	}{
		{"android apk", Build{Status: StatusFinished, Platform: "android", Distribution: "INTERNAL", HasArchive: true}, true},
		{"ios simulator build", Build{Status: StatusFinished, Platform: "ios", Simulator: true, HasArchive: true}, true},
		{"ios device build", Build{Status: StatusFinished, Platform: "ios", HasArchive: true}, false},
		{"android store bundle", Build{Status: StatusFinished, Platform: "android", Distribution: "STORE", HasArchive: true}, false},
		{"still building", Build{Status: StatusInProgress, Platform: "android"}, false},
		{"expired", Build{Status: StatusFinished, Platform: "android"}, false},
	}
	for _, tt := range tests {
		ok, reason := tt.build.Installable()
		if ok != tt.want || (!ok && reason == "") {
			t.Errorf("%s: Installable = %v, %q", tt.name, ok, reason)
		}
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func noGlobal(string) (string, error) { return "", errors.New("not found") }

func TestDetect(t *testing.T) {
	const easJSON = `{"cli": {}, "build": {"preview": {}, "development": {}, "production": {}}}`

	t.Run("no project", func(t *testing.T) {
		root := t.TempDir()
		write(t, filepath.Join(root, ".git", "HEAD"), "")
		if _, found := Detect(root, noGlobal); found {
			t.Error("found a project in an empty repository")
		}
	})

	t.Run("in a parent folder, with the project's own eas", func(t *testing.T) {
		root := t.TempDir()
		write(t, filepath.Join(root, "eas.json"), easJSON)
		write(t, filepath.Join(root, "node_modules", ".bin", "eas"), "")
		deep := filepath.Join(root, "src", "screens")
		if err := os.MkdirAll(deep, 0o755); err != nil {
			t.Fatal(err)
		}
		project, found := Detect(deep, func(string) (string, error) { return "/usr/local/bin/eas", nil })
		if !found || project.Dir != root {
			t.Fatalf("project = %+v, found = %v", project, found)
		}
		if want := []string{"development", "preview", "production"}; !reflect.DeepEqual(project.Profiles, want) {
			t.Errorf("profiles = %v", project.Profiles)
		}
		if want := []string{filepath.Join(root, "node_modules", ".bin", "eas")}; !reflect.DeepEqual(project.Command, want) {
			t.Errorf("command = %v, want the project's pinned copy", project.Command)
		}
	})

	t.Run("in a monorepo app folder, with a global eas", func(t *testing.T) {
		root := t.TempDir()
		write(t, filepath.Join(root, ".git", "HEAD"), "")
		write(t, filepath.Join(root, "apps", "resident", "eas.json"), easJSON)
		project, found := Detect(root, func(string) (string, error) { return "/usr/local/bin/eas", nil })
		if !found || project.Dir != filepath.Join(root, "apps", "resident") || !reflect.DeepEqual(project.Command, []string{"/usr/local/bin/eas"}) {
			t.Errorf("project = %+v, found = %v", project, found)
		}
	})

	t.Run("does not look above the repository", func(t *testing.T) {
		outer := t.TempDir()
		write(t, filepath.Join(outer, "eas.json"), easJSON)
		repo := filepath.Join(outer, "other-repo")
		write(t, filepath.Join(repo, ".git", "HEAD"), "")
		if _, found := Detect(repo, noGlobal); found {
			t.Error("used an eas.json from outside the repository")
		}
	})

	t.Run("settings file", func(t *testing.T) {
		root := t.TempDir()
		write(t, filepath.Join(root, "eas.json"), easJSON)
		write(t, filepath.Join(root, ConfigFile), `{"eas": {"command": ["pnpm", "exec", "eas"], "build_command": ["make", "build-{profile}", "PLATFORM={platform}"]}}`)
		project, _ := Detect(root, noGlobal)
		if !reflect.DeepEqual(project.Command, []string{"pnpm", "exec", "eas"}) || len(project.BuildCommand) != 3 {
			t.Errorf("project = %+v", project)
		}
	})

	t.Run("without any eas command", func(t *testing.T) {
		root := t.TempDir()
		write(t, filepath.Join(root, "eas.json"), easJSON)
		project, found := Detect(root, noGlobal)
		if !found || len(project.Command) != 0 {
			t.Fatalf("project = %+v", project)
		}
		_, err := (&Client{Runner: &runtest.Fake{}, Project: project}).List(context.Background(), 5)
		if apperr.CodeOf(err) != apperr.ToolMissing || !strings.Contains(err.Error(), "npm install --global eas-cli") {
			t.Errorf("error = %v", err)
		}
	})
}

func client(fake *runtest.Fake) *Client {
	return &Client{Runner: fake, Project: Project{Dir: "/work/app", Command: []string{"eas"}}}
}

func TestListRunsInTheProjectFolder(t *testing.T) {
	var seen run.Options
	fake := &runtest.Fake{Answer: func(_ string, _ []string, opts run.Options) run.Result {
		seen = opts
		return runtest.Out(buildList)
	}}
	builds, err := client(fake).List(context.Background(), 15)
	if err != nil || len(builds) != 3 {
		t.Fatalf("builds = %d, err = %v", len(builds), err)
	}
	if seen.Dir != "/work/app" || !fake.Called("eas build:list --json --non-interactive --limit 15") {
		t.Errorf("dir = %q, ran %v", seen.Dir, fake.Calls())
	}
}

func TestListExplainsAMissingLogin(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{
		"build:list": runtest.Fail(1, "An Expo user account is required to proceed.\nEither log in with eas login or set the EXPO_TOKEN environment variable"),
	})
	_, err := client(fake).List(context.Background(), 15)
	if apperr.CodeOf(err) != apperr.CommandFailed || !strings.Contains(err.Error(), "not logged in to Expo") || !strings.Contains(err.Error(), "Run: eas login") {
		t.Errorf("error = %v", err)
	}
}

// eas prints notices before its error, and the real reason must not be lost behind them. The
// output is from eas-cli 24.7 in a project whose dependencies were not installed.
func TestListExplainsAConfigThatDoesNotLoad(t *testing.T) {
	stderr := `★ eas-cli@27.10.0 is now available.
To upgrade, run:
npm install -g eas-cli
Proceeding with outdated version.

Found eas-cli in your project dependencies.
It's recommended to use the "cli.version" field in eas.json to enforce the eas-cli version for your project.
Learn more: https://github.com/expo/eas-cli#enforcing-eas-cli-version-for-your-project

/work/node_modules/.pnpm/expo@57.0.23/node_modules/expo/bin/cli config --json exited with non-zero code: 1
    Error: build:list command failed.`
	fake := runtest.Table(map[string]run.Result{"build:list": runtest.Fail(1, stderr)})
	_, err := client(fake).List(context.Background(), 15)
	if apperr.CodeOf(err) != apperr.CommandFailed {
		t.Fatalf("error = %v", err)
	}
	text := err.Error()
	for _, want := range []string{"eas failed: build:list command failed.", "app config could not be loaded", "/work/app", "EXPO_NO_DOTENV=1", ".env"} {
		if !strings.Contains(text, want) {
			t.Errorf("error should mention %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Found eas-cli") || strings.Contains(text, "now available") {
		t.Errorf("a notice was reported as the reason:\n%s", text)
	}
}

func TestDownload(t *testing.T) {
	ok := runtest.Table(map[string]run.Result{"build:download": runtest.Out(`{"path": "/cache/eas/Sample.app"}`)})
	path, err := client(ok).Download(context.Background(), "BUILD-1")
	if err != nil || path != "/cache/eas/Sample.app" {
		t.Errorf("path = %q, err = %v", path, err)
	}
	if !ok.Called("eas build:download --build-id BUILD-1 --json --non-interactive") {
		t.Errorf("ran %v", ok.Calls())
	}
	silent := runtest.Table(map[string]run.Result{"build:download": runtest.Out("done")})
	if _, err := client(silent).Download(context.Background(), "BUILD-1"); apperr.CodeOf(err) != apperr.ParseFailed {
		t.Errorf("error = %v", err)
	}
}

func TestStartAndCancel(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{"eas": {}})
	c := client(fake)
	if err := c.Start(context.Background(), "preview", "ios"); err != nil {
		t.Fatal(err)
	}
	// --no-wait: the build runs on Expo's servers, and the dashboard shows its progress.
	if !fake.Called("eas build --profile preview --platform ios --non-interactive --no-wait") {
		t.Errorf("ran %v", fake.Calls())
	}
	if err := c.Cancel(context.Background(), "BUILD-1"); err != nil || !fake.Called("eas build:cancel BUILD-1 --non-interactive") {
		t.Errorf("err = %v, ran %v", err, fake.Calls())
	}
}

func TestStartUsesTheProjectsOwnBuildCommand(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{"make": {}})
	c := client(fake)
	c.Project.BuildCommand = []string{"make", "eas-build-{profile}", "PLATFORM={platform}"}
	if err := c.Start(context.Background(), "preview", "android"); err != nil {
		t.Fatal(err)
	}
	calls := fake.Calls()
	if len(calls) != 1 || calls[0] != "make eas-build-preview PLATFORM=android" {
		t.Errorf("ran %v, want only the project's command", calls)
	}
}

package actions

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
)

func write(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ConfigFile), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func repo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestLoadAndResolve(t *testing.T) {
	root := repo(t)
	write(t, root, `{"actions":[{"name":"Flutter run","command":["flutter","run","-d","{device_id}"]},
		{"name":"Gradle","platform":"android","command":["./gradlew","installDebug"]}]}`)
	list, err := Load(root)
	if err != nil || len(list) != 2 {
		t.Fatalf("Load = %v, %v", list, err)
	}
	if list[0].Dir != root {
		t.Errorf("Dir = %q, want %q", list[0].Dir, root)
	}
	emu := device.Device{ID: "Pixel", Name: "Pixel", Platform: device.Android, Serial: "emulator-5556"}
	argv, err := list[0].Resolve(emu, "")
	if want := []string{"flutter", "run", "-d", "emulator-5556"}; err != nil || !reflect.DeepEqual(argv, want) {
		t.Errorf("Resolve = %v, %v; want %v", argv, err, want)
	}
	sim := device.Device{ID: "UDID", Name: "iPhone", Platform: device.IOS}
	if got := For(list, sim); len(got) != 1 || got[0].Name != "Flutter run" {
		t.Errorf("For(iOS) = %v, want only Flutter run", got)
	}
	if _, err := list[1].Resolve(sim, ""); apperr.CodeOf(err) != apperr.Unsupported {
		t.Errorf("an Android action on an iPhone gave %v, want unsupported", err)
	}
}

func TestResolveNeedsAppOnlyWhenUsed(t *testing.T) {
	a := Action{Name: "Reset", Command: []string{"adb", "shell", "pm", "clear", "{app}"}}
	d := device.Device{ID: "x", Platform: device.Android}
	if _, err := a.Resolve(d, ""); apperr.CodeOf(err) != apperr.Usage {
		t.Errorf("missing app gave %v, want usage", err)
	}
	argv, err := a.Resolve(d, "com.example.app")
	if err != nil || argv[4] != "com.example.app" {
		t.Errorf("Resolve = %v, %v", argv, err)
	}
}

func TestNearerFileWinsAndCommandsRunWhereTheyAreDefined(t *testing.T) {
	root := repo(t)
	sub := filepath.Join(root, "apps", "mobile")
	write(t, root, `{"actions":[{"name":"Run","command":["root-run"]},{"name":"Lint","command":["lint"]}]}`)
	write(t, sub, `{"actions":[{"name":"Run","command":["sub-run"]}]}`)
	list, err := Load(sub)
	if err != nil || len(list) != 2 {
		t.Fatalf("Load = %v, %v", list, err)
	}
	if list[0].Command[0] != "sub-run" || list[0].Dir != sub || list[1].Dir != root {
		t.Errorf("got %+v", list)
	}
}

func TestLoadStopsAtTheRepositoryRoot(t *testing.T) {
	parent := t.TempDir()
	write(t, parent, `{"actions":[{"name":"Outside","command":["x"]}]}`)
	root := filepath.Join(parent, "repo")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	list, err := Load(root)
	if err != nil || len(list) != 0 {
		t.Errorf("Load = %v, %v; a file above the repository must not be read", list, err)
	}
}

func TestLoadRejectsBadFiles(t *testing.T) {
	cases := map[string]string{
		"not json":            `{"actions":`,
		"no name":             `{"actions":[{"command":["x"]}]}`,
		"no command":          `{"actions":[{"name":"A"}]}`,
		"empty program":       `{"actions":[{"name":"A","command":[""]}]}`,
		"string command":      `{"actions":[{"name":"A","command":"flutter run"}]}`,
		"unknown placeholder": `{"actions":[{"name":"A","command":["x","{devce_id}"]}]}`,
		"placeholder program": `{"actions":[{"name":"A","command":["{app}"]}]}`,
		"bad platform":        `{"actions":[{"name":"A","platform":"windows","command":["x"]}]}`,
		"duplicate name":      `{"actions":[{"name":"A","command":["x"]},{"name":"A","command":["y"]}]}`,
	}
	for name, content := range cases {
		root := repo(t)
		write(t, root, content)
		_, err := Load(root)
		if apperr.CodeOf(err) != apperr.ParseFailed {
			t.Errorf("%s: got %v, want parse_failed", name, err)
		} else if !strings.Contains(err.Error(), ConfigFile) {
			t.Errorf("%s: the message should name the file: %v", name, err)
		}
	}
}

func TestNoFileMeansNoActions(t *testing.T) {
	list, err := Load(repo(t))
	if err != nil || len(list) != 0 {
		t.Errorf("Load = %v, %v", list, err)
	}
	if _, err := Find(nil, "x"); apperr.CodeOf(err) != apperr.NotFound {
		t.Errorf("Find = %v, want not_found", err)
	}
}

func TestTheEasKeyDoesNotBreakLoading(t *testing.T) {
	root := repo(t)
	write(t, root, `{"eas":{"command":["pnpm","exec","eas"]},"actions":[{"name":"A","command":["x"]}]}`)
	if list, err := Load(root); err != nil || len(list) != 1 {
		t.Errorf("Load = %v, %v", list, err)
	}
}

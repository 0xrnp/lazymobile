package ios

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/run"
	"github.com/0xrnp/lazymobile/internal/run/runtest"
)

// dataContainer makes what a simulator keeps for an app: folders with files and a link in them, and
// the container manager's metadata file.
func dataContainer(t *testing.T) (container, outside string) {
	t.Helper()
	root := t.TempDir()
	container = filepath.Join(root, "Devices", "SIM", "data", "Containers", "Data", "Application", "ABC-123")
	outside = filepath.Join(root, "precious.txt")
	files := map[string]string{
		".com.apple.mobile_container_manager.metadata.plist": "metadata",
		"Documents/e2e.txt":                       "saved",
		"Library/Preferences/dev.example.plist":   "prefs",
		"Library/Caches/com.apple.dyld/cache.bin": "cache",
		"tmp/scratch":                             "scratch",
		"loose.txt":                               "loose",
	}
	for name, content := range files {
		path := filepath.Join(container, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(outside, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A link inside the container to a file outside it must be removed without touching the file.
	if err := os.Symlink(outside, filepath.Join(container, "Documents", "link")); err != nil {
		t.Fatal(err)
	}
	return container, outside
}

func TestClearDataEmptiesTheContainerAndKeepsItsFoldersAndMetadata(t *testing.T) {
	container, outside := dataContainer(t)
	fake := runtest.Table(map[string]run.Result{
		"simctl terminate":         {},
		"simctl get_app_container": runtest.Out(container + "\n"),
	})
	if err := client(fake).ClearData(context.Background(), bootedSim, "com.example.app"); err != nil {
		t.Fatal(err)
	}
	// The app is stopped before its files go, or it would write its preferences back.
	calls := fake.Calls()
	if len(calls) != 2 || !fake.Called("simctl terminate "+udidBooted+" com.example.app") || !fake.Called("get_app_container "+udidBooted+" com.example.app data") {
		t.Errorf("calls %v", calls)
	}
	var left []string
	filepath.WalkDir(container, func(path string, d os.DirEntry, err error) error {
		if rel, _ := filepath.Rel(container, path); rel != "." {
			left = append(left, rel)
		}
		return nil
	})
	want := map[string]bool{".com.apple.mobile_container_manager.metadata.plist": true, "Documents": true, "Library": true, "tmp": true}
	if len(left) != len(want) {
		t.Fatalf("left %v", left)
	}
	for _, name := range left {
		if !want[name] {
			t.Errorf("%s was left", name)
		}
	}
	if content, err := os.ReadFile(outside); err != nil || string(content) != "keep" {
		t.Errorf("a file outside the container: %q, %v", content, err)
	}
}

func TestClearDataRefusesAFolderThatIsNotADataContainerAndAnAppThatIsNotInstalled(t *testing.T) {
	ctx := context.Background()
	elsewhere := t.TempDir()
	if err := os.WriteFile(filepath.Join(elsewhere, "mine.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{"the app itself": "/path/to/Example.app", "an unrelated folder": elsewhere, "nothing": ""} {
		fake := runtest.Table(map[string]run.Result{"simctl terminate": {}, "simctl get_app_container": runtest.Out(path + "\n")})
		if err := client(fake).ClearData(ctx, bootedSim, "com.example.app"); apperr.CodeOf(err) != apperr.ParseFailed {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(elsewhere, "mine.txt")); err != nil {
		t.Errorf("a file outside any container was removed: %v", err)
	}

	typo := runtest.Table(map[string]run.Result{
		"simctl terminate":         runtest.Fail(3, simctlNothingToTerminate),
		"simctl get_app_container": runtest.Fail(2, simctlNoContainer),
	})
	if err := client(typo).ClearData(ctx, bootedSim, "com.example.ap"); apperr.CodeOf(err) != apperr.NotFound {
		t.Errorf("not installed: %v", err)
	}
	phone := runtest.Table(map[string]run.Result{})
	if err := client(phone).ClearData(ctx, iphone, "com.example.app"); apperr.CodeOf(err) != apperr.Unsupported || len(phone.Calls()) != 0 {
		t.Errorf("an iPhone: %v, calls %v", err, phone.Calls())
	}
}

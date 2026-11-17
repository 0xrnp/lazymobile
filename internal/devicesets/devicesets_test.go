package devicesets

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/0xrnp/lazymobile/internal/apperr"
)

func project(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for dir, content := range files {
		full := filepath.Join(root, dir)
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(full, ConfigFile), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestLoadAndNearerFileWins(t *testing.T) {
	root := project(t, map[string]string{
		".":           `{"device_sets": {"matrix": ["iPhone 17"], "tablets": ["Pixel Tablet*"]}}`,
		"apps/mobile": `{"device_sets": {"matrix": ["Pixel 9a"]}}`,
	})
	sets, err := Load(filepath.Join(root, "apps", "mobile"))
	if err != nil {
		t.Fatal(err)
	}
	want := Sets{"matrix": {"Pixel 9a"}, "tablets": {"Pixel Tablet*"}}
	if !reflect.DeepEqual(sets, want) {
		t.Errorf("sets = %v, want %v", sets, want)
	}
}

func TestLoadRejectsBadSets(t *testing.T) {
	for name, content := range map[string]string{
		"not json": `{"device_sets":`,
		"empty":    `{"device_sets": {"m": []}}`,
		"blank":    `{"device_sets": {"m": ["a", " "]}}`,
	} {
		if _, err := Load(project(t, map[string]string{".": content})); apperr.CodeOf(err) != apperr.ParseFailed {
			t.Errorf("%s: %v, want parse_failed", name, err)
		}
	}
}

func TestUnknownSetListsTheKnownOnes(t *testing.T) {
	_, err := Sets{"b": {"x"}, "a": {"y"}}.Patterns("c")
	if apperr.CodeOf(err) != apperr.NotFound || !strings.Contains(err.(*apperr.Error).Hint, "a, b") {
		t.Errorf("err = %v", err)
	}
	if _, err := (Sets{}).Patterns("c"); !strings.Contains(err.(*apperr.Error).Hint, "device_sets") {
		t.Errorf("no sets at all should say how to add one: %v", err)
	}
}

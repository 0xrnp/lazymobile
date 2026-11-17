package tui

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// projectWith is a project folder whose .lazymobile.json holds content.
func projectWith(t *testing.T, content string) Memory {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".lazymobile.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return Memory{Folder: dir}
}

const twoActions = `{"actions": [
  {"name": "Flutter run", "command": ["flutter", "run", "-d", "{device_id}"]},
  {"name": "Gradle", "platform": "android", "command": ["./gradlew", "installDebug"]}]}`

func TestProjectActionShowsTheExactCommandAndAsksFirst(t *testing.T) {
	h := startWith(t, projectWith(t, twoActions), pixel)
	h.press("R")
	h.wantScreen("Run on Pixel 9a", "Flutter run", "Gradle", "flutter run -d {device_id}")

	h.press("enter")
	h.wantScreen("Run Flutter run?", "flutter run -d emulator-5554", "Run it only if you trust this project")

	// Enter must not run a command from a file the user may not have read.
	if _, cmd := h.m.Update(special["enter"]); cmd != nil {
		t.Errorf("enter ran the command")
	}
	h.press("n")
	h.wantNoScreen("Run Flutter run?")

	h.press("R", "enter")
	_, cmd := h.m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if cmd == nil {
		t.Errorf("confirming did not start the command")
	}
}

func TestProjectActionsAreFilteredByPlatform(t *testing.T) {
	h := startWith(t, projectWith(t, twoActions), iphone)
	h.press("R")
	h.wantScreen("Flutter run")
	h.wantNoScreen("Gradle")
}

func TestProjectActionsExplainWhenThereAreNone(t *testing.T) {
	h := startWith(t, projectWith(t, `{}`), pixel)
	h.press("R")
	h.wantScreen("No project actions", ".lazymobile.json", "{device_id}")
}

func TestProjectActionsReportABadFile(t *testing.T) {
	h := startWith(t, projectWith(t, `{"actions": [{"name": "A", "command": ["x", "{nope}"]}]}`), pixel)
	h.press("R")
	h.wantScreen("Could not read the project actions", "{nope}")
}

func TestProjectActionNeedingAnAppSaysSo(t *testing.T) {
	h := startWith(t, projectWith(t, `{"actions": [{"name": "Reset", "command": ["echo", "{app}"]}]}`), pixel)
	h.press("R", "enter")
	h.wantScreen("Cannot run Reset", "needs an app id")
	h.press("esc")
	h.m.lastApp[pixel.Platform] = "com.example.app"
	h.press("R", "enter")
	h.wantScreen("Run Reset?", "echo com.example.app")
}

package android

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/0xrnp/lazymobile/internal/apperr"
)

func TestBuiltAppIDIsReadFromGradlesOutputMetadata(t *testing.T) {
	dir := t.TempDir()
	apk := filepath.Join(dir, "app-dev-debug.apk")
	if _, err := BuiltAppID(apk); apperr.CodeOf(err) != apperr.NotFound {
		t.Errorf("without metadata: %v", err)
	}
	// What the Android Gradle plugin 8.11 wrote for a dev flavor with a .debug suffix.
	metadata := `{"version": 3, "artifactType": {"type": "APK", "kind": "Directory"}, "applicationId": "dev.lazymobile.hellokt.dev.debug", "variantName": "devDebug", "elements": [{"type": "SINGLE", "outputFile": "app-dev-debug.apk"}]}`
	if err := os.WriteFile(filepath.Join(dir, "output-metadata.json"), []byte(metadata), 0o600); err != nil {
		t.Fatal(err)
	}
	if id, err := BuiltAppID(apk); err != nil || id != "dev.lazymobile.hellokt.dev.debug" {
		t.Errorf("%q, %v", id, err)
	}
	if _, ok := ParseOutputMetadata([]byte(`{"version": 3}`)); ok {
		t.Error("metadata without an id was read")
	}
}

package android

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/0xrnp/lazymobile/internal/apperr"
)

// BuiltAppID is the application id of an APK that Gradle built, from the output-metadata.json the
// Android Gradle plugin (4.1 and newer) writes next to it. It is the id the app is installed under,
// with any suffix of its build type or flavor, which the build files do not say on their own.
func BuiltAppID(apk string) (string, error) {
	metadata := filepath.Join(filepath.Dir(apk), "output-metadata.json")
	data, err := os.ReadFile(metadata)
	if err != nil {
		return "", apperr.New(apperr.NotFound, "Cannot tell which app %s is: there is no output-metadata.json beside it", filepath.Base(apk)).
			WithHint("Launch it with its id: lazymobile launch <device> <app-id>.")
	}
	id, ok := ParseOutputMetadata(data)
	if !ok {
		return "", apperr.New(apperr.ParseFailed, "Could not read the application id from %s", metadata)
	}
	return id, nil
}

// ParseOutputMetadata reads the applicationId of Gradle's output-metadata.json.
func ParseOutputMetadata(data []byte) (string, bool) {
	var metadata struct {
		ApplicationID string `json:"applicationId"`
	}
	if err := json.Unmarshal(data, &metadata); err != nil || metadata.ApplicationID == "" {
		return "", false
	}
	return metadata.ApplicationID, true
}

package ios

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/run"
)

// containerMetadataPrefix starts the files the simulator's container manager keeps in a data
// container. Without them the container is no longer the app's.
const containerMetadataPrefix = ".com.apple.mobile_container_manager"

// ClearData erases what an app saved on a booted simulator, as if it had just been installed:
// it stops the app, then empties its data container (Documents, Library with its preferences and
// caches, tmp). The folders stay, since apps expect them. Its app group containers, which other
// apps of the same developer share, and its keychain items are kept. Apple has no command for
// this, so the files are removed here, only below the container that simctl names.
func (c *Client) ClearData(ctx context.Context, d device.Device, bundleID string) error {
	if err := onlySimulator(d, "clear an app's data"); err != nil {
		return err
	}
	// A running app would write its preferences back.
	if err := c.Stop(ctx, d, bundleID); err != nil {
		return err
	}
	argv := []string{"xcrun", "simctl", "get_app_container", d.ID, bundleID, "data"}
	res := c.Runner.Run(ctx, argv, run.Options{Timeout: actionTimeout})
	if err := apperr.FromResult(res, argv, notInstalled); err != nil {
		return err
	}
	container := strings.TrimSpace(res.Stdout)
	// Never delete from a path that is not an app's data container.
	if !filepath.IsAbs(container) || !strings.Contains(container, string(filepath.Separator)+filepath.Join("Containers", "Data", "Application")+string(filepath.Separator)) {
		return &apperr.Error{Code: apperr.ParseFailed, Message: fmt.Sprintf("simctl named a folder that is not an app's data: %q", container), Argv: argv}
	}
	return emptyContainer(container)
}

// emptyContainer removes everything inside each folder of a data container, and the loose files,
// except the container manager's own.
func emptyContainer(container string) error {
	top, err := os.ReadDir(container)
	if err != nil {
		return apperr.New(apperr.CommandFailed, "Cannot read the app's data folder: %v", err)
	}
	for _, entry := range top {
		if strings.HasPrefix(entry.Name(), containerMetadataPrefix) {
			continue
		}
		path := filepath.Join(container, entry.Name())
		if !entry.IsDir() {
			if err := os.Remove(path); err != nil {
				return apperr.New(apperr.CommandFailed, "Cannot remove %s: %v", entry.Name(), err)
			}
			continue
		}
		children, err := os.ReadDir(path)
		if err != nil {
			return apperr.New(apperr.CommandFailed, "Cannot read %s: %v", entry.Name(), err)
		}
		for _, child := range children {
			// RemoveAll removes a symbolic link itself, never what it points at.
			if err := os.RemoveAll(filepath.Join(path, child.Name())); err != nil {
				return apperr.New(apperr.CommandFailed, "Cannot remove %s/%s: %v", entry.Name(), child.Name(), err)
			}
		}
	}
	return nil
}

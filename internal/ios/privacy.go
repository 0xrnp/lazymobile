package ios

import (
	"context"
	"slices"
	"strings"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/run"
)

// permissionServices are the services simctl can grant or revoke. It has none for the camera.
var permissionServices = []string{
	"location", "location-always", "photos", "photos-add", "contacts", "contacts-limited",
	"calendar", "reminders", "microphone", "motion", "media-library", "siri",
}

// PermissionNames are the permissions a booted simulator's app can be given or refused.
func PermissionNames() []string { return slices.Clone(permissionServices) }

var permissionAction = action{verb: "change a permission", hint: "Boot it first."}

// SetPermission grants or revokes a permission of an app, by bundle id, on a booted simulator.
// Some changes end the app if it is running. simctl says success for an app that is not
// installed, so that is checked first.
func (c *Client) SetPermission(ctx context.Context, d device.Device, bundleID, service string, grant bool) error {
	if err := onlySimulator(d, "change a permission"); err != nil {
		return err
	}
	if !slices.Contains(permissionServices, service) {
		return apperr.New(apperr.InvalidState, "%q is not a permission of a simulator", service).
			WithHint("Use one of: %s.", strings.Join(permissionServices, ", "))
	}
	lookup := []string{"xcrun", "simctl", "get_app_container", d.ID, bundleID, "app"}
	if found := c.Runner.Run(ctx, lookup, run.Options{Timeout: actionTimeout}); apperr.FromResult(found, lookup, notInstalled) != nil {
		if found.Missing || found.Err != nil || found.TimedOut {
			return apperr.FromResult(found, lookup, notInstalled)
		}
		return notInstalledApp(d, bundleID, lookup, found)
	}
	verb := "revoke"
	if grant {
		verb = "grant"
	}
	argv := []string{"xcrun", "simctl", "privacy", d.ID, verb, service, bundleID}
	return actionError(c.Runner.Run(ctx, argv, run.Options{Timeout: actionTimeout}), argv, permissionAction)
}

package android

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
)

// androidPermissions are the permissions a person can name, and what each is on every Android
// version. Photos and notifications depend on the version, and are handled apart.
var androidPermissions = map[string][]string{
	"camera":     {"android.permission.CAMERA"},
	"microphone": {"android.permission.RECORD_AUDIO"},
	"location":   {"android.permission.ACCESS_FINE_LOCATION", "android.permission.ACCESS_COARSE_LOCATION"},
	"contacts":   {"android.permission.READ_CONTACTS", "android.permission.WRITE_CONTACTS"},
	"calendar":   {"android.permission.READ_CALENDAR", "android.permission.WRITE_CALENDAR"},
}

// PermissionNames are the permissions of an Android app that a person can name. A full name such
// as android.permission.READ_SMS works too.
func PermissionNames() []string {
	return []string{"camera", "microphone", "location", "photos", "contacts", "calendar", "notifications"}
}

// fullPermission is a permission written out, such as android.permission.READ_SMS.
var fullPermission = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z0-9_]+)+$`)

// permissionsFor is what name means on a device: one or more full permission names.
func (c *Client) permissionsFor(ctx context.Context, adb string, d device.Device, name string) ([]string, error) {
	if list, known := androidPermissions[name]; known {
		return list, nil
	}
	switch name {
	case "photos", "notifications":
		sdk, err := c.sdkLevel(ctx, adb, d)
		if err != nil {
			return nil, err
		}
		switch {
		case name == "notifications" && sdk < 33:
			return nil, apperr.New(apperr.Unsupported, "Notifications are not a permission before Android 13").WithHint("%s runs API %d.", d.Name, sdk)
		case name == "notifications":
			return []string{"android.permission.POST_NOTIFICATIONS"}, nil
		case sdk >= 33:
			return []string{"android.permission.READ_MEDIA_IMAGES"}, nil
		}
		return []string{"android.permission.READ_EXTERNAL_STORAGE"}, nil
	case "":
	default:
		if fullPermission.MatchString(name) {
			return []string{name}, nil
		}
	}
	return nil, apperr.New(apperr.InvalidState, "%q is not a permission", name).
		WithHint("Use one of: %s, or a full name such as android.permission.READ_SMS.", strings.Join(PermissionNames(), ", "))
}

// sdkLevel is the API level of a running device.
func (c *Client) sdkLevel(ctx context.Context, adb string, d device.Device) (int, error) {
	argv := []string{adb, "-s", d.TargetID(), "shell", "getprop", "ro.build.version.sdk"}
	res := c.run(ctx, argv, queryTimeout)
	if err := apperr.FromResult(res, argv, installHint["adb"]); err != nil {
		return 0, err
	}
	level, err := strconv.Atoi(strings.TrimSpace(res.Stdout))
	if err != nil {
		return 0, &apperr.Error{Code: apperr.ParseFailed, Message: "The device did not say its Android version", Argv: argv, Stderr: res.Stdout}
	}
	return level, nil
}

// SetPermission grants or revokes a permission of an app, by package name, on a running emulator
// or phone. A name that stands for several permissions, such as location, changes each the app
// asks for.
func (c *Client) SetPermission(ctx context.Context, d device.Device, id, name string, grant bool) error {
	adb, err := c.appTool(d, id)
	if err != nil {
		return err
	}
	permissions, err := c.permissionsFor(ctx, adb, d, name)
	if err != nil {
		return err
	}
	verb := "revoke"
	if grant {
		verb = "grant"
	}
	var changed bool
	var firstErr error
	for _, permission := range permissions {
		argv := []string{adb, "-s", d.TargetID(), "shell", "pm", verb, id, permission}
		res := c.run(ctx, argv, actionTimeout)
		out := res.Stdout + res.Stderr
		switch {
		case strings.Contains(out, "package not found"):
			return &apperr.Error{Code: apperr.NotFound, Message: fmt.Sprintf("%s is not installed on %s", id, d.Name),
				Hint: fmt.Sprintf("Run `lazymobile apps %q` to see what is installed.", d.Name), Argv: argv, ExitCode: res.Code}
		case strings.Contains(out, "Unknown permission"):
			return apperr.New(apperr.InvalidState, "%s is not a permission on %s", name, d.Name).
				WithHint("Use one of: %s, or a full name such as android.permission.READ_SMS.", strings.Join(PermissionNames(), ", "))
		case strings.Contains(out, "has not requested permission"), strings.Contains(out, "not a changeable permission type"):
			if firstErr == nil {
				firstErr = apperr.New(apperr.InvalidState, "%s does not ask for the %s permission", id, name).
					WithHint("An app can only be given the permissions it asks for.")
			}
			continue
		}
		if err := apperr.FromResult(res, argv, installHint["adb"]); err != nil {
			if firstErr == nil || len(permissions) == 1 {
				firstErr = err
			}
			continue
		}
		changed = true
	}
	if !changed {
		return firstErr
	}
	return nil
}

// ClearData erases all the data of an app, by package name, on a running emulator or phone, as
// if it had just been installed. The app is stopped.
func (c *Client) ClearData(ctx context.Context, d device.Device, id string) error {
	adb, err := c.appTool(d, id)
	if err != nil {
		return err
	}
	// pm clear says only "Failed" for a package that does not exist.
	if _, err := c.AppInfo(ctx, d, id); err != nil {
		return err
	}
	argv := []string{adb, "-s", d.TargetID(), "shell", "pm", "clear", id}
	res := c.run(ctx, argv, actionTimeout)
	if err := apperr.FromResult(res, argv, installHint["adb"]); err != nil {
		return err
	}
	if !strings.Contains(res.Stdout, "Success") {
		return &apperr.Error{Code: apperr.CommandFailed, Message: "The device did not clear the data of " + id + ": " + strings.TrimSpace(res.Stdout+res.Stderr), Argv: argv}
	}
	return nil
}

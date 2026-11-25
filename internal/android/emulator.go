package android

import (
	"context"
	"strconv"
	"strings"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
)

// SetLocation puts a running emulator at a place. Only an emulator has a console to ask: a phone
// has no way to be told where it is.
func (c *Client) SetLocation(ctx context.Context, d device.Device, lat, lon float64) error {
	adb, err := c.emulator(d, "set the location")
	if err != nil {
		return err
	}
	// The console takes the longitude first, and answers KO with exit code 0 when it refuses.
	argv := []string{adb, "-s", d.TargetID(), "emu", "geo", "fix",
		strconv.FormatFloat(lon, 'f', -1, 64), strconv.FormatFloat(lat, 'f', -1, 64)}
	res := c.run(ctx, argv, actionTimeout)
	if err := apperr.FromResult(res, argv, installHint["adb"]); err != nil {
		return err
	}
	if out := strings.TrimSpace(res.Stdout + res.Stderr); strings.Contains(out, "KO") {
		return &apperr.Error{Code: apperr.CommandFailed, Message: "The emulator did not set the location: " + out, Argv: argv}
	}
	return nil
}

// emulator checks that d is a running emulator, and returns the path of adb.
func (c *Client) emulator(d device.Device, what string) (string, error) {
	adb, err := c.runningDevice(d)
	if err != nil {
		return "", err
	}
	if d.Kind != device.Emulator {
		return "", apperr.New(apperr.Unsupported, "Cannot %s on %s: it is not an emulator", what, d.Name).
			WithHint("Only emulators can do this: a phone is left alone.")
	}
	return adb, nil
}

// demo sends one command to the status bar's demo mode.
func (c *Client) demo(ctx context.Context, adb string, d device.Device, command string, extras ...string) error {
	argv := []string{adb, "-s", d.TargetID(), "shell", "am", "broadcast", "-a", "com.android.systemui.demo", "-e", "command", command}
	for i := 0; i+1 < len(extras); i += 2 {
		argv = append(argv, "-e", extras[i], extras[i+1])
	}
	return apperr.FromResult(c.run(ctx, argv, actionTimeout), argv, installHint["adb"])
}

// CleanStatusBar gives a running emulator the tidy status bar of a screenshot, through the demo
// mode of System UI: 12:00, a full battery, full signal, and no notification icons.
func (c *Client) CleanStatusBar(ctx context.Context, d device.Device) error {
	adb, err := c.emulator(d, "change the status bar")
	if err != nil {
		return err
	}
	setting := []string{adb, "-s", d.TargetID(), "shell", "settings", "put", "global", "sysui_demo_allowed", "1"}
	if err := apperr.FromResult(c.run(ctx, setting, actionTimeout), setting, installHint["adb"]); err != nil {
		return err
	}
	steps := []struct {
		command string
		extras  []string
	}{
		{"enter", nil},
		{"clock", []string{"hhmm", "1200"}},
		{"battery", []string{"level", "100", "plugged", "false"}},
		{"network", []string{"wifi", "show", "level", "4", "fully", "true"}},
		{"network", []string{"mobile", "show", "datatype", "none", "level", "4", "fully", "true"}},
		{"notifications", []string{"visible", "false"}},
	}
	for _, step := range steps {
		if err := c.demo(ctx, adb, d, step.command, step.extras...); err != nil {
			return err
		}
	}
	return nil
}

// ResetStatusBar leaves the demo mode of a running emulator's status bar.
func (c *Client) ResetStatusBar(ctx context.Context, d device.Device) error {
	adb, err := c.emulator(d, "change the status bar")
	if err != nil {
		return err
	}
	return c.demo(ctx, adb, d, "exit")
}

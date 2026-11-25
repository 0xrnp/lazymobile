package android

import (
	"context"
	"strings"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
)

// networkProfiles are the speeds an emulator can be held to. speed and delay are the names its
// console takes for `network speed` and `network delay`.
var networkProfiles = map[string]struct{ speed, delay string }{
	"gsm":  {"gsm", "gsm"},
	"edge": {"edge", "edge"},
	"3g":   {"umts", "umts"},
	"lte":  {"lte", "none"},
	"full": {"full", "none"},
}

// NetworkModes lists what SetNetwork takes: offline, online, and the speeds, slowest first.
func NetworkModes() []string {
	return []string{"offline", "online", "gsm", "edge", "3g", "lte", "full"}
}

// ValidNetworkMode reports whether mode is one of NetworkModes.
func ValidNetworkMode(mode string) bool {
	_, speed := networkProfiles[mode]
	return speed || mode == "offline" || mode == "online"
}

// SetNetwork changes what a running emulator's network is like: offline (airplane mode), online
// (airplane mode off, full speed), or a speed and delay such as edge. A speed also turns airplane
// mode off, so that a slow network is never an offline one. Only an emulator has a console to ask.
func (c *Client) SetNetwork(ctx context.Context, d device.Device, mode string) error {
	if !ValidNetworkMode(mode) {
		return apperr.New(apperr.InvalidState, "Unknown network mode: %s", mode).WithHint("Use %s.", strings.Join(NetworkModes(), ", "))
	}
	adb, err := c.emulator(d, "change the network")
	if err != nil {
		return err
	}
	airplane := func(on string) error {
		argv := []string{adb, "-s", d.TargetID(), "shell", "cmd", "connectivity", "airplane-mode", on}
		return apperr.FromResult(c.run(ctx, argv, actionTimeout), argv, installHint["adb"])
	}
	if mode == "offline" {
		return airplane("enable")
	}
	profile, ok := networkProfiles[mode]
	if mode == "online" {
		profile, ok = networkProfiles["full"], true
	}
	if !ok {
		return nil
	}
	for _, step := range [][]string{{"speed", profile.speed}, {"delay", profile.delay}} {
		argv := []string{adb, "-s", d.TargetID(), "emu", "network", step[0], step[1]}
		res := c.run(ctx, argv, actionTimeout)
		if err := apperr.FromResult(res, argv, installHint["adb"]); err != nil {
			return err
		}
		// The console answers KO with exit code 0 when it refuses.
		if out := strings.TrimSpace(res.Stdout + res.Stderr); strings.Contains(out, "KO") {
			return &apperr.Error{Code: apperr.CommandFailed, Message: "The emulator did not set the network " + step[0] + ": " + out, Argv: argv}
		}
	}
	return airplane("disable")
}

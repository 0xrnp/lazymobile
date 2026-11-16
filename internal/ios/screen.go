package ios

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/run"
)

// Appearance is "light" or "dark", the mode of a booted simulator.
func (c *Client) Appearance(ctx context.Context, d device.Device) (string, error) {
	if err := onlySimulator(d, "read the appearance"); err != nil {
		return "", err
	}
	argv := []string{"xcrun", "simctl", "ui", d.ID, "appearance"}
	res := c.Runner.Run(ctx, argv, run.Options{Timeout: actionTimeout})
	if err := actionError(res, argv, appearanceAction); err != nil {
		return "", err
	}
	if mode := strings.TrimSpace(res.Stdout); mode == "light" || mode == "dark" {
		return mode, nil
	}
	return "", &apperr.Error{Code: apperr.ParseFailed, Message: "simctl did not say the appearance", Argv: argv, Stderr: res.Stdout}
}

// SetAppearance switches a booted simulator to "light" or "dark".
func (c *Client) SetAppearance(ctx context.Context, d device.Device, mode string) error {
	if err := onlySimulator(d, "change the appearance"); err != nil {
		return err
	}
	argv := []string{"xcrun", "simctl", "ui", d.ID, "appearance", mode}
	return actionError(c.Runner.Run(ctx, argv, run.Options{Timeout: actionTimeout}), argv, appearanceAction)
}

var recordAction = action{verb: "record the screen", hint: "Boot it first."}

// Record films a booted simulator's screen into path until ctx ends or limit passes (no limit when
// it is zero). Both end it with an interrupt, which is how simctl finishes the video, so neither
// is an error as long as a video was saved.
func (c *Client) Record(ctx context.Context, d device.Device, path string, limit time.Duration) error {
	if err := onlySimulator(d, "record the screen"); err != nil {
		return err
	}
	argv := []string{"xcrun", "simctl", "io", d.ID, "recordVideo", path}
	res := c.Runner.Run(ctx, argv, run.Options{Timeout: limit, Interrupt: true})
	if !res.TimedOut && !errors.Is(res.Err, context.Canceled) {
		if err := actionError(res, argv, recordAction); err != nil {
			return err
		}
	}
	if info, err := os.Stat(path); err != nil || info.Size() == 0 {
		return &apperr.Error{Code: apperr.CommandFailed, Message: "simctl did not save a video", Argv: argv, Stderr: res.Stderr}
	}
	return nil
}

var (
	locationAction  = action{verb: "set the location", hint: "Boot it first."}
	statusBarAction = action{verb: "change the status bar", hint: "Boot it first."}
)

func formatCoordinate(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// SetLocation puts a booted simulator at a place, which its apps see until ClearLocation.
func (c *Client) SetLocation(ctx context.Context, d device.Device, lat, lon float64) error {
	if err := onlySimulator(d, "set the location"); err != nil {
		return err
	}
	argv := []string{"xcrun", "simctl", "location", d.ID, "set", formatCoordinate(lat) + "," + formatCoordinate(lon)}
	return actionError(c.Runner.Run(ctx, argv, run.Options{Timeout: actionTimeout}), argv, locationAction)
}

// ClearLocation takes the fake location off a booted simulator.
func (c *Client) ClearLocation(ctx context.Context, d device.Device) error {
	if err := onlySimulator(d, "clear the location"); err != nil {
		return err
	}
	argv := []string{"xcrun", "simctl", "location", d.ID, "clear"}
	return actionError(c.Runner.Run(ctx, argv, run.Options{Timeout: actionTimeout}), argv, locationAction)
}

// CleanStatusBar gives a booted simulator the tidy status bar of a screenshot: 9:41, a full
// battery, full signal, and no carrier name.
func (c *Client) CleanStatusBar(ctx context.Context, d device.Device) error {
	if err := onlySimulator(d, "change the status bar"); err != nil {
		return err
	}
	argv := []string{"xcrun", "simctl", "status_bar", d.ID, "override", "--time", "9:41",
		"--batteryState", "charged", "--batteryLevel", "100", "--wifiBars", "3",
		"--cellularBars", "4", "--cellularMode", "active", "--operatorName", ""}
	return actionError(c.Runner.Run(ctx, argv, run.Options{Timeout: actionTimeout}), argv, statusBarAction)
}

// ResetStatusBar takes the overrides off a booted simulator's status bar.
func (c *Client) ResetStatusBar(ctx context.Context, d device.Device) error {
	if err := onlySimulator(d, "change the status bar"); err != nil {
		return err
	}
	argv := []string{"xcrun", "simctl", "status_bar", d.ID, "clear"}
	return actionError(c.Runner.Run(ctx, argv, run.Options{Timeout: actionTimeout}), argv, statusBarAction)
}

// Screenshot saves a PNG of a booted simulator's screen to path.
func (c *Client) Screenshot(ctx context.Context, d device.Device, path string) error {
	if err := onlySimulator(d, "take a screenshot"); err != nil {
		return err
	}
	argv := []string{"xcrun", "simctl", "io", d.ID, "screenshot", "--type=png", path}
	return actionError(c.Runner.Run(ctx, argv, run.Options{Timeout: actionTimeout}), argv, screenshotAction)
}

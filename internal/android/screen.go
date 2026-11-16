package android

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/run"
)

// MaxRecording is the longest screenrecord films at a time.
const MaxRecording = 180 * time.Second

// Record films a running emulator's or phone's screen into path until ctx ends or limit passes (no
// limit when it is zero, which means MaxRecording). screenrecord writes on the device, so the
// video is pulled and the copy there removed. It is stopped with an interrupt, which is how it
// finishes the file.
func (c *Client) Record(ctx context.Context, d device.Device, path string, limit time.Duration) error {
	adb, err := c.runningDevice(d)
	if err != nil {
		return err
	}
	if limit > MaxRecording {
		return apperr.New(apperr.InvalidState, "Android records at most %d seconds at a time", int(MaxRecording/time.Second)).
			WithHint("Record again for the rest.")
	}
	seconds := int(MaxRecording / time.Second)
	if limit > 0 {
		seconds = int((limit + time.Second - 1) / time.Second)
	}
	remote := fmt.Sprintf("/sdcard/lazymobile-%d.mp4", time.Now().UnixNano())
	serial := d.TargetID()
	// Whatever happens, the copy on the device goes.
	defer c.run(context.WithoutCancel(ctx), []string{adb, "-s", serial, "shell", "rm", "-f", remote}, actionTimeout)

	// The recording outlives ctx, which only asks it to stop, so it is not run with it.
	started := []string{adb, "-s", serial, "shell", "screenrecord", "--time-limit", strconv.Itoa(seconds), remote}
	finished := make(chan run.Result, 1)
	go func() {
		finished <- c.run(context.WithoutCancel(ctx), started, time.Duration(seconds)*time.Second+30*time.Second)
	}()
	var res run.Result
	select {
	case res = <-finished:
	case <-ctx.Done():
		// The interrupt is sent again until the recording ends, in case it came before it began.
		stop := []string{adb, "-s", serial, "shell", "pkill", "-2", "screenrecord"}
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
	stopping:
		for {
			c.run(context.WithoutCancel(ctx), stop, actionTimeout)
			select {
			case res = <-finished:
				break stopping
			case <-ticker.C:
			}
		}
	}
	if err := apperr.FromResult(res, started, installHint["adb"]); err != nil {
		return err
	}
	pull := []string{adb, "-s", serial, "pull", remote, path}
	return apperr.FromResult(c.run(context.WithoutCancel(ctx), pull, 2*time.Minute), pull, installHint["adb"])
}

var nightMode = regexp.MustCompile(`Night mode:\s*(\w+)`)

// Appearance is "dark", "light", or "auto" when the device follows the time of day. It needs
// Android 10 or newer.
func (c *Client) Appearance(ctx context.Context, d device.Device) (string, error) {
	adb, err := c.runningDevice(d)
	if err != nil {
		return "", err
	}
	argv := []string{adb, "-s", d.TargetID(), "shell", "cmd", "uimode", "night"}
	res := c.run(ctx, argv, actionTimeout)
	if err := apperr.FromResult(res, argv, installHint["adb"]); err != nil {
		return "", err
	}
	m := nightMode.FindStringSubmatch(res.Stdout)
	if m == nil {
		return "", &apperr.Error{Code: apperr.ParseFailed, Message: "The device did not say its night mode", Hint: "Dark mode needs Android 10 or newer.", Argv: argv, Stderr: res.Stdout}
	}
	switch m[1] {
	case "yes":
		return "dark", nil
	case "no":
		return "light", nil
	}
	return "auto", nil
}

// SetAppearance switches a running emulator or phone to "dark" or "light".
func (c *Client) SetAppearance(ctx context.Context, d device.Device, mode string) error {
	adb, err := c.runningDevice(d)
	if err != nil {
		return err
	}
	night := "no"
	if mode == "dark" {
		night = "yes"
	}
	argv := []string{adb, "-s", d.TargetID(), "shell", "cmd", "uimode", "night", night}
	res := c.run(ctx, argv, actionTimeout)
	if err := apperr.FromResult(res, argv, installHint["adb"]); err != nil {
		return err
	}
	// Android before 10 has no such command, and adb still exits 0.
	if !nightMode.MatchString(res.Stdout) {
		return &apperr.Error{Code: apperr.CommandFailed, Message: "The device did not change its night mode: " + strings.TrimSpace(res.Stdout+res.Stderr),
			Hint: "Dark mode needs Android 10 or newer.", Argv: argv}
	}
	return nil
}

var pngSignature = "\x89PNG\r\n\x1a\n"

// Screenshot returns a PNG of a running emulator's or phone's screen.
func (c *Client) Screenshot(ctx context.Context, d device.Device) ([]byte, error) {
	adb, err := c.runningDevice(d)
	if err != nil {
		return nil, err
	}
	// exec-out gives the bytes as they are. `shell` would turn the PNG's newlines into CR LF.
	argv := []string{adb, "-s", d.TargetID(), "exec-out", "screencap", "-p"}
	res := c.run(ctx, argv, actionTimeout)
	if err := apperr.FromResult(res, argv, installHint["adb"]); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(res.Stdout, pngSignature) {
		return nil, &apperr.Error{Code: apperr.ParseFailed, Message: "The device did not send a screenshot", Argv: argv, Stderr: res.Stderr}
	}
	return []byte(res.Stdout), nil
}

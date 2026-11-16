package android

import (
	"context"
	"regexp"
	"strings"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
)

func badAddress(verb, address string) error {
	return apperr.New(apperr.InvalidState, "Cannot %s %s: expected host:port", verb, address).
		WithHint("On the phone: Settings > Developer options > Wireless debugging shows the address and port.")
}

var pairingCode = regexp.MustCompile(`^\d{6}$`)

// Pair pairs with a phone using the code from "Pair device with pairing code" (Android 11+).
// The pairing address differs from the connect address.
func (c *Client) Pair(ctx context.Context, address, code string) error {
	if !ValidAddress(address) {
		return badAddress("pair with", address)
	}
	if !pairingCode.MatchString(code) {
		return apperr.New(apperr.InvalidState, "The pairing code is six digits")
	}
	adb, err := c.Opts.tool("adb")
	if err != nil {
		return err
	}
	argv := []string{adb, "pair", address, code}
	res := c.run(ctx, argv, actionTimeout)
	if err := apperr.FromResult(res, argv, installHint["adb"]); err != nil {
		return err
	}
	if !strings.Contains(res.Stdout, "Successfully paired") {
		return &apperr.Error{
			Code:    apperr.CommandFailed,
			Message: "Pairing failed: " + strings.TrimSpace(res.Stdout+res.Stderr),
			Hint:    "Check the code and address, and keep the pairing dialog open on the phone.",
			Argv:    argv,
		}
	}
	return nil
}

// Connect connects to a phone over Wi-Fi. adb exits 0 even when this fails, so the output
// decides.
func (c *Client) Connect(ctx context.Context, address string) error {
	if !ValidAddress(address) {
		return badAddress("connect to", address)
	}
	adb, err := c.Opts.tool("adb")
	if err != nil {
		return err
	}
	argv := []string{adb, "connect", address}
	res := c.run(ctx, argv, actionTimeout)
	if err := apperr.FromResult(res, argv, installHint["adb"]); err != nil {
		return err
	}
	out := res.Stdout + res.Stderr
	if strings.Contains(out, "connected to") && !strings.Contains(out, "cannot") && !strings.Contains(out, "failed") {
		return nil
	}
	return &apperr.Error{
		Code:    apperr.CommandFailed,
		Message: "Could not connect: " + strings.TrimSpace(out),
		Hint:    "Pair first if this phone was never paired, and make sure both are on the same network.",
		Argv:    argv,
	}
}

// Disconnect disconnects a Wi-Fi device.
func (c *Client) Disconnect(ctx context.Context, d device.Device) error {
	if d.Platform != device.Android || d.Transport != "wifi" {
		return apperr.New(apperr.Unsupported, "Only Android devices connected over Wi-Fi can be disconnected")
	}
	adb, err := c.Opts.tool("adb")
	if err != nil {
		return err
	}
	argv := []string{adb, "disconnect", d.TargetID()}
	return apperr.FromResult(c.run(ctx, argv, actionTimeout), argv, installHint["adb"])
}

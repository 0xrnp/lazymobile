package android

import (
	"context"
	"strconv"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
)

// Android key codes, from android.view.KeyEvent.
const (
	keyMenu = "82" // KEYCODE_MENU opens a React Native app's developer menu
	keyR    = "46" // KEYCODE_R, pressed twice, reloads it
)

// ReversePort makes the device's own localhost:port reach the same port on this computer, which
// is how a phone or emulator finds a dev server such as Metro.
func (c *Client) ReversePort(ctx context.Context, d device.Device, port int) error {
	adb, err := c.runningDevice(d)
	if err != nil {
		return err
	}
	p := strconv.Itoa(port)
	argv := []string{adb, "-s", d.TargetID(), "reverse", "tcp:" + p, "tcp:" + p}
	return apperr.FromResult(c.run(ctx, argv, actionTimeout), argv, installHint["adb"])
}

// OpenDevMenu sends the menu key, which a React Native app in development answers with its
// developer menu.
func (c *Client) OpenDevMenu(ctx context.Context, d device.Device) error {
	return c.keys(ctx, d, keyMenu)
}

// Reload presses R twice, which a React Native app in development answers by reloading.
func (c *Client) Reload(ctx context.Context, d device.Device) error {
	return c.keys(ctx, d, keyR, keyR)
}

func (c *Client) keys(ctx context.Context, d device.Device, codes ...string) error {
	adb, err := c.runningDevice(d)
	if err != nil {
		return err
	}
	argv := append([]string{adb, "-s", d.TargetID(), "shell", "input", "keyevent"}, codes...)
	return apperr.FromResult(c.run(ctx, argv, actionTimeout), argv, installHint["adb"])
}

package ios

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/run"
)

// These are what Xcode's devicectl does on a real iPhone. Each change can be undone with the
// same command, and the caller says how: it is a person's own phone.
var (
	phoneAppearanceAction = action{verb: "change the appearance", hint: "Unlock it and keep it connected.",
		unsupported: "This iPhone's iOS does not offer it to Xcode."}
	phoneLocationAction = action{verb: "set the location", hint: "Unlock it and keep it connected.",
		unsupported: "This iPhone's iOS does not offer a simulated location to Xcode."}
	phoneStatusBarAction = action{verb: "change the status bar", hint: "Unlock it and keep it connected.",
		unsupported: "This iPhone's iOS does not offer a status bar override to Xcode."}
	phoneLinkAction = action{verb: "open a link", hint: "Unlock it and keep it connected."}
)

// devicectlDo runs a devicectl command that changes a phone.
func (c *Client) devicectlDo(ctx context.Context, a action, args ...string) error {
	argv := append([]string{"xcrun", "devicectl"}, args...)
	argv = append(argv, "--quiet")
	return phoneActionError(c.Runner.Run(ctx, argv, run.Options{Timeout: actionTimeout}), argv, a)
}

// phoneAppearance is "light" or "dark", the mode of an iPhone.
func (c *Client) phoneAppearance(ctx context.Context, d device.Device) (string, error) {
	content, err := c.devicectl(ctx, "device", "info", "appearance", "--device", d.ID)
	if err != nil {
		return "", err
	}
	var out struct {
		Result struct {
			Style string `json:"userInterfaceStyle"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(content), &out); err != nil {
		return "", apperr.New(apperr.ParseFailed, "Could not read the appearance of %s", d.Name)
	}
	if style := strings.ToLower(out.Result.Style); style == "light" || style == "dark" {
		return style, nil
	}
	return "", apperr.New(apperr.ParseFailed, "devicectl did not say the appearance of %s", d.Name)
}

func (c *Client) phoneSetAppearance(ctx context.Context, d device.Device, mode string) error {
	return c.devicectlDo(ctx, phoneAppearanceAction, "device", "settings", "appearance", "--device", d.ID, "--mode", mode)
}

func (c *Client) phoneSetLocation(ctx context.Context, d device.Device, lat, lon float64) error {
	return c.devicectlDo(ctx, phoneLocationAction, "device", "simulate", "location", "coordinate", "--device", d.ID,
		// The = form, since a negative number after a flag is read as another flag.
		"--latitude="+formatCoordinate(lat), "--longitude="+formatCoordinate(lon))
}

func (c *Client) phoneClearLocation(ctx context.Context, d device.Device) error {
	return c.devicectlDo(ctx, phoneLocationAction, "device", "simulate", "location", "clear", "--device", d.ID)
}

func (c *Client) phoneCleanStatusBar(ctx context.Context, d device.Device) error {
	return c.devicectlDo(ctx, phoneStatusBarAction, "device", "simulate", "statusBar", "preset", "screenshot", "--device", d.ID)
}

func (c *Client) phoneResetStatusBar(ctx context.Context, d device.Device) error {
	return c.devicectlDo(ctx, phoneStatusBarAction, "device", "simulate", "statusBar", "clear", "--device", d.ID)
}

// Safari is the app that opens a web address on an iPhone.
const Safari = "com.apple.mobilesafari"

// PhoneOpenURL opens link in the app bundleID on an iPhone, which launches it. A phone has no
// command to ask which app handles a link, so the app is named, and a web address uses Safari.
func (c *Client) PhoneOpenURL(ctx context.Context, d device.Device, link, bundleID string) error {
	return c.devicectlDo(ctx, phoneLinkAction, "device", "process", "launch", "--device", d.ID, "--payload-url", link, bundleID)
}

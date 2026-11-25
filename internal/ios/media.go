package ios

import (
	"context"
	"strings"

	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/run"
)

var (
	mediaAction     = action{verb: "add photos or videos", hint: "Boot it first."}
	clipboardAction = action{verb: "use the clipboard", hint: "Boot it first."}
)

// AddMedia adds photos, videos and contacts (.vcf) to a booted simulator's library.
func (c *Client) AddMedia(ctx context.Context, d device.Device, paths []string) error {
	if err := onlySimulator(d, "add photos or videos"); err != nil {
		return err
	}
	argv := append([]string{"xcrun", "simctl", "addmedia", d.ID}, paths...)
	return actionError(c.Runner.Run(ctx, argv, run.Options{Timeout: 5 * actionTimeout}), argv, mediaAction)
}

// SetClipboard puts text on a booted simulator's clipboard, ready to paste into a field.
func (c *Client) SetClipboard(ctx context.Context, d device.Device, text string) error {
	if err := onlySimulator(d, "set the clipboard"); err != nil {
		return err
	}
	argv := []string{"xcrun", "simctl", "pbcopy", d.ID}
	return actionError(c.Runner.Run(ctx, argv, run.Options{Timeout: actionTimeout, Stdin: text}), argv, clipboardAction)
}

// Clipboard reads the text on a booted simulator's clipboard.
func (c *Client) Clipboard(ctx context.Context, d device.Device) (string, error) {
	if err := onlySimulator(d, "read the clipboard"); err != nil {
		return "", err
	}
	argv := []string{"xcrun", "simctl", "pbpaste", d.ID}
	res := c.Runner.Run(ctx, argv, run.Options{Timeout: actionTimeout})
	if err := actionError(res, argv, clipboardAction); err != nil {
		return "", err
	}
	return strings.TrimRight(res.Stdout, "\n"), nil
}

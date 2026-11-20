package ios

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/run"
)

// maxPushPayload is the largest payload simctl takes, the same limit as Apple's push service.
const maxPushPayload = 4096

var pushAction = action{verb: "send a push notification", hint: "Boot it first."}

// AlertPayload is the payload of a notification that shows title and body, with a sound.
func AlertPayload(title, body string) []byte {
	payload := map[string]any{"aps": map[string]any{
		"alert": map[string]string{"title": title, "body": body},
		"sound": "default",
	}}
	data, _ := json.Marshal(payload) // a map of strings always marshals
	return data
}

const payloadHint = `A payload is a JSON object with an "aps" key, such as {"aps": {"alert": "Hello"}}.`

// CheckPushPayload says what is wrong with a payload simctl would refuse.
func CheckPushPayload(data []byte) error {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(data, &payload); err != nil {
		return apperr.New(apperr.Usage, "The payload is not a JSON object: %v", err).WithHint(payloadHint)
	}
	if _, ok := payload["aps"]; !ok {
		return apperr.New(apperr.Usage, `The payload has no "aps" key`).WithHint(payloadHint)
	}
	if len(data) > maxPushPayload {
		return apperr.New(apperr.Usage, "The payload is %d bytes, more than the %d a notification can carry", len(data), maxPushPayload)
	}
	return nil
}

// SendPush delivers a push notification with payload to an app on a booted simulator, as if it
// came from Apple's push service.
func (c *Client) SendPush(ctx context.Context, d device.Device, id string, payload []byte) error {
	if err := onlySimulator(d, "send a push notification"); err != nil {
		return err
	}
	if err := CheckPushPayload(payload); err != nil {
		return err
	}
	argv := []string{"xcrun", "simctl", "push", d.ID, id, "-"}
	res := c.Runner.Run(ctx, argv, run.Options{Timeout: actionTimeout, Stdin: string(payload)})
	// UNErrorDomain 2003, "Source is not authorized", is the same for an app that is not installed
	// and for one that was never allowed notifications.
	if strings.Contains(res.Stderr, "UNErrorDomain, code=2003") {
		return &apperr.Error{
			Code:    apperr.InvalidState,
			Message: fmt.Sprintf("%s on %s cannot receive notifications", id, d.Name),
			Hint:    "Check that it is installed (lazymobile apps), and that it asked for notifications and was allowed. Settings > Notifications on the simulator shows it once it has asked.",
			Argv:    argv, ExitCode: res.Code, Stderr: res.Stderr,
		}
	}
	return actionError(res, argv, pushAction)
}

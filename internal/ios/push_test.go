package ios

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/run"
	"github.com/0xrnp/lazymobile/internal/run/runtest"
)

// simctlPushUnauthorized is what simctl 27.2 printed for an app that was not installed, and the
// same for an installed app that never asked for notifications.
const simctlPushUnauthorized = `An error was encountered processing the command (domain=UNErrorDomain, code=2003):
Simulator device failed to complete the requested operation.
Underlying error (domain=UNErrorDomain, code=2003):
	Repository could not save notification. Source is not authorized.
`

func TestSendPushGivesSimctlThePayloadOnStdin(t *testing.T) {
	var stdin string
	fake := &runtest.Fake{Answer: func(_ string, _ []string, opts run.Options) run.Result {
		stdin = opts.Stdin
		return runtest.Out("Notification sent to 'com.example.app'\n")
	}}
	payload := AlertPayload("Order", `Your "tea" is ready`)
	if err := client(fake).SendPush(context.Background(), bootedSim, "com.example.app", payload); err != nil {
		t.Fatal(err)
	}
	if !fake.Called("xcrun simctl push " + udidBooted + " com.example.app -") {
		t.Errorf("ran %v", fake.Calls())
	}
	var got struct {
		APS struct {
			Alert struct{ Title, Body string } `json:"alert"`
			Sound string                       `json:"sound"`
		} `json:"aps"`
	}
	if err := json.Unmarshal([]byte(stdin), &got); err != nil || got.APS.Alert.Title != "Order" || got.APS.Alert.Body != `Your "tea" is ready` || got.APS.Sound != "default" {
		t.Errorf("stdin %q: %+v, %v", stdin, got, err)
	}
}

func TestSendPushRefusals(t *testing.T) {
	unauthorized := runtest.Table(map[string]run.Result{"simctl push": runtest.Fail(211, simctlPushUnauthorized)})
	err := client(unauthorized).SendPush(context.Background(), bootedSim, "com.example.app", AlertPayload("a", "b"))
	if apperr.CodeOf(err) != apperr.InvalidState || !strings.Contains(err.Error(), "com.example.app on iPhone 16 cannot receive notifications") {
		t.Errorf("not allowed notifications: %v", err)
	}
	for name, payload := range map[string]string{
		"not JSON":  `{"aps":`,
		"no aps":    `{"alert": "x"}`,
		"too large": `{"aps": {"alert": "` + strings.Repeat("x", 4100) + `"}}`,
	} {
		fake := runtest.Table(map[string]run.Result{})
		if err := client(fake).SendPush(context.Background(), bootedSim, "com.example.app", []byte(payload)); apperr.CodeOf(err) != apperr.Usage || len(fake.Calls()) != 0 {
			t.Errorf("%s: %v, calls %v", name, err, fake.Calls())
		}
	}
	fake := runtest.Table(map[string]run.Result{})
	if err := client(fake).SendPush(context.Background(), iphone, "com.example.app", AlertPayload("a", "b")); apperr.CodeOf(err) != apperr.Unsupported || len(fake.Calls()) != 0 {
		t.Errorf("an iPhone: %v, calls %v", err, fake.Calls())
	}
}

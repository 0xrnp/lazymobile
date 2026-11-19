// Package update finds the newest release of lazymobile, for the one-line note in the dashboard. It
// is the only part of the program that asks the network for anything.
//
// It sends a plain GET to GitHub's public release list for this repository, with the user agent
// "lazymobile". Nothing about the user, their devices or their projects is in the request, though
// GitHub sees the address it came from, as with any request. It asks at most once a day, and
// LAZYMOBILE_NO_UPDATE_CHECK, set to anything, turns it off.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/0xrnp/lazymobile/internal/state"
)

const (
	// URL is where the newest release is read.
	URL = "https://api.github.com/repos/0xrnp/lazymobile/releases/latest"
	// EnvOff turns the check off when it is set to anything.
	EnvOff = "LAZYMOBILE_NO_UPDATE_CHECK"
	// Every is how long a check is remembered, and so how often GitHub is asked.
	Every   = 24 * time.Hour
	timeout = 5 * time.Second
	// maxBody keeps a wrong answer from filling memory.
	maxBody = 1 << 20
)

// Options are what a check needs. Zero values mean the real ones.
type Options struct {
	// Store remembers the last check, so GitHub is asked once a day. A check without one is not
	// made, since it could not keep to that.
	Store  state.Store
	Client *http.Client
	Getenv func(string) string
	Now    func() time.Time
	URL    string
}

// Latest is the newest release, such as "0.5.0", or "" when the check is off. The answer of the last
// check is used for a day. A check that fails is not asked again before then either, so a
// computer without a network, or a limit at GitHub, is not tried at every start.
func Latest(ctx context.Context, o Options) (string, error) {
	if o.Getenv != nil && o.Getenv(EnvOff) != "" || o.Store.Path == "" {
		return "", nil
	}
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	last := o.Store.Load().UpdateCheck
	if !last.At.After(now()) && now().Sub(last.At) < Every {
		return last.Latest, nil
	}
	latest, err := fetch(ctx, o)
	remember := last.Latest
	if err == nil {
		remember = latest
	}
	_ = o.Store.Update(func(f *state.File) { f.UpdateCheck = state.UpdateCheck{At: now(), Latest: remember} })
	return latest, err
}

func fetch(ctx context.Context, o Options) (string, error) {
	client := o.Client
	if client == nil {
		client = http.DefaultClient
	}
	url := o.URL
	if url == "" {
		url = URL
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "lazymobile")
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the release list answered %s", resp.Status)
	}
	var release struct {
		Tag string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&release); err != nil {
		return "", fmt.Errorf("the release list could not be read: %w", err)
	}
	tag := strings.TrimPrefix(strings.TrimSpace(release.Tag), "v")
	if tag == "" {
		return "", fmt.Errorf("the release list has no version")
	}
	return tag, nil
}

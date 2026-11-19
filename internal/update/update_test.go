package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/0xrnp/lazymobile/internal/state"
)

// release answers like GitHub does, and counts how often it was asked.
func release(t *testing.T, status int, body string) (*httptest.Server, *atomic.Int32, *atomic.Value) {
	t.Helper()
	var asked atomic.Int32
	var agent atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		agent.Store(r.Header.Get("User-Agent"))
		if r.URL.RawQuery != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Method != http.MethodGet {
			t.Errorf("the request carries more than it should: %s %s %v", r.Method, r.URL, r.Header)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server, &asked, &agent
}

func options(t *testing.T, server *httptest.Server) Options {
	t.Helper()
	return Options{
		Store: state.Store{Path: filepath.Join(t.TempDir(), "state.json")}, URL: server.URL,
		Getenv: func(string) string { return "" },
	}
}

func TestLatestReadsTheNewestReleaseAndSendsNothingAboutTheUser(t *testing.T) {
	server, asked, agent := release(t, 200, `{"tag_name": "v0.5.0", "name": "x"}`)
	got, err := Latest(context.Background(), options(t, server))
	if err != nil || got != "0.5.0" || asked.Load() != 1 {
		t.Fatalf("got %q, %v after %d requests", got, err, asked.Load())
	}
	if agent.Load() != "lazymobile" {
		t.Errorf("the user agent is %q", agent.Load())
	}
}

func TestLatestAsksOnceADay(t *testing.T) {
	server, asked, _ := release(t, 200, `{"tag_name": "v0.5.0"}`)
	o := options(t, server)
	now := time.Unix(1_800_000_000, 0)
	o.Now = func() time.Time { return now }
	for range 3 {
		if got, err := Latest(context.Background(), o); err != nil || got != "0.5.0" {
			t.Fatalf("got %q, %v", got, err)
		}
		now = now.Add(time.Hour)
	}
	if asked.Load() != 1 {
		t.Errorf("GitHub was asked %d times in a day", asked.Load())
	}
	now = now.Add(Every)
	if _, err := Latest(context.Background(), o); err != nil || asked.Load() != 2 {
		t.Errorf("a day later: %v, %d requests", err, asked.Load())
	}
}

func TestLatestIsOffWhenAskedAndWhenThereIsNoPlaceToRemember(t *testing.T) {
	server, asked, _ := release(t, 200, `{"tag_name": "v0.5.0"}`)
	off := options(t, server)
	off.Getenv = func(name string) string {
		if name == EnvOff {
			return "1"
		}
		return ""
	}
	forgetful := options(t, server)
	forgetful.Store = state.Store{}
	for name, o := range map[string]Options{"switched off": off, "no state file": forgetful} {
		if got, err := Latest(context.Background(), o); got != "" || err != nil {
			t.Errorf("%s: got %q, %v", name, got, err)
		}
	}
	if asked.Load() != 0 {
		t.Errorf("GitHub was asked %d times", asked.Load())
	}
}

func TestAFailedCheckIsNotRepeatedAtEveryStart(t *testing.T) {
	for name, body := range map[string]struct {
		status int
		body   string
	}{
		"not found":    {404, `{"message": "Not Found"}`},
		"rate limited": {403, `{"message": "rate limit"}`},
		"not JSON":     {200, `<html>`},
		"no version":   {200, `{}`},
		"too long":     {200, `{"tag_name": "` + strings.Repeat("9", 2<<20) + `"}`},
	} {
		server, asked, _ := release(t, body.status, body.body)
		o := options(t, server)
		if got, err := Latest(context.Background(), o); err == nil || got != "" {
			t.Errorf("%s: got %q, %v", name, got, err)
		}
		if got, err := Latest(context.Background(), o); err != nil || got != "" || asked.Load() != 1 {
			t.Errorf("%s: the failure was not remembered: %q, %v, %d requests", name, got, err, asked.Load())
		}
	}
}

func TestAFailedCheckKeepsWhatWasKnown(t *testing.T) {
	good, _, _ := release(t, 200, `{"tag_name": "v0.5.0"}`)
	o := options(t, good)
	now := time.Unix(1_800_000_000, 0)
	o.Now = func() time.Time { return now }
	if _, err := Latest(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	bad, _, _ := release(t, 500, "")
	o.URL = bad.URL
	now = now.Add(2 * Every)
	if _, err := Latest(context.Background(), o); err == nil {
		t.Fatal("a failure was not reported")
	}
	if got, err := Latest(context.Background(), o); err != nil || got != "0.5.0" {
		t.Errorf("what was known was lost: %q, %v", got, err)
	}
}

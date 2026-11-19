package mobile

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/run"
	"github.com/0xrnp/lazymobile/internal/run/runtest"
)

// clipboardCall is a command the clipboard ran, and what it was given on standard input.
type clipboardCall struct {
	argv  []string
	stdin string
}

func clipboardManager(goos string, env map[string]string) (*Manager, *[]clipboardCall) {
	var calls []clipboardCall
	fake := &runtest.Fake{Answer: func(_ string, argv []string, opts run.Options) run.Result {
		calls = append(calls, clipboardCall{argv, opts.Stdin})
		return run.Result{}
	}}
	m := &Manager{Runner: fake, GOOS: goos, Getenv: func(key string) string { return env[key] }}
	return m, &calls
}

func TestCopyOnMacOS(t *testing.T) {
	m, calls := clipboardManager("darwin", nil)
	if err := m.CopyText(context.Background(), "the log"); err != nil {
		t.Fatal(err)
	}
	if err := m.CopyFile(context.Background(), "/work/Shot.PNG"); err != nil {
		t.Fatal(err)
	}
	if err := m.CopyFile(context.Background(), "/work/clip it.mp4"); err != nil {
		t.Fatal(err)
	}
	got := *calls
	if len(got) != 3 {
		t.Fatalf("%d commands", len(got))
	}
	if got[0].argv[0] != "pbcopy" || got[0].stdin != "the log" {
		t.Errorf("text: %q with %q", got[0].argv, got[0].stdin)
	}
	// A picture is copied as one, and the path is an argument, never part of the script.
	picture := strings.Join(got[1].argv, " ")
	if got[1].argv[0] != "osascript" || !strings.Contains(picture, "«class PNGf»") || got[1].argv[len(got[1].argv)-1] != "/work/Shot.PNG" {
		t.Errorf("picture: %q", got[1].argv)
	}
	video := got[2].argv
	if strings.Contains(strings.Join(video, " "), "PNGf") || video[len(video)-1] != "/work/clip it.mp4" {
		t.Errorf("video: %q", video)
	}
}

// linuxClipboard runs the real Linux clipboard script with fake wl-copy and xclip programs on PATH.
// Each one writes what it was given, its arguments and what it read, to a file named after it. It
// stays in the background as the real ones do, which must not keep the command from ending.
type linuxClipboard struct {
	t   *testing.T
	dir string
	m   *Manager
}

func newLinuxClipboard(t *testing.T, tools []string, env map[string]string) *linuxClipboard {
	t.Helper()
	dir := t.TempDir()
	for _, tool := range tools {
		script := "#!/bin/sh\n{ echo \"args: $*\"; cat; } > \"" + dir + "/" + tool + ".out\"\n(sleep 4 &)\n"
		if err := os.WriteFile(filepath.Join(dir, tool), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+":/usr/bin:/bin")
	return &linuxClipboard{t: t, dir: dir, m: &Manager{Runner: run.Exec{}, GOOS: "linux", Getenv: func(key string) string { return env[key] }}}
}

// got is what the tool was given: its arguments, then what it read.
func (c *linuxClipboard) got(tool string) string {
	data, err := os.ReadFile(filepath.Join(c.dir, tool+".out"))
	if err != nil {
		return ""
	}
	return string(data)
}

func TestLinuxClipboardCopiesTextAPictureAndAFileWithAHostileName(t *testing.T) {
	c := newLinuxClipboard(t, []string{"wl-copy"}, map[string]string{"WAYLAND_DISPLAY": "wayland-0"})
	began := time.Now()
	if err := c.m.CopyText(context.Background(), "the log\nsecond line"); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(began); took > 2*time.Second {
		t.Errorf("copying took %v: the tool in the background held the command", took)
	}
	if got := c.got("wl-copy"); got != "args: \nthe log\nsecond line" {
		t.Errorf("text: %q", got)
	}

	picture := filepath.Join(t.TempDir(), "My $(touch pwned) `x` 'shot'.png")
	if err := os.WriteFile(picture, []byte("PNGDATA"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := c.m.CopyFile(context.Background(), picture); err != nil {
		t.Fatal(err)
	}
	if got := c.got("wl-copy"); got != "args: --type image/png\nPNGDATA" {
		t.Errorf("picture: %q", got)
	}
	if _, err := os.Stat("pwned"); err == nil {
		t.Error("a command in a file name ran")
	}

	// Another file is copied as its address, with what a URI cannot hold escaped.
	video := filepath.Join(t.TempDir(), "my clip #1.mp4")
	if err := c.m.CopyFile(context.Background(), video); err != nil {
		t.Fatal(err)
	}
	want := "args: --type text/uri-list\nfile://" + strings.ReplaceAll(strings.ReplaceAll(video, " ", "%20"), "#", "%23") + "\n"
	if got := c.got("wl-copy"); got != want {
		t.Errorf("video: %q, want %q", got, want)
	}
}

func TestLinuxClipboardFallsBackToXclipWhenWlCopyIsMissing(t *testing.T) {
	// Xwayland: both are set, but only xclip is installed.
	c := newLinuxClipboard(t, []string{"xclip"}, map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"})
	if err := c.m.CopyText(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	if got := c.got("xclip"); got != "args: -selection clipboard\nhello" {
		t.Errorf("text: %q", got)
	}
	picture := filepath.Join(t.TempDir(), "shot.png")
	_ = os.WriteFile(picture, []byte("PNGDATA"), 0o600)
	if err := c.m.CopyFile(context.Background(), picture); err != nil {
		t.Fatal(err)
	}
	if got := c.got("xclip"); got != "args: -selection clipboard -t image/png\nPNGDATA" {
		t.Errorf("picture: %q", got)
	}
}

func TestLinuxClipboardSaysWhichToolIsMissingAndWhyAFileFailed(t *testing.T) {
	c := newLinuxClipboard(t, nil, map[string]string{"DISPLAY": ":0"})
	err := c.m.CopyText(context.Background(), "hello")
	if err == nil || !strings.Contains(err.Error(), "wl-copy or xclip was not found") {
		t.Errorf("a missing tool: %v", err)
	}
	c = newLinuxClipboard(t, []string{"xclip"}, map[string]string{"DISPLAY": ":0"})
	err = c.m.CopyFile(context.Background(), filepath.Join(t.TempDir(), "gone.png"))
	if err == nil || !strings.Contains(err.Error(), "cannot read") {
		t.Errorf("a missing file: %v", err)
	}
}

func TestCopyWithoutADesktopSaysSo(t *testing.T) {
	m, calls := clipboardManager("linux", nil)
	err := m.CopyText(context.Background(), "the log")
	if apperr.CodeOf(err) != apperr.Unsupported || len(*calls) != 0 {
		t.Errorf("err %v after %d commands", err, len(*calls))
	}
}

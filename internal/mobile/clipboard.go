package mobile

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/run"
)

const clipboardTimeout = 10 * time.Second

// Each AppleScript gets the path as an argument, so a path is never read as part of the script.
var (
	copyPicture = []string{"osascript",
		"-e", "on run argv",
		"-e", "set the clipboard to (read (POSIX file (item 1 of argv)) as «class PNGf»)",
		"-e", "end run"}
	copyFileRef = []string{"osascript",
		"-e", "on run argv",
		"-e", "set the clipboard to (POSIX file (item 1 of argv))",
		"-e", "end run"}
)

// CopyText puts text on this computer's clipboard.
func (m *Manager) CopyText(ctx context.Context, text string) error {
	if m.GOOS == "darwin" {
		return m.runClipboard(ctx, []string{"pbcopy"}, text)
	}
	argv, err := m.linuxClipboard("", "-")
	if err != nil {
		return err
	}
	return m.runClipboard(ctx, argv, text)
}

// CopyFile puts a file on this computer's clipboard: a PNG as a picture, to paste into a chat, and
// any other file as the file itself, to attach.
func (m *Manager) CopyFile(ctx context.Context, path string) error {
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	picture := strings.EqualFold(filepath.Ext(path), ".png")
	if m.GOOS == "darwin" {
		argv := copyFileRef
		if picture {
			argv = copyPicture
		}
		return m.runClipboard(ctx, append(append([]string(nil), argv...), path), "")
	}
	if picture {
		argv, err := m.linuxClipboard("image/png", path)
		if err != nil {
			return err
		}
		return m.runClipboard(ctx, argv, "")
	}
	argv, err := m.linuxClipboard("text/uri-list", "-")
	if err != nil {
		return err
	}
	// A file is copied as its address, which needs spaces and the like escaped.
	uri := (&url.URL{Scheme: "file", Path: path}).String()
	return m.runClipboard(ctx, argv, uri+"\n")
}

// linuxClipboardScript copies standard input, or the file named by $2 when it is not "-", as the
// type $1 (empty for plain text), with wl-copy when $3 is 1 and it is installed, else with xclip
// when $4 is 1 and it is installed. Both stay in the background to hold what was copied, so their
// output goes to /dev/null, or they would keep the command from ending. Errors in reading the file
// still show.
const linuxClipboardScript = `mime=$1 input=$2
if [ "$input" != - ] && [ ! -r "$input" ]; then echo "cannot read $input" >&2; exit 1; fi
if [ "$3" = 1 ] && command -v wl-copy >/dev/null 2>&1; then
  t=; [ -n "$mime" ] && t="--type $mime"
  if [ "$input" = - ]; then wl-copy $t >/dev/null 2>&1; else wl-copy $t < "$input" >/dev/null 2>&1; fi
  exit $?
fi
if [ "$4" = 1 ] && command -v xclip >/dev/null 2>&1; then
  t=; [ -n "$mime" ] && t="-t $mime"
  if [ "$input" = - ]; then xclip -selection clipboard $t >/dev/null 2>&1; else xclip -selection clipboard $t < "$input" >/dev/null 2>&1; fi
  exit $?
fi
echo "wl-copy or xclip was not found" >&2
exit 127`

// linuxClipboard is the command that copies to the clipboard on Linux, for a desktop session.
func (m *Manager) linuxClipboard(mime, input string) ([]string, error) {
	getenv := m.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	wayland, x := getenv("WAYLAND_DISPLAY") != "", getenv("DISPLAY") != ""
	if !wayland && !x {
		return nil, apperr.New(apperr.Unsupported, "There is no clipboard to copy to here").
			WithHint("lazymobile copies with wl-copy or xclip, in a desktop session.")
	}
	flag := func(on bool) string {
		if on {
			return "1"
		}
		return "0"
	}
	return []string{"sh", "-c", linuxClipboardScript, "sh", mime, input, flag(wayland), flag(x)}, nil
}

func (m *Manager) runClipboard(ctx context.Context, argv []string, stdin string) error {
	res := m.Runner.Run(ctx, argv, run.Options{Stdin: stdin, Timeout: clipboardTimeout})
	return apperr.FromResult(res, argv, "On Linux, install wl-copy (Wayland) or xclip (X11).")
}

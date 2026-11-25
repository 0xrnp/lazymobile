package android

import (
	"context"
	"encoding/xml"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
)

// maxElements is how many elements of a screen are listed, so a long list does not fill an agent's
// context.
const maxElements = 250

type uiNode struct {
	Text       string   `xml:"text,attr"`
	ID         string   `xml:"resource-id,attr"`
	Class      string   `xml:"class,attr"`
	Package    string   `xml:"package,attr"`
	Desc       string   `xml:"content-desc,attr"`
	Checked    bool     `xml:"checked,attr"`
	Clickable  bool     `xml:"clickable,attr"`
	LongClick  bool     `xml:"long-clickable,attr"`
	Enabled    bool     `xml:"enabled,attr"`
	Focused    bool     `xml:"focused,attr"`
	Scrollable bool     `xml:"scrollable,attr"`
	Password   bool     `xml:"password,attr"`
	Bounds     string   `xml:"bounds,attr"`
	Children   []uiNode `xml:"node"`
}

type uiRoot struct {
	Nodes []uiNode `xml:"node"`
}

var boundsPattern = regexp.MustCompile(`^\[(\d+),(\d+)\]\[(\d+),(\d+)\]$`)

// Screen reads what is on the screen of a running emulator: the texts, labels and ids that can be
// matched, and where to tap them. Only an emulator is read, since a phone's screen is the person's
// own.
func (c *Client) Screen(ctx context.Context, d device.Device) (device.Screen, error) {
	adb, err := c.emulator(d, "read the screen")
	if err != nil {
		return device.Screen{}, err
	}
	argv := []string{adb, "-s", d.TargetID(), "exec-out", "uiautomator", "dump", "/dev/tty"}
	res := c.run(ctx, argv, actionTimeout)
	if err := apperr.FromResult(res, argv, installHint["adb"]); err != nil {
		return device.Screen{}, err
	}
	return ParseScreen(res.Stdout)
}

// ParseScreen reads the output of uiautomator dump.
func ParseScreen(out string) (device.Screen, error) {
	start := strings.Index(out, "<?xml")
	end := strings.LastIndex(out, "</hierarchy>")
	if start < 0 || end < 0 {
		return device.Screen{}, apperr.New(apperr.CommandFailed, "The screen could not be read: uiautomator gave no hierarchy").
			WithHint("An app that is busy drawing, or a video, can stop it. Try again in a moment.")
	}
	var root uiRoot
	if err := xml.Unmarshal([]byte(out[start:end+len("</hierarchy>")]), &root); err != nil {
		return device.Screen{}, apperr.New(apperr.CommandFailed, "The screen could not be read: %v", err)
	}
	var screen device.Screen
	var walk func(n uiNode, top bool)
	walk = func(n uiNode, top bool) {
		x1, y1, x2, y2, ok := parseBounds(n.Bounds)
		if ok && top && screen.Width == 0 {
			screen.Width, screen.Height, screen.App = x2, y2, n.Package
		}
		if ok && x2 > x1 && y2 > y1 {
			text := n.Text
			if n.Password {
				text = strings.Repeat("•", len([]rune(text)))
			}
			tappable := n.Clickable || n.LongClick
			// Something is listed when it can be matched by what it says or is, or touched.
			if text != "" || n.Desc != "" || tappable || n.Scrollable || (n.ID != "" && n.Focused) {
				if len(screen.Elements) >= maxElements {
					screen.Truncated = true
				} else {
					screen.Elements = append(screen.Elements, device.Element{
						Text: text, Desc: n.Desc, ID: n.ID, Class: shortClass(n.Class),
						X: (x1 + x2) / 2, Y: (y1 + y2) / 2, Width: x2 - x1, Height: y2 - y1,
						Tappable: tappable, Scrolls: n.Scrollable, Focused: n.Focused, Checked: n.Checked, Disabled: !n.Enabled,
					})
				}
			}
		}
		for _, child := range n.Children {
			walk(child, false)
		}
	}
	for _, n := range root.Nodes {
		walk(n, true)
	}
	return screen, nil
}

func parseBounds(s string) (x1, y1, x2, y2 int, ok bool) {
	m := boundsPattern.FindStringSubmatch(s)
	if m == nil {
		return 0, 0, 0, 0, false
	}
	n := make([]int, 4)
	for i := range n {
		n[i], _ = strconv.Atoi(m[i+1])
	}
	return n[0], n[1], n[2], n[3], true
}

func shortClass(class string) string {
	if i := strings.LastIndex(class, "."); i >= 0 {
		return class[i+1:]
	}
	return class
}

// Tap touches the screen of a running emulator at x, y, in pixels.
func (c *Client) Tap(ctx context.Context, d device.Device, x, y int) error {
	return c.input(ctx, d, "touch the screen", "tap", strconv.Itoa(x), strconv.Itoa(y))
}

// Swipe drags from one point to another over ms milliseconds, which scrolls a list.
func (c *Client) Swipe(ctx context.Context, d device.Device, x1, y1, x2, y2, ms int) error {
	return c.input(ctx, d, "touch the screen", "swipe", strconv.Itoa(x1), strconv.Itoa(y1), strconv.Itoa(x2), strconv.Itoa(y2), strconv.Itoa(ms))
}

// maxTyped is the longest text typed at once.
const maxTyped = 500

// TypeText types text into the field that has the focus, as a keyboard would. Android's input
// command takes printable ASCII only.
func (c *Client) TypeText(ctx context.Context, d device.Device, text string) error {
	if text == "" || len(text) > maxTyped {
		return apperr.New(apperr.InvalidState, "Type between 1 and %d characters at a time", maxTyped)
	}
	for _, r := range text {
		if r > unicode.MaxASCII || !unicode.IsPrint(r) {
			return apperr.New(apperr.InvalidState, "Android's input command types printable ASCII only: %q is not", r).
				WithHint("Use press_key enter for a new line. Other characters can be pasted into a field some other way.")
		}
	}
	// input text turns %s into a space. The text passes through a shell on the device, so it is quoted.
	encoded := strings.ReplaceAll(text, " ", "%s")
	return c.input(ctx, d, "type", "text", shellQuote(encoded))
}

// keys are the names Key takes, and the Android key each presses. Power is left out: it turns off
// the screen the other tools read.
var keys = map[string]string{
	"back": "BACK", "home": "HOME", "enter": "ENTER", "delete": "DEL", "tab": "TAB", "escape": "ESCAPE",
	"menu": "MENU", "app_switch": "APP_SWITCH", "search": "SEARCH", "space": "SPACE",
	"volume_up": "VOLUME_UP", "volume_down": "VOLUME_DOWN", "page_up": "PAGE_UP", "page_down": "PAGE_DOWN",
	"dpad_up": "DPAD_UP", "dpad_down": "DPAD_DOWN", "dpad_left": "DPAD_LEFT", "dpad_right": "DPAD_RIGHT", "dpad_center": "DPAD_CENTER",
}

// KeyNames are the keys Key presses, sorted.
func KeyNames() []string {
	names := make([]string, 0, len(keys))
	for name := range keys {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// Key presses one key, such as back or home.
func (c *Client) Key(ctx context.Context, d device.Device, name string) error {
	code, ok := keys[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return apperr.New(apperr.InvalidState, "%q is not a key lazymobile presses", name).
			WithHint("%s", "Use one of: "+strings.Join(KeyNames(), ", ")+".")
	}
	return c.input(ctx, d, "press a key", "keyevent", "KEYCODE_"+code)
}

func (c *Client) input(ctx context.Context, d device.Device, what string, args ...string) error {
	adb, err := c.emulator(d, what)
	if err != nil {
		return err
	}
	argv := append([]string{adb, "-s", d.TargetID(), "shell", "input"}, args...)
	res := c.run(ctx, argv, actionTimeout)
	if err := apperr.FromResult(res, argv, installHint["adb"]); err != nil {
		return err
	}
	// input prints its complaint and still exits 0.
	if out := strings.TrimSpace(res.Stdout + res.Stderr); strings.Contains(out, "Exception") || strings.Contains(out, "Error:") {
		return apperr.New(apperr.CommandFailed, "Android's input command failed: %s", firstLine(out))
	}
	return nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

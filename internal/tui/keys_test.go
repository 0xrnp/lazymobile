package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/0xrnp/lazymobile/internal/eas"
)

func TestKeyTableIsSound(t *testing.T) {
	seen := map[string]bool{}
	for _, a := range keyTable {
		if a.id == "" || seen[a.id] {
			t.Errorf("action id %q is empty or used twice", a.id)
		}
		seen[a.id] = true
		if len(a.keys) == 0 || a.show == "" || a.desc == "" {
			t.Errorf("%s needs keys, a way to write them and a description", a.id)
		}
		// The widest window has room for the key column, two spaces and the description.
		if w := len([]rune(a.show)); w > keyColumn {
			t.Errorf("%s: %q is %d columns, the key column has %d", a.id, a.show, w, keyColumn)
		}
		if w := len([]rune(a.desc)); w > 59 {
			t.Errorf("%s: the description is %d columns, which does not fit the widest window", a.id, w)
		}
	}
	// Every state the hints can be in names actions that exist and have something to show.
	h := start(t, iphone)
	for _, focus := range []pane{paneDevices, paneBuilds, paneLogs} {
		h.m.focus = focus
		for _, id := range h.m.footerKeys() {
			if keyByID(id) == nil {
				t.Errorf("the hints name %q, which is not in the key table", id)
			}
		}
	}
}

// keysIn are the string cases of every switch in a function, which are the keys it handles.
func keysIn(t *testing.T, files map[string]*ast.File, funcs ...string) []string {
	t.Helper()
	var keys []string
	for _, name := range funcs {
		found := false
		for _, f := range files {
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Name.Name != name {
					continue
				}
				found = true
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					clause, ok := n.(*ast.CaseClause)
					if !ok {
						return true
					}
					for _, e := range clause.List {
						if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == token.STRING {
							key, err := strconv.Unquote(lit.Value)
							if err != nil {
								t.Fatal(err)
							}
							keys = append(keys, key)
						}
					}
					return true
				})
			}
		}
		if !found {
			t.Fatalf("%s is not in the package any more: update this test with it", name)
		}
	}
	sort.Strings(keys)
	return slices.Compact(keys)
}

// The key table is what the menu and the hints say, so it has to say what the handlers do. Adding
// a key to a handler without the table, or the other way round, fails here.
func TestKeyTableMatchesTheHandlers(t *testing.T) {
	fset := token.NewFileSet()
	files := map[string]*ast.File{}
	for _, name := range []string{"model.go", "builds.go"} {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = f
	}
	handlers := map[keyContext][]string{
		ctxDevices:    {"deviceKey", "moveCursor", "deviceAction"},
		ctxLogs:       {"logKey"},
		ctxBuilds:     {"buildKey"},
		ctxEverywhere: {"keyString"},
	}
	for ctx, funcs := range handlers {
		var table []string
		for _, a := range keyTable {
			if a.ctx == ctx {
				table = append(table, a.keys...)
			}
		}
		sort.Strings(table)
		table = slices.Compact(table)
		if got := keysIn(t, files, funcs...); !slices.Equal(got, table) {
			t.Errorf("%s: the handler takes %v, the key table lists %v", ctx, got, table)
		}
	}
}

// Everywhere keys are handled before a pane's, so a pane cannot use one.
func TestNoPaneKeyIsHiddenByAnEverywhereKey(t *testing.T) {
	everywhere := map[string]bool{}
	for _, a := range keyTable {
		if a.ctx == ctxEverywhere {
			for _, k := range a.keys {
				everywhere[k] = true
			}
		}
	}
	for _, a := range keyTable {
		for _, k := range a.keys {
			if a.ctx != ctxEverywhere && everywhere[k] {
				t.Errorf("%s: %q does nothing there, it is an everywhere key", a.id, k)
			}
		}
	}
}

// menuHeaders are the sections the menu shows, in order.
func menuHeaders(h *harness) []string {
	var out []string
	for _, r := range h.m.modal.keys.rows() {
		if r.header != "" {
			out = append(out, r.header)
		}
	}
	return out
}

func TestTheKeysMenuPutsTheFocusedPanesKeysFirst(t *testing.T) {
	h := start(t, iphone)
	h.press("?")
	h.wantScreen("Keys", "── Devices", "Boot a simulator", "1 of ")
	if got := menuHeaders(h); !slices.Equal(got, []string{"Devices", "Everywhere", "Logs"}) {
		t.Errorf("sections from the devices: %v", got)
	}

	h = start(t, pixel)
	h.openLog(pixel, "")
	h.press("?")
	if got := menuHeaders(h); !slices.Equal(got, []string{"Logs", "Everywhere", "Devices"}) {
		t.Errorf("sections from a log: %v", got)
	}
	h.wantScreen("── Logs", "Scroll")
}

func TestTheKeysMenuListsBuildsOnlyInAnExpoProject(t *testing.T) {
	h := start(t, iphone)
	h.press("?")
	if slices.Contains(menuHeaders(h), "Builds") {
		t.Error("the menu lists builds outside an Expo project")
	}

	h = startWithBuilds(t, &fakeBuilds{list: []eas.Build{androidBuild}}, iphone)
	h.press("tab", "?")
	if got := menuHeaders(h); !slices.Equal(got, []string{"Builds", "Everywhere", "Devices", "Logs"}) {
		t.Errorf("sections from the builds: %v", got)
	}
}

func TestTheKeysMenuClosesWithEscQOrTheKeyThatOpenedIt(t *testing.T) {
	for _, key := range []string{"esc", "q", "?"} {
		h := start(t, iphone)
		h.press("?", key)
		if h.m.modal != nil {
			t.Errorf("%s did not close the menu", key)
		}
		h.wantScreen("iPhone 17")
	}
}

func TestTheKeysMenuCursorStopsAtTheEnds(t *testing.T) {
	h := start(t, iphone)
	h.press("?", "k", "k")
	if at, _ := h.m.modal.keys.position(); at != 1 {
		t.Errorf("up from the top moved to %d", at)
	}
	h.press("G")
	at, total := h.m.modal.keys.position()
	if at != total || total < 30 {
		t.Errorf("G is at %d of %d", at, total)
	}
	h.press("j")
	if again, _ := h.m.modal.keys.position(); again != total {
		t.Errorf("down from the bottom moved to %d", again)
	}
	h.wantScreen(strconv.Itoa(total) + " of " + strconv.Itoa(total))
	h.press("g")
	if at, _ := h.m.modal.keys.position(); at != 1 {
		t.Errorf("g is at %d", at)
	}
	// Moving skips the section titles.
	for range total + 5 {
		h.press("j")
		if h.m.modal.keys.selected() == nil {
			t.Fatal("the cursor is on a section title")
		}
	}
}

func TestTheKeysMenuFilters(t *testing.T) {
	h := start(t, iphone)
	h.press("?", "/")
	h.typeText("wrap")
	h.wantScreen("/ wrap", "Wrap long lines", "1 of 1")
	h.wantNoScreen("Boot a simulator")
	// Enter keeps the filter and goes back to moving.
	h.press("enter")
	h.wantScreen("filter: wrap", "Wrap long lines")
	// Esc clears the filter first and closes the menu second.
	h.press("esc")
	h.wantScreen("Boot a simulator")
	if h.m.modal == nil {
		t.Fatal("esc closed the menu while a filter was set")
	}
	h.press("esc")
	if h.m.modal != nil {
		t.Error("esc did not close the menu")
	}

	h.press("?", "/")
	h.typeText("zzzz")
	h.wantScreen("No key matches zzzz")
	h.press("enter", "enter")
	if h.m.modal == nil {
		t.Error("enter with nothing under the cursor closed the menu")
	}
	// Backspace edits the filter.
	h.press("esc", "/")
	h.typeText("zq")
	h.press("backspace", "backspace")
	h.typeText("shut")
	h.wantScreen("Shut down")
}

func TestTheKeysMenuRunsTheActionUnderTheCursor(t *testing.T) {
	h := start(t, iphone)
	h.press("?", "/")
	h.typeText("copy the device")
	h.press("enter", "enter")
	if h.m.modal != nil {
		t.Fatal("running an action left the menu open")
	}
	h.wantScreen("Copied SIM-1")
}

// An action asks what its key asks: running erase from the menu still wants a yes.
func TestTheKeysMenuDoesNotSkipAQuestion(t *testing.T) {
	h := start(t, iphone)
	h.press("?", "/")
	h.typeText("erase a sim")
	h.press("enter", "enter")
	h.wantScreen("Erase iPhone 17?")
	h.wantCalls("erase", 0)
	h.press("y")
	h.wantCalls("erase", 1)
}

// A log's action runs in a log even when the menu was opened from the devices.
func TestTheKeysMenuMovesTheFocusToTheKeysPane(t *testing.T) {
	h := start(t, pixel)
	h.openLog(pixel, "")
	h.press("esc")
	if h.m.focusOnLog() {
		t.Fatal("esc left the focus on the log")
	}
	h.press("?", "/")
	h.typeText("wrap long")
	h.press("enter", "enter")
	if !h.m.focusOnLog() || !h.m.first().wrap {
		t.Errorf("focus on a log: %v, wrapped: %v", h.m.focusOnLog(), h.m.first().wrap)
	}

	// And a device action runs on the devices when the focus is on a log.
	h.press("?", "/")
	h.typeText("copy the device")
	h.press("enter", "enter")
	if h.m.focusOnLog() {
		t.Error("a device action left the focus on the log")
	}
	h.wantScreen("Copied emulator-5554")
}

func TestTheKeysMenuSaysWhyAnActionCannotBeDone(t *testing.T) {
	h := start(t, galaxy)
	h.press("?", "/")
	h.typeText("Boot a")
	h.wantScreen("is a real device")
	if !regexp.MustCompile(`\x1b\[(\d+;)*9(;\d+)*m`).MatchString(h.m.render()) {
		t.Error("an action that cannot be done is not struck through")
	}
	h.press("enter", "enter")
	if h.m.modal == nil {
		t.Error("the menu closed on an action that cannot be done")
	}
	h.wantCalls("boot", 0)
	h.wantScreen("is a real device")

	// Log keys cannot be used before a log is open.
	h.press("esc", "esc")
	h.press("?", "/")
	h.typeText("Wrap long")
	h.wantScreen("No log is open")
}

func TestTheKeysMenuFitsEverySize(t *testing.T) {
	h := start(t, pixel)
	h.openLog(pixel, "")
	for _, size := range [][2]int{{44, 10}, {60, 14}, {80, 20}, {100, 12}, {120, 30}, {200, 60}} {
		h.m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for _, filter := range []string{"", "l"} {
			h.press("?")
			if filter != "" {
				h.press("/")
				h.typeText(filter)
			}
			h.press("G")
			lines := strings.Split(h.screen(), "\n")
			if len(lines) != size[1] {
				t.Errorf("%dx%d: %d lines", size[0], size[1], len(lines))
			}
			h.press("esc", "esc")
			h.m.modal = nil
		}
	}
}

func TestTheWheelScrollsTheKeysMenu(t *testing.T) {
	h := start(t, iphone)
	h.press("?")
	h.m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown, X: 60, Y: 10})
	if at, _ := h.m.modal.keys.position(); at != 4 {
		t.Errorf("one notch down is at %d, want 4", at)
	}
	h.m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp, X: 60, Y: 10})
	h.m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp, X: 60, Y: 10})
	if at, _ := h.m.modal.keys.position(); at != 1 {
		t.Errorf("up past the top is at %d, want 1", at)
	}
	if h.m.modal == nil {
		t.Error("the wheel closed the menu")
	}
}

// A narrow screen cuts the hints from the middle, so the keys and quit are never the part lost.
func TestHintsKeepKeysAndQuitOnANarrowScreen(t *testing.T) {
	h := start(t, pixel)
	h.openLog(pixel, "")
	h.openLog(pixel, "")
	for _, width := range []int{44, 60, 80, 100, 120, 200} {
		line := h.m.hintsLine(width)
		if !strings.HasSuffix(line, "keys  q quit") {
			t.Errorf("width %d: %q does not end with the keys and quit", width, line)
		}
		// A line that left hints out says there are more.
		if full := h.m.hintsLine(1000); line != full && !strings.HasSuffix(line, "? more keys  q quit") {
			t.Errorf("width %d: %q left hints out without saying so", width, line)
		}
		if w := ansi.StringWidth(line); w > width-2 {
			t.Errorf("width %d: the hints are %d columns", width, w)
		}
	}
	if !strings.Contains(h.m.hintsLine(200), "S save") {
		t.Error("a wide screen should show every hint")
	}
}

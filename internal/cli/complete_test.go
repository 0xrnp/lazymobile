package cli

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/state"
)

// bashBreaks is bash's default COMP_WORDBREAKS.
const bashBreaks = " \t\n\"'><=;|&(:"

// complete is what lazymobile prints for zsh, which passes the words typed, or for bash and fish,
// which pass the line: one line of the result per candidate.
func (e *env) complete(t *testing.T, shell string, typed ...string) []string {
	t.Helper()
	args := append([]string{"__complete", shell}, typed...)
	switch shell {
	case "bash":
		args = []string{"__complete", "bash", bashBreaks, "lazymobile " + strings.Join(typed, " ")}
	case "fish":
		args = []string{"__complete", "fish", "lazymobile " + strings.Join(typed, " ")}
	}
	if code := e.run(args...); code != ExitOK {
		t.Fatalf("__complete %q: code %d, %s", typed, code, e.errs.String())
	}
	out := strings.TrimSuffix(e.out.String(), "\n")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// The commands and flags are listed by hand for completion, so this keeps them in step with usage.
func TestCompletionCoversEveryCommand(t *testing.T) {
	var commands []string
	for line := range strings.SplitSeq(usage, "\n") {
		if rest, ok := strings.CutPrefix(line, "  lazymobile "); ok {
			command, _, _ := strings.Cut(rest, " ")
			if command != "" && !slices.Contains(commands, command) {
				commands = append(commands, command)
			}
		}
	}
	for _, command := range commands {
		if _, ok := positionals[command]; !ok {
			t.Errorf("usage has %q, but completion does not know it", command)
		}
	}
	for command := range positionals {
		if !slices.Contains(commands, command) {
			t.Errorf("completion offers %q, which usage does not have", command)
		}
	}

	// A flag shown with a value, such as --out <file> or --tail N, must be known to take one, or
	// the value would be taken for the next argument.
	withValue := regexp.MustCompile(`(--[a-z-]+) (<[a-z -]+>|[A-Za-z0-9][a-z0-9|]*)[\] ]`)
	for line := range strings.SplitSeq(usage, "\n") {
		rest, ok := strings.CutPrefix(line, "  lazymobile ")
		if !ok {
			continue
		}
		synopsis, _, _ := strings.Cut(rest, "   ")
		for _, m := range withValue.FindAllStringSubmatch(synopsis+" ", -1) {
			// --all takes the patterns that follow as its arguments, not one value.
			if _, ok := flagValues[m[1]]; !ok && m[1] != "--all" {
				t.Errorf("%s takes a value in usage, but completion does not know it", m[1])
			}
		}
	}
	all := strings.Join(slices.Concat(slices.Collect(func(yield func([]string) bool) {
		for _, flags := range usageFlags() {
			if !yield(flags) {
				return
			}
		}
	})...), " ")
	for flag := range flagValues {
		if !strings.Contains(all+" ", flag+" ") {
			t.Errorf("completion knows %s, which usage does not have", flag)
		}
	}
}

func TestCompleteCommandsAndFlags(t *testing.T) {
	e := newEnv(t)
	if got := e.complete(t, "zsh", "la"); !slices.Equal(got, []string{"launch"}) {
		t.Errorf("la: %q", got)
	}
	if got := e.complete(t, "zsh", "launch", "iPhone 16", "--"); !slices.Equal(got, []string{"--restart", "--json"}) {
		t.Errorf("launch flags: %q", got)
	}
	if got := e.complete(t, "fish", "logs", `"iPhone 16"`, "--level", "e"); !slices.Equal(got, []string{"error"}) {
		t.Errorf("--level: %q", got)
	}
	if got := e.complete(t, "zsh", "completion", ""); !slices.Equal(got, []string{"zsh", "bash", "fish"}) {
		t.Errorf("completion: %q", got)
	}
	if got := e.complete(t, "zsh", "nosuch", ""); len(got) != 0 {
		t.Errorf("an unknown command: %q", got)
	}
	// After -- the words are the tool's own.
	if got := e.complete(t, "zsh", "run", "--", ""); len(got) != 0 {
		t.Errorf("after --: %q", got)
	}
	// A flag written with its value after =.
	if got := e.complete(t, "zsh", "logs", "iPhone 16", "--level=e"); !slices.Equal(got, []string{"--level=error"}) {
		t.Errorf("zsh --level=e: %q", got)
	}
	// bash replaces only what follows the =.
	if got := e.complete(t, "bash", "logs", `iPhone\ 16`, "--level=e"); !slices.Equal(got, []string{"error"}) {
		t.Errorf("bash --level=e: %q", got)
	}
	// A flag with its value after = is one word, so the device after it is still the first argument.
	if got := e.complete(t, "bash", "logs", "--level=error", `iPhone\ 16\ P`); !slices.Equal(got, []string{`iPhone\ 16\ Pro`}) {
		t.Errorf("after --level=error: %q", got)
	}
}

func TestCompletionFollowsFlagsThatChangeTheArguments(t *testing.T) {
	e := newEnv(t)
	if got := e.complete(t, "zsh", "install", "--all", ""); !slices.Equal(got, []string{completeFiles}) {
		t.Errorf("install --all: %q", got)
	}
	if got := e.complete(t, "zsh", "install", "--set=qa", ""); !slices.Equal(got, []string{completeFiles}) {
		t.Errorf("install --set=qa: %q", got)
	}
	for _, typed := range [][]string{{"delete", "--unavailable", ""}, {"action", "--list", ""}, {"boot", "--set", "qa", ""}} {
		if got := e.complete(t, "zsh", typed...); len(got) != 0 {
			t.Errorf("%q: %q", typed, got)
		}
	}
}

func TestShellWords(t *testing.T) {
	for line, want := range map[string]struct {
		words []string
		raw   string
		open  rune
	}{
		"lazymobile boot ":                     {[]string{"lazymobile", "boot", ""}, "", 0},
		`lazymobile boot iPhone\ 16\ P`:        {[]string{"lazymobile", "boot", "iPhone 16 P"}, `iPhone\ 16\ P`, 0},
		`lazymobile boot "iPhone 16 P`:         {[]string{"lazymobile", "boot", "iPhone 16 P"}, `"iPhone 16 P`, '"'},
		`lazymobile boot 'Rudra'\''s i`:        {[]string{"lazymobile", "boot", "Rudra's i"}, `'Rudra'\''s i`, '\''},
		`lazymobile boot "a \"b\" \c"`:         {[]string{"lazymobile", "boot", `a "b" \c`}, `"a \"b\" \c"`, 0},
		`lazymobile launch 192.168.1.5:5555 i`: {[]string{"lazymobile", "launch", "192.168.1.5:5555", "i"}, "i", 0},
	} {
		words, raw, open := shellWords(line)
		if !slices.Equal(words, want.words) || raw != want.raw || open != want.open {
			t.Errorf("%s: %q %q %q, want %q %q %q", line, words, raw, open, want.words, want.raw, want.open)
		}
	}
}

func TestBashLine(t *testing.T) {
	for _, c := range []struct{ value, raw, breaks, want string }{
		{"iPhone 16 Pro", `iPhone\ 1`, bashBreaks, `iPhone\ 16\ Pro`},
		{"192.168.1.5:5555", "192.168.1.5:5", bashBreaks, "5555"},
		{"192.168.1.5:5555", "192.168.1.5:5", " \t\n", "192.168.1.5:5555"},
		{"--level=error", "--level=e", bashBreaks, "error"},
	} {
		if got := bashLine(c.value, c.raw, 0, c.breaks); got != c.want {
			t.Errorf("%q in place of %q: %q, want %q", c.value, c.raw, got, c.want)
		}
	}
	// Inside an open quote bash keeps the quote, so nothing is escaped.
	if got := bashLine("iPhone 16 Pro", `"iPhone 1`, '"', bashBreaks); got != "iPhone 16 Pro" {
		t.Errorf("in quotes: %q", got)
	}
}

func TestCompleteDevices(t *testing.T) {
	e := newEnv(t)
	got := e.complete(t, "zsh", "boot", "")
	// The running one first, with what it is beside it.
	want := []string{"iPhone 16 Pro:simulator, ios 18.2, booted", "iPhone 16:simulator, ios 18.2, shutdown"}
	if !slices.Equal(got, want) {
		t.Errorf("zsh: %q, want %q", got, want)
	}
	// bash inserts what it is given, so a space is escaped.
	if got := e.complete(t, "bash", "boot", `iPhone\ 16\ P`); !slices.Equal(got, []string{`iPhone\ 16\ Pro`}) {
		t.Errorf("bash: %q", got)
	}
	if got := e.complete(t, "bash", "boot", `"iphone 16 P`); !slices.Equal(got, []string{"iPhone 16 Pro"}) {
		t.Errorf("bash in quotes: %q", got)
	}
	if got := e.complete(t, "fish", "boot", "iphone\\ 16\\ p"); !slices.Equal(got, []string{"iPhone 16 Pro\tsimulator, ios 18.2, booted"}) {
		t.Errorf("fish: %q", got)
	}
}

func TestCompleteAppsOfTheDevicesPlatform(t *testing.T) {
	e := newEnv(t)
	if err := e.app.State.Update(func(f *state.File) {
		f.Apps = map[device.Platform][]string{device.IOS: {"com.example.ios"}, device.Android: {"com.example.android"}}
	}); err != nil {
		t.Fatal(err)
	}
	if got := e.complete(t, "zsh", "launch", "iPhone 16", ""); !slices.Equal(got, []string{"com.example.ios"}) {
		t.Errorf("an iOS device: %q", got)
	}
	if got := e.complete(t, "zsh", "logs", "iPhone 16", "--app", ""); !slices.Equal(got, []string{"com.example.ios"}) {
		t.Errorf("--app: %q", got)
	}
	// bash passes the line, with the name's quotes.
	if got := e.complete(t, "bash", "launch", `"iPhone 16"`, ""); !slices.Equal(got, []string{"com.example.ios"}) {
		t.Errorf("a quoted name in bash: %q", got)
	}
	got := e.complete(t, "zsh", "launch", "nosuch", "")
	slices.Sort(got)
	if !slices.Equal(got, []string{"com.example.android", "com.example.ios"}) {
		t.Errorf("a device that is not found: %q", got)
	}
}

func TestCompletePermissionsOfTheDevicesPlatform(t *testing.T) {
	e := newEnv(t)
	got := e.complete(t, "zsh", "permission", "iPhone 16", "grant", "")
	if !slices.Contains(got, "photos-add") || slices.Contains(got, "camera") {
		t.Errorf("an iOS simulator: %q", got)
	}
	if got := e.complete(t, "zsh", "permission", "nosuch", "revoke", "ca"); !slices.Equal(got, []string{"calendar", "camera"}) {
		t.Errorf("a device that is not found: %q", got)
	}
}

func TestCompleteFilesWhereAPathGoes(t *testing.T) {
	e := newEnv(t)
	for _, typed := range [][]string{{"install", `iPhone\ 16`, ""}, {"screenshot", `iPhone\ 16`, "--out", "sh"}, {"screenshot", `iPhone\ 16`, "--out=sh"}} {
		if got := e.complete(t, "bash", typed...); !slices.Equal(got, []string{completeFiles}) {
			t.Errorf("%q: %q", typed, got)
		}
	}
}

func TestCompletionScripts(t *testing.T) {
	e := newEnv(t)
	for shell, want := range map[string]string{"zsh": "compdef _lazymobile lazymobile", "bash": "complete -F _lazymobile lazymobile", "fish": "complete -c lazymobile"} {
		if code := e.run("completion", shell); code != ExitOK || !strings.Contains(e.out.String(), want) {
			t.Errorf("%s: code %d, %q", shell, code, e.out.String())
		}
	}
	for _, args := range [][]string{{"completion"}, {"completion", "powershell"}, {"__complete"}, {"__complete", "bash", "lazymobile "}} {
		if code := e.run(args...); code != ExitUsage {
			t.Errorf("%q: code %d, want %d", args, code, ExitUsage)
		}
	}
}

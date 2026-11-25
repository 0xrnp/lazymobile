package cli

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/0xrnp/lazymobile/internal/android"
	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/mobile"
)

// A shell's completion script runs `lazymobile __complete` and shows the lines it prints. zsh passes
// the words typed, unquoted, and the current one last. bash passes its COMP_WORDBREAKS and the line
// up to the cursor, and fish the line: bash's own words are also split at : and =, which would
// break a serial such as 192.168.1.5:5555 or --level=error in two. Everything else about completion
// is decided here, so the three scripts stay a few lines each.

// completeFiles is printed alone when the shell should complete a file name instead.
const completeFiles = "__files__"

// completeTimeout bounds the device listing, so a slow adb never holds up the prompt.
const completeTimeout = 3 * time.Second

// argKind is what a positional argument or a flag's value is.
type argKind int

const (
	argNone argKind = iota
	argDevice
	argApp
	argFile
	argAction
	argSet
	argPermission
	argWords
)

type argSpec struct {
	kind  argKind
	words []string
}

var (
	deviceArg = argSpec{kind: argDevice}
	appArg    = argSpec{kind: argApp}
	fileArg   = argSpec{kind: argFile}
)

func words(w ...string) argSpec { return argSpec{kind: argWords, words: w} }

// positionals are the arguments each command takes, in order. A command that takes none, or only
// free text, has none listed. TestCompletionCoversEveryCommand keeps this in step with usage.
var positionals = map[string][]argSpec{
	"list": nil, "builds": nil, "doctor": nil, "mcp": nil, "version": nil, "changelog": nil,
	"boot": {deviceArg}, "shutdown": {deviceArg}, "erase": {deviceArg}, "delete": {deviceArg},
	"run": {deviceArg}, "link": {deviceArg}, "record": {deviceArg}, "screenshot": {deviceArg},
	"logs": {deviceArg}, "apps": {deviceArg}, "id": {deviceArg},
	"uninstall": {deviceArg, appArg}, "launch": {deviceArg, appArg}, "stop": {deviceArg, appArg},
	"cleardata": {deviceArg, appArg}, "crash": {deviceArg, appArg}, "report": {deviceArg, appArg},
	"push":       {deviceArg, appArg},
	"jank":       {deviceArg, appArg},
	"create":     {words("ios", "android")},
	"media":      {deviceArg, fileArg},
	"clipboard":  {deviceArg},
	"network":    {deviceArg, words(android.NetworkModes()...)},
	"install":    {deviceArg, fileArg},
	"appearance": {deviceArg, words("dark", "light")},
	"permission": {deviceArg, words("grant", "revoke"), {kind: argPermission}, appArg},
	"location":   {deviceArg, words("clear")},
	"statusbar":  {deviceArg, words("clean", "reset")},
	"dev":        {deviceArg, words("reverse", "menu", "reload")},
	"action":     {deviceArg, {kind: argAction}},
	"completion": {words("zsh", "bash", "fish")},
}

// flagValues are the flags that take a value, and what the value is. A flag missing here takes
// none, so the word after it is the next positional argument.
var flagValues = map[string]argSpec{
	"--app": appArg, "--out": fileArg, "--save": fileArg, "--payload": fileArg, "--set": {kind: argSet},
	"--title": {}, "--body": {}, "--type": {}, "--runtime": {}, "--image": {},
	"--mode":  words("logcat", "system", "console"),
	"--level": words("verbose", "debug", "info", "warning", "error", "fatal"),
	"--since": {}, "--tail": {}, "--duration": {}, "--lines": {}, "--limit": {}, "--port": {}, "--profile": {},
}

var usageFlag = regexp.MustCompile(`--[a-z][a-z-]*`)

// usageFlags are the flags each command's usage lines name, and the dashboard's under "".
func usageFlags() map[string][]string {
	flags := map[string][]string{"": {"--no-window", "--keep-simulator-app"}}
	for _, line := range strings.Split(usage, "\n") {
		rest, ok := strings.CutPrefix(line, "  lazymobile ")
		if !ok {
			continue
		}
		command, _, _ := strings.Cut(rest, " ")
		// The description after the synopsis can mention flags of the dashboard or the tool.
		synopsis, _, _ := strings.Cut(rest, "   ")
		synopsis, _, _ = strings.Cut(synopsis, "[-- ")
		for _, f := range usageFlag.FindAllString(synopsis, -1) {
			if !slices.Contains(flags[command], f) {
				flags[command] = append(flags[command], f)
			}
		}
	}
	return flags
}

// candidate is one completion: the word to insert and a note shown beside it.
type candidate struct{ value, note string }

func (a *App) complete(ctx context.Context, args []string) error {
	if len(args) < 2 || (args[0] == "bash" && len(args) != 3) || (args[0] == "fish" && len(args) != 2) {
		return usageError{"__complete needs a shell and what was typed"}
	}
	shell, typed := args[0], args[1:]
	// raw is the current word as it is on the line, and open the quote it leaves open, if any.
	var raw string
	var open rune
	if shell == "bash" || shell == "fish" {
		var words []string
		words, raw, open = shellWords(args[len(args)-1])
		if len(words) < 2 {
			return nil
		}
		typed = words[1:]
	}
	before, current := typed[:len(typed)-1], typed[len(typed)-1]
	cands, files := a.candidates(ctx, before, current)
	if files {
		_, err := fmt.Fprintln(a.Stdout, completeFiles)
		return err
	}
	for _, c := range cands {
		var line string
		switch shell {
		case "zsh":
			line = strings.ReplaceAll(c.value, ":", `\:`)
			if c.note != "" {
				line += ":" + c.note
			}
		case "fish":
			line = c.value
			if c.note != "" {
				line += "\t" + c.note
			}
		default:
			line = bashLine(c.value, raw, open, args[1])
		}
		if _, err := fmt.Fprintln(a.Stdout, line); err != nil {
			return err
		}
	}
	return nil
}

// candidates are the completions of current after the words before it, or files when the shell
// should complete a path.
func (a *App) candidates(ctx context.Context, before []string, current string) ([]candidate, bool) {
	flags := usageFlags()
	if len(before) == 0 {
		if strings.HasPrefix(current, "-") {
			return match(plain(flags[""]), current), false
		}
		var commands []string
		for command := range positionals {
			commands = append(commands, command)
		}
		slices.Sort(commands)
		return match(plain(commands), current), false
	}
	command := before[0]
	if _, known := positionals[command]; !known {
		return nil, false
	}
	specs := positionalsFor(command, before)
	// Walk the words after the command to see whether current is a flag's value or which
	// positional argument it is.
	var args []string
	for i := 1; i < len(before); i++ {
		word := before[i]
		if word == "--" {
			return nil, false // the rest is the tool's own
		}
		if strings.HasPrefix(word, "-") {
			if _, takesValue := flagValues[word]; takesValue {
				i++
			}
			continue
		}
		args = append(args, word)
	}
	if flag, partial, ok := strings.Cut(current, "="); ok && strings.HasPrefix(flag, "--") {
		spec, takesValue := flagValues[flag]
		if !takesValue {
			return nil, false
		}
		cands, files := a.argCandidates(ctx, spec, command, args, partial)
		for i := range cands {
			cands[i].value = flag + "=" + cands[i].value
		}
		return cands, files
	}
	if last := len(before) - 1; last >= 1 && strings.HasPrefix(before[last], "-") {
		if spec, takesValue := flagValues[before[last]]; takesValue {
			return a.argCandidates(ctx, spec, command, args, current)
		}
	}
	if strings.HasPrefix(current, "-") {
		return match(plain(flags[command]), current), false
	}
	if len(args) >= len(specs) {
		return nil, false
	}
	return a.argCandidates(ctx, specs[len(args)], command, args, current)
}

// positionalsFor are the arguments command takes, given the flags typed so far: some flags change
// them, such as install --all, which takes a file and no device.
func positionalsFor(command string, before []string) []argSpec {
	has := func(flag string) bool {
		return slices.ContainsFunc(before, func(w string) bool { return w == flag || strings.HasPrefix(w, flag+"=") })
	}
	switch {
	case command == "install" && (has("--all") || has("--set")):
		return []argSpec{fileArg}
	case command == "boot" && has("--set"), command == "delete" && has("--unavailable"), command == "action" && has("--list"):
		return nil
	}
	return positionals[command]
}

// argCandidates complete an argument of command, given the positional arguments typed before it.
func (a *App) argCandidates(ctx context.Context, spec argSpec, command string, args []string, current string) ([]candidate, bool) {
	switch spec.kind {
	case argFile:
		return nil, true
	case argWords:
		return match(plain(spec.words), current), false
	case argDevice:
		return match(a.deviceCandidates(ctx), current), false
	case argApp:
		d, found := a.typedDevice(ctx, command, args)
		return match(plain(a.rememberedApps(d, found)), current), false
	case argPermission:
		if d, found := a.typedDevice(ctx, command, args); found {
			return match(plain(mobile.PermissionNames(d.Platform)), current), false
		}
		names := mobile.PermissionNames(device.IOS)
		for _, name := range mobile.PermissionNames(device.Android) {
			if !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
		return match(plain(names), current), false
	case argAction:
		return match(plain(a.actionNames()), current), false
	case argSet:
		return match(plain(a.setNames()), current), false
	}
	return nil, false
}

// deviceCandidates offer a device by name when no other device has it, else by id, with what it
// is and its state beside it, running devices first.
func (a *App) deviceCandidates(ctx context.Context) []candidate {
	if a.Manager == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, completeTimeout)
	defer cancel()
	devices, _ := a.Manager.List(ctx)
	// A running device is the one usually meant, so those come first.
	slices.SortStableFunc(devices, func(x, y device.Device) int {
		switch {
		case x.Ready() == y.Ready():
			return 0
		case x.Ready():
			return -1
		}
		return 1
	})
	names := map[string]int{}
	for _, d := range devices {
		names[d.Name]++
	}
	cands := make([]candidate, 0, len(devices))
	for _, d := range devices {
		value, note := d.Name, string(d.Kind)
		if names[d.Name] > 1 {
			value, note = d.ID, d.Name+", "+note
		}
		if d.OSVersion != "" {
			note += ", " + string(d.Platform) + " " + d.OSVersion
		}
		cands = append(cands, candidate{value, note + ", " + string(d.State)})
	}
	return cands
}

// typedDevice is the device named by the command's first argument, when that is a device.
func (a *App) typedDevice(ctx context.Context, command string, args []string) (device.Device, bool) {
	specs := positionals[command]
	if len(args) == 0 || len(specs) == 0 || specs[0].kind != argDevice || a.Manager == nil {
		return device.Device{}, false
	}
	ctx, cancel := context.WithTimeout(ctx, completeTimeout)
	defer cancel()
	d, err := a.Manager.Find(ctx, args[0])
	return d, err == nil
}

// rememberedApps are the app ids used before: those of the device's platform when one was found,
// else all of them.
func (a *App) rememberedApps(d device.Device, found bool) []string {
	apps := a.State.Load().Apps
	if found {
		return apps[d.Platform]
	}
	var ids []string
	for _, recent := range apps {
		for _, id := range recent {
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

func (a *App) actionNames() []string {
	if a.Actions == nil {
		return nil
	}
	all, err := a.Actions()
	if err != nil {
		return nil
	}
	names := make([]string, len(all))
	for i, action := range all {
		names[i] = action.Name
	}
	return names
}

func (a *App) setNames() []string {
	if a.DeviceSets == nil {
		return nil
	}
	sets, err := a.DeviceSets()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(sets))
	for name := range sets {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func plain(values []string) []candidate {
	cands := make([]candidate, len(values))
	for i, v := range values {
		cands[i] = candidate{value: v}
	}
	return cands
}

// match keeps the candidates that start with what is typed, ignoring case, since device names
// are typed in any case.
func match(cands []candidate, typed string) []candidate {
	var kept []candidate
	for _, c := range cands {
		if strings.HasPrefix(strings.ToLower(c.value), strings.ToLower(typed)) {
			kept = append(kept, c)
		}
	}
	return kept
}

// shellWords splits a command line as bash and fish do, as far as completion needs: blanks separate
// words, a backslash keeps the next character, and quotes keep what is between them. The last word
// is the one being completed, empty when the line ends in a blank. raw is that word as typed, and
// open the quote it leaves open, if any.
func shellWords(line string) (words []string, raw string, open rune) {
	var word strings.Builder
	inWord, start := false, 0
	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if open == 0 && (r == ' ' || r == '\t' || r == '\n') {
			if inWord {
				words = append(words, word.String())
				word.Reset()
				inWord = false
			}
			continue
		}
		if !inWord {
			inWord, start = true, i
		}
		switch {
		case open == '\'':
			if r == '\'' {
				open = 0
			} else {
				word.WriteRune(r)
			}
		case open == '"':
			switch {
			case r == '"':
				open = 0
			case r == '\\' && i+1 < len(runes) && strings.ContainsRune("\"\\$`", runes[i+1]):
				i++
				word.WriteRune(runes[i])
			default:
				word.WriteRune(r)
			}
		case r == '\\':
			if i+1 < len(runes) {
				i++
				word.WriteRune(runes[i])
			}
		case r == '\'' || r == '"':
			open = r
		default:
			word.WriteRune(r)
		}
	}
	if !inWord {
		return append(words, ""), "", open
	}
	return append(words, word.String()), string(runes[start:]), open
}

// bashLine is value as bash should insert it in place of raw. Inside an open quote it is inserted
// as it is. Otherwise it is escaped, and the part of raw up to its last : or =, when those break
// words in COMP_WORDBREAKS, is left out, since bash replaces only what follows.
func bashLine(value, raw string, open rune, breaks string) string {
	if open != 0 {
		return value
	}
	line := bashEscape(value)
	var cut string
	for _, r := range ":=" {
		if strings.ContainsRune(breaks, r) {
			cut += string(r)
		}
	}
	if i := strings.LastIndexAny(raw, cut); cut != "" && i >= 0 && strings.HasPrefix(line, raw[:i+1]) {
		line = line[i+1:]
	}
	return line
}

// bashEscape puts a backslash before each character bash would split or expand, since bash
// inserts a completion as typed.
func bashEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(" \t'\"\\()[]{}<>|&;$`!*?#~", r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

const zshCompletion = `#compdef lazymobile
# lazymobile completion for zsh. Load it with: source <(lazymobile completion zsh)

_lazymobile() {
  local -a lines
  lines=("${(@f)$(lazymobile __complete zsh "${(@Q)words[2,CURRENT-1]}" "${(Q)PREFIX}" 2>/dev/null)}")
  if [[ "${lines[1]}" == __files__ ]]; then
    compset -P '--*='
    _files
    return
  fi
  # Device names are offered whatever the case typed.
  [[ -n "${lines[1]}" ]] && _describe -t values lazymobile lines -M 'm:{a-zA-Z}={A-Za-z}'
}

if (( $+functions[compdef] )); then
  compdef _lazymobile lazymobile
fi
`

const bashCompletion = `# lazymobile completion for bash. Load it with: eval "$(lazymobile completion bash)"

_lazymobile() {
  local line
  COMPREPLY=()
  while IFS= read -r line; do
    if [[ $line == __files__ ]]; then
      COMPREPLY=()
      compopt -o default 2>/dev/null
      return
    fi
    COMPREPLY+=("$line")
  done < <(lazymobile __complete bash "$COMP_WORDBREAKS" "${COMP_LINE:0:COMP_POINT}" 2>/dev/null)
}

if type compopt >/dev/null 2>&1; then
  complete -F _lazymobile lazymobile
else
  # bash 3.2, the one macOS ships, has no compopt to ask for file names, so it always falls back to them.
  complete -o default -F _lazymobile lazymobile
fi
`

const fishCompletion = `# lazymobile completion for fish. Load it with: lazymobile completion fish | source

function __lazymobile_complete
    set -l out (lazymobile __complete fish (commandline -cp) 2>/dev/null)
    if test "$out[1]" = __files__
        set -l token (commandline -ct)
        set -l flag (string match -r -- '^--[^=]*=' $token)
        __fish_complete_path (string replace -r -- '^--[^=]*=' '' $token) | string replace -r -- '^' "$flag"
        return
    end
    printf '%s\n' $out
end

complete -c lazymobile -f -a '(__lazymobile_complete)'
`

// completion prints the completion script for a shell.
func (a *App) completion(args []string) error {
	if len(args) != 1 {
		return usageError{"completion needs a shell: zsh, bash or fish"}
	}
	scripts := map[string]string{"zsh": zshCompletion, "bash": bashCompletion, "fish": fishCompletion}
	script, ok := scripts[args[0]]
	if !ok {
		return usageError{fmt.Sprintf("no completion for %q: zsh, bash or fish", args[0])}
	}
	_, err := fmt.Fprint(a.Stdout, script)
	return err
}

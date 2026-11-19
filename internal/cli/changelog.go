package cli

import (
	"flag"
	"fmt"
	"strings"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/changelog"
)

// changelog prints what changed: the notes of the running version, of one version, or of all.
func (a *App) changelog(args []string) error {
	fs := flag.NewFlagSet("changelog", flag.ContinueOnError)
	all := fs.Bool("all", false, "")
	asJSON := fs.Bool("json", false, "")
	positional, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(positional) > 1 || (*all && len(positional) == 1) {
		return usageError{"changelog takes one version, or --all"}
	}
	entries := changelog.Parse(a.Changelog)
	if len(entries) == 0 {
		return apperr.New(apperr.NotFound, "This build has no changelog")
	}
	chosen := entries
	switch {
	case *all:
	case len(positional) == 1:
		entry, found := changelog.Find(entries, positional[0])
		if !found {
			return apperr.New(apperr.NotFound, "No notes for version %s", positional[0]).
				WithHint("`lazymobile changelog --all` lists every version.")
		}
		chosen = []changelog.Entry{entry}
	default:
		// The running version's notes, or the newest ones for a build in between releases.
		chosen = entries[:1]
		if entry, found := changelog.Find(entries, a.Version); found && changelog.Released(a.Version) {
			chosen = []changelog.Entry{entry}
		}
	}
	if *asJSON {
		return a.writeJSON(map[string]any{"entries": chosen})
	}
	texts := make([]string, len(chosen))
	for i, e := range chosen {
		texts[i] = e.Text()
	}
	fmt.Fprintln(a.Stdout, strings.Join(texts, "\n\n"))
	return nil
}

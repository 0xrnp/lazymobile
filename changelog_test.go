package lazymobile

import (
	"strings"
	"testing"

	"github.com/0xrnp/lazymobile/internal/changelog"
)

// The notes are read by the program, so a mistake in the file is a mistake in the product.
func TestTheChangelogCanBeReadAndIsInOrder(t *testing.T) {
	entries := changelog.Parse(Changelog)
	if len(entries) < 2 {
		t.Fatalf("only %d versions were read", len(entries))
	}
	var previous *changelog.Entry
	for i, e := range entries {
		if e.Notes == "" {
			t.Errorf("%s has no notes", e.Version)
		}
		if e.Version == changelog.Unreleased && i != 0 {
			t.Errorf("Unreleased is not first")
		}
		if e.Version != changelog.Unreleased {
			if e.Date == "" {
				t.Errorf("%s has no date", e.Version)
			}
			if previous != nil && changelog.Compare(previous.Version, e.Version) <= 0 {
				t.Errorf("%s comes after %s: the newest must be first", e.Version, previous.Version)
			}
			previous = &entries[i]
		}
	}
	if strings.Contains(Changelog, "—") {
		t.Error("the changelog has an em dash")
	}
}

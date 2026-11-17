package logs

import (
	"reflect"
	"strings"
	"testing"
)

func TestQueryMatches(t *testing.T) {
	cases := []struct {
		query, line string
		want        bool
	}{
		{"", "anything", true},
		{"error", "An ERROR happened", true},
		{"error", "all fine", false},
		{"error timeout", "error: request timeout", true},
		{"error timeout", "error: refused", false},
		{"error -noisy", "error from noisy", false},
		{"error -noisy", "error from quiet", true},
		{"-noisy", "quiet", true},
		{`/id=\d+/`, "user id=42 left", true},
		{`/id=\d+/`, "user id=x left", false},
		{`/^D /`, "D tag", true},
		{`-/^D /`, "D tag", false},
		{`-/^D /`, "E tag", true},
		{`"two words"`, "has two words in it", true},
		{`"two words"`, "two other words", false},
		{`/a b/ c`, "a b and c", true},
		{`-"two words" ok`, "ok two words", false},
		{"a.b", "axb", false},
		{"a.b", "a.b", true},
		{"-", "x - y", true},
		{"- x", "- x", true},
		{`/(?-i)Error/`, "error", false},
		{`/(?-i)Error/`, "Error", true},
	}
	for _, c := range cases {
		q, err := ParseQuery(c.query)
		if err != nil {
			t.Errorf("%q: %v", c.query, err)
			continue
		}
		if got := q.Match(c.line); got != c.want {
			t.Errorf("query %q on %q = %v, want %v", c.query, c.line, got, c.want)
		}
	}
}

func TestQueryErrorsSayWhatIsWrong(t *testing.T) {
	cases := map[string]string{
		`/abc`:      "closing /",
		`//`:        "empty",
		`/(/`:       "not valid",
		`"abc`:      "closing",
		`/a/b`:      "put a space",
		`"a"b`:      "put a space",
		`error /x(`: "closing /",
	}
	for query, want := range cases {
		if _, err := ParseQuery(query); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: error %v, want one about %q", query, err, want)
		}
	}
}

func TestQuerySpans(t *testing.T) {
	q, _ := ParseQuery(`err /\d+/ -skip`)
	got := q.Spans("Err 42 and err")
	want := [][2]int{{0, 3}, {4, 6}, {11, 14}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("spans = %v, want %v", got, want)
	}
	overlap, _ := ParseQuery("abc bcd")
	if got := overlap.Spans("abcd"); !reflect.DeepEqual(got, [][2]int{{0, 4}}) {
		t.Errorf("overlapping words should merge: %v", got)
	}
	if got := (Query{}).Spans("x"); got != nil {
		t.Errorf("an empty query highlights nothing: %v", got)
	}
}

func TestPlainQueryTakesTextAsItIs(t *testing.T) {
	q := PlainQuery(`/open -x`)
	if !q.Match("see /open -x here") || q.Match("open") || q.Source() != `/open -x` {
		t.Errorf("PlainQuery did not take the text literally")
	}
	if !PlainQuery("").Empty() {
		t.Errorf("an empty plain query should be empty")
	}
}

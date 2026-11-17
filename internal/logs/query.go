package logs

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Query is what the filter box holds. Words are separated by spaces and a line must pass all of
// them, ignoring case:
//
//	error          lines that contain "error"
//	-noisy         lines that do not contain "noisy"
//	/id=\d+/       lines the regular expression matches (Go syntax, ignoring case unless the
//	               expression says otherwise with (?-i))
//	-/^D /         lines the regular expression does not match
//	"two words"    quotes keep a space inside one word
type Query struct {
	source  string
	include []*regexp.Regexp
	exclude []*regexp.Regexp
}

// Source is the text the query was made from.
func (q Query) Source() string { return q.source }

// Empty reports whether every line passes.
func (q Query) Empty() bool { return len(q.include) == 0 && len(q.exclude) == 0 }

// PlainQuery is text to look for as it is, with no syntax: the fallback for a saved filter that
// does not parse.
func PlainQuery(text string) Query {
	if text == "" {
		return Query{}
	}
	return Query{source: text, include: []*regexp.Regexp{literal(text)}}
}

func literal(text string) *regexp.Regexp {
	return regexp.MustCompile("(?i)" + regexp.QuoteMeta(text))
}

// ParseQuery reads a filter. The error says what is wrong, for the person typing it.
func ParseQuery(text string) (Query, error) {
	q := Query{source: text}
	rest := strings.TrimLeft(text, " \t")
	for rest != "" {
		negate := false
		if strings.HasPrefix(rest, "-") && len(rest) > 1 && rest[1] != ' ' && rest[1] != '\t' {
			negate, rest = true, rest[1:]
		}
		var re *regexp.Regexp
		var err error
		switch rest[0] {
		case '/':
			end := strings.IndexByte(rest[1:], '/')
			if end < 0 {
				return Query{}, fmt.Errorf("a regular expression needs a closing /")
			}
			pattern := rest[1 : 1+end]
			if pattern == "" {
				return Query{}, fmt.Errorf("the regular expression // is empty")
			}
			if re, err = regexp.Compile("(?i)" + pattern); err != nil {
				return Query{}, fmt.Errorf("the regular expression is not valid: %v", trimRegexpError(err))
			}
			rest = rest[end+2:]
		case '"':
			end := strings.IndexByte(rest[1:], '"')
			if end < 0 {
				return Query{}, fmt.Errorf("a quote needs a closing \"")
			}
			if word := rest[1 : 1+end]; word != "" {
				re = literal(word)
			}
			rest = rest[end+2:]
		default:
			end := strings.IndexAny(rest, " \t")
			if end < 0 {
				end = len(rest)
			}
			re, rest = literal(rest[:end]), rest[end:]
		}
		if !strings.HasPrefix(rest, " ") && !strings.HasPrefix(rest, "\t") && rest != "" {
			return Query{}, fmt.Errorf("put a space after %q", text[:len(text)-len(rest)])
		}
		rest = strings.TrimLeft(rest, " \t")
		switch {
		case re == nil:
		case negate:
			q.exclude = append(q.exclude, re)
		default:
			q.include = append(q.include, re)
		}
	}
	return q, nil
}

func trimRegexpError(err error) string {
	return strings.TrimPrefix(err.Error(), "error parsing regexp: ")
}

// Match reports whether a line of text passes.
func (q Query) Match(text string) bool {
	for _, re := range q.include {
		if !re.MatchString(text) {
			return false
		}
	}
	for _, re := range q.exclude {
		if re.MatchString(text) {
			return false
		}
	}
	return true
}

// Spans are the byte ranges of text to highlight: what the wanted words matched, in order and
// not overlapping.
func (q Query) Spans(text string) [][2]int {
	var spans [][2]int
	for _, re := range q.include {
		for _, m := range re.FindAllStringIndex(text, -1) {
			if m[1] > m[0] && utf8.ValidString(text[m[0]:m[1]]) {
				spans = append(spans, [2]int{m[0], m[1]})
			}
		}
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i][0] < spans[j][0] })
	var merged [][2]int
	for _, s := range spans {
		if n := len(merged); n > 0 && s[0] <= merged[n-1][1] {
			merged[n-1][1] = max(merged[n-1][1], s[1])
			continue
		}
		merged = append(merged, s)
	}
	return merged
}

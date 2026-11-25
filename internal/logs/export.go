package logs

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/0xrnp/lazymobile/internal/apperr"
)

// Format is a way to write lines to a file.
type Format string

const (
	// Text is the lines as they are, one per row.
	Text Format = "text"
	// JSON is a list of lines with their fields, as logs --json gives them one by one.
	JSON Format = "json"
	// JSONLines is one JSON line per log line: it can be appended to and read as a stream.
	JSONLines Format = "jsonl"
	// CSV has a row per line, for a spreadsheet.
	CSV Format = "csv"
	// Markdown is a fenced block under a heading, to paste into an issue or a pull request.
	Markdown Format = "md"
)

// Formats are the formats Encode writes.
func Formats() []Format { return []Format{Text, JSON, JSONLines, CSV, Markdown} }

// ParseFormat reads a format by its name or by a file extension (with or without the dot), so
// that log, txt, ndjson and markdown work too.
func ParseFormat(name string) (Format, bool) {
	switch strings.ToLower(strings.TrimPrefix(strings.TrimSpace(name), ".")) {
	case "text", "txt", "log":
		return Text, true
	case "json":
		return JSON, true
	case "jsonl", "ndjson":
		return JSONLines, true
	case "csv":
		return CSV, true
	case "md", "markdown":
		return Markdown, true
	}
	return "", false
}

// FormatOf is the format a file name asks for by its extension, and Text when it has none that is
// known.
func FormatOf(path string) Format {
	if f, ok := ParseFormat(filepath.Ext(path)); ok {
		return f
	}
	return Text
}

// Extension is the file extension of f, with the dot.
func (f Format) Extension() string {
	switch f {
	case JSON:
		return ".json"
	case JSONLines:
		return ".jsonl"
	case CSV:
		return ".csv"
	case Markdown:
		return ".md"
	}
	return ".log"
}

// Meta says what the lines are, for the formats that have a heading.
type Meta struct {
	Device, App string
}

// Encode writes lines in format f. A line's text is never changed, so a token in a log stays in
// the file: the file is written for the person alone (see WriteFile).
func Encode(f Format, lines []Line, meta Meta) ([]byte, error) {
	var b bytes.Buffer
	switch f {
	case Text:
		for _, line := range lines {
			b.WriteString(line.Text)
			b.WriteByte('\n')
		}
	case JSON:
		if lines == nil {
			lines = []Line{}
		}
		data, err := json.MarshalIndent(lines, "", "  ")
		if err != nil {
			return nil, err
		}
		b.Write(data)
		b.WriteByte('\n')
	case JSONLines:
		enc := json.NewEncoder(&b)
		for _, line := range lines {
			if err := enc.Encode(line); err != nil {
				return nil, err
			}
		}
	case CSV:
		w := csv.NewWriter(&b)
		_ = w.Write([]string{"line", "level", "pid", "crash", "text"})
		for i, line := range lines {
			level := ""
			if line.Level != NoLevel {
				level = line.Level.String()
			}
			// A cell that starts with = + - or @ would run as a formula in a spreadsheet.
			_ = w.Write([]string{strconv.Itoa(i + 1), level, line.PID, strconv.FormatBool(line.Crash), spreadsheetSafe(line.Text)})
		}
		w.Flush()
		if err := w.Error(); err != nil {
			return nil, err
		}
	case Markdown:
		title := "Log"
		if meta.App != "" {
			title += " of " + meta.App
		}
		if meta.Device != "" {
			title += " on " + meta.Device
		}
		fmt.Fprintf(&b, "### %s\n\n%d lines.\n\n", title, len(lines))
		fence := strings.Repeat("`", 3)
		for _, line := range lines {
			if n := longestRun(line.Text, '`'); n >= len(fence) {
				fence = strings.Repeat("`", n+1)
			}
		}
		b.WriteString(fence + "\n")
		for _, line := range lines {
			b.WriteString(line.Text)
			b.WriteByte('\n')
		}
		b.WriteString(fence + "\n")
	default:
		return nil, apperr.New(apperr.InvalidState, "Unknown log format %q", string(f)).
			WithHint("Use one of: text, json, jsonl, csv, md.")
	}
	return b.Bytes(), nil
}

func spreadsheetSafe(text string) string {
	if text != "" && strings.ContainsRune("=+-@\t\r", rune(text[0])) {
		return "'" + text
	}
	return text
}

func longestRun(s string, c byte) int {
	longest, run := 0, 0
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return longest
}

// WriteFile writes lines to path in format f. It never replaces a file, and only the user can read
// the result, since a log can hold tokens and personal data. It returns the full path.
func WriteFile(path string, f Format, lines []Line, meta Meta) (string, error) {
	data, err := Encode(f, lines, meta)
	if err != nil {
		return "", err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", apperr.New(apperr.InvalidState, "That does not look like a file name: %v", err)
	}
	if info, err := os.Stat(filepath.Dir(path)); err != nil || !info.IsDir() {
		return "", apperr.New(apperr.InvalidState, "The folder %s does not exist", filepath.Dir(path)).WithHint("Choose a file in a folder that exists.")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return "", apperr.New(apperr.InvalidState, "%s already exists", path).WithHint("Choose another name: lazymobile never replaces a file.")
		}
		return "", apperr.New(apperr.CommandFailed, "Could not write %s: %v", path, err)
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		os.Remove(path)
		return "", apperr.New(apperr.CommandFailed, "Could not write %s: %v", path, err)
	}
	if err := file.Close(); err != nil {
		return "", apperr.New(apperr.CommandFailed, "Could not write %s: %v", path, err)
	}
	return path, nil
}

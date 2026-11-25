package logs

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var sample = []Line{
	{Text: "I hello, \"world\"", Level: Info, PID: "42"},
	{Text: "E boom ```fenced``` here", Level: Error, Crash: true},
	{Text: "=HYPERLINK(\"http://evil\")"},
}

func TestEncodeWritesEachFormat(t *testing.T) {
	text, _ := Encode(Text, sample, Meta{})
	if string(text) != "I hello, \"world\"\nE boom ```fenced``` here\n=HYPERLINK(\"http://evil\")\n" {
		t.Errorf("text %q", text)
	}
	data, _ := Encode(JSON, sample, Meta{})
	var back []Line
	if err := json.Unmarshal(data, &back); err != nil || len(back) != 3 || back[1].Crash != true || back[0].PID != "42" {
		t.Errorf("json %v %s", err, data)
	}
	if empty, _ := Encode(JSON, nil, Meta{}); strings.TrimSpace(string(empty)) != "[]" {
		t.Errorf("empty json %q", empty)
	}
	data, _ = Encode(JSONLines, sample, Meta{})
	rows := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(rows) != 3 || !strings.HasPrefix(rows[0], `{"text":"I hello`) {
		t.Errorf("jsonl %q", data)
	}
	data, _ = Encode(CSV, sample, Meta{})
	cells, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
	if err != nil || len(cells) != 4 || cells[1][1] != "info" || cells[2][3] != "true" || cells[1][4] != `I hello, "world"` {
		t.Errorf("csv %v %q", err, cells)
	}
	// A spreadsheet would run this as a formula.
	if cells[3][4] != `'=HYPERLINK("http://evil")` {
		t.Errorf("formula cell %q", cells[3][4])
	}
}

func TestMarkdownUsesAFenceLongerThanAnyInTheLog(t *testing.T) {
	data, _ := Encode(Markdown, sample, Meta{Device: "Pixel 9a", App: "com.example.app"})
	got := string(data)
	if !strings.HasPrefix(got, "### Log of com.example.app on Pixel 9a\n\n3 lines.\n\n````\n") || !strings.HasSuffix(got, "\n````\n") {
		t.Errorf("markdown %q", got)
	}
	if _, err := Encode(Format("pdf"), sample, Meta{}); err == nil {
		t.Error("an unknown format was accepted")
	}
}

func TestFormatsAreFoundByNameOrExtension(t *testing.T) {
	for in, want := range map[string]Format{"json": JSON, ".JSONL": JSONLines, "ndjson": JSONLines, "txt": Text, "log": Text, "markdown": Markdown, "csv": CSV} {
		if got, ok := ParseFormat(in); !ok || got != want {
			t.Errorf("%q: %q %v", in, got, ok)
		}
	}
	if _, ok := ParseFormat("pdf"); ok {
		t.Error("pdf is a format")
	}
	if FormatOf("a/b/run.CSV") != CSV || FormatOf("noext") != Text || FormatOf("x.unknown") != Text {
		t.Error("FormatOf")
	}
}

func TestWriteFileNeverReplacesAndIsPrivate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")
	got, err := WriteFile(path, Text, sample, Meta{})
	if err != nil || got != path {
		t.Fatalf("%q %v", got, err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", info.Mode().Perm())
	}
	if _, err := WriteFile(path, Text, nil, Meta{}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("replaced: %v", err)
	}
	if content, _ := os.ReadFile(path); !strings.Contains(string(content), "boom") {
		t.Errorf("the first file was changed: %q", content)
	}
	if _, err := WriteFile(filepath.Join(dir, "nope", "b.log"), Text, nil, Meta{}); err == nil {
		t.Error("a missing folder was accepted")
	}
}

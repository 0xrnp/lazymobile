package tui

import (
	"fmt"
	"testing"

	"github.com/0xrnp/lazymobile/internal/logs"
)

func floodBatch(n, from int) []logs.Line {
	batch := make([]logs.Line, n)
	for i := range batch {
		batch[i] = logs.Line{Text: fmt.Sprintf("I/Choreographer( 4406): line %d of the flood that an app writes while it runs", from+i)}
	}
	return batch
}

// BenchmarkLogAddFull is a log that is full and takes a batch of lines each time, as when an app
// is chatty: every batch must drop as many lines as it adds.
func BenchmarkLogAddFull(b *testing.B) {
	v := &logView{follow: true}
	v.add(floodBatch(maxLogLines, 0))
	batch := floodBatch(50, maxLogLines)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		v.add(batch)
	}
}

// BenchmarkCleanPlain is a typical log line, which has nothing to take out.
func BenchmarkCleanPlain(b *testing.B) {
	line := "I/Choreographer( 4406): Skipped 33 frames!  The application may be doing too much work on its main thread."
	b.ReportAllocs()
	for range b.N {
		_ = clean(line)
	}
}

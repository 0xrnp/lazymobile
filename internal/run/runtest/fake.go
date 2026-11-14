// Package runtest answers for external tools in tests.
package runtest

import (
	"context"
	"strings"
	"sync"

	"github.com/0xrnp/lazymobile/internal/run"
)

// Fake is a Runner that answers from a function and records what it was asked.
type Fake struct {
	// Answer decides the result of each command. The command is its arguments joined by spaces.
	Answer func(command string, argv []string, opts run.Options) run.Result

	mu    sync.Mutex
	calls []string
}

// Run implements run.Runner.
func (f *Fake) Run(_ context.Context, argv []string, opts run.Options) run.Result {
	command := strings.Join(argv, " ")
	f.mu.Lock()
	f.calls = append(f.calls, command)
	f.mu.Unlock()
	if f.Answer == nil {
		return run.Result{Missing: true}
	}
	return f.Answer(command, argv, opts)
}

// Calls returns every command so far, in order.
func (f *Fake) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// Called reports whether a command containing part was run.
func (f *Fake) Called(part string) bool {
	for _, call := range f.Calls() {
		if strings.Contains(call, part) {
			return true
		}
	}
	return false
}

// Table answers each command by the first key it contains. Commands that match nothing are
// reported as missing tools.
func Table(answers map[string]run.Result) *Fake {
	return &Fake{Answer: func(command string, _ []string, _ run.Options) run.Result {
		best := ""
		for key := range answers {
			// The longest matching key wins, so a specific answer beats a general one.
			if strings.Contains(command, key) && len(key) > len(best) {
				best = key
			}
		}
		if best == "" {
			return run.Result{Missing: true}
		}
		return answers[best]
	}}
}

// Out is a successful result with this output.
func Out(stdout string) run.Result {
	return run.Result{Stdout: stdout}
}

// Fail is a failed result with this exit code and error output.
func Fail(code int, stderr string) run.Result {
	return run.Result{Code: code, Stderr: stderr}
}

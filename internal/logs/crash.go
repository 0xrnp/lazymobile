package logs

import (
	"regexp"
	"strings"
)

// crashPhrases mark a crash whatever stream they come from: an Android fatal exception or signal,
// an iOS uncaught exception, Swift fatal error or crash type, and the errors React Native stops on.
var crashPhrases = []string{
	"FATAL EXCEPTION",
	"Fatal signal ",
	"Terminating app due to uncaught exception",
	// Swift's runtime writes this before it stops the app: a forced unwrap of nil, an index out of
	// range, or fatalError().
	"Fatal error: ",
	"Unhandled JS Exception",
	"Invariant Violation",
	"EXC_BAD_ACCESS",
	"EXC_BAD_INSTRUCTION",
	"EXC_BREAKPOINT",
	"EXC_CRASH",
}

// androidCrashTag matches the lines of a crash report in logcat. Every line of the trace has the
// tag and the error level, not only the first, so each is marked.
var androidCrashTag = regexp.MustCompile(`\s[EF]\s+(AndroidRuntime|ReactNativeJS):`)

func hasCrashPhrase(text string) bool {
	for _, phrase := range crashPhrases {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

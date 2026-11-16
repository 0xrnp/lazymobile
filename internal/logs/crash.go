package logs

import (
	"regexp"
	"strings"
)

// crashPhrases mark a crash whatever stream they come from: an Android fatal exception or signal,
// an iOS uncaught exception or crash type, and the errors React Native stops on.
var crashPhrases = []string{
	"FATAL EXCEPTION",
	"Fatal signal ",
	"Terminating app due to uncaught exception",
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

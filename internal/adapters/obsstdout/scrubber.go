package obsstdout

import (
	"strings"

	"codereviewer/internal/ports"
)

// scrubbingLogger wraps a ports.Logger and redacts values that look
// like raw code, diffs, or oversized prose before passing them through.
// The system-wide invariant ("no payload logging") is meant to be
// enforced at the call site, but this is a defense in depth — if an
// error message accidentally embeds a chunk of source, the redaction
// fires here and operators don't see customer code in their dashboards.
//
// Heuristics:
//   - Any string value longer than maxLen is truncated to "<scrubbed: %d
//     chars>" so length leaks but content does not.
//   - Strings containing diff hunk markers ("@@", "diff --git"), code
//     block fences ("```"), or three+ consecutive newlines are redacted
//     wholesale.
//
// The scrubber is intentionally aggressive — false positives (a long
// commit message gets redacted) cost a log line; false negatives (a
// diff slips through) cost a compliance escalation.
type scrubbingLogger struct {
	inner  ports.Logger
	maxLen int
}

// NewScrubbingLogger wraps inner. maxLen is the per-value byte ceiling
// before truncation; the default (300) keeps log lines readable while
// catching any prose-sized leakage.
func NewScrubbingLogger(inner ports.Logger, maxLen int) ports.Logger {
	if maxLen <= 0 {
		maxLen = 300
	}
	return &scrubbingLogger{inner: inner, maxLen: maxLen}
}

func (l *scrubbingLogger) Info(msg string, kv ...any)  { l.inner.Info(msg, l.scrubKv(kv)...) }
func (l *scrubbingLogger) Warn(msg string, kv ...any)  { l.inner.Warn(msg, l.scrubKv(kv)...) }
func (l *scrubbingLogger) Error(msg string, kv ...any) { l.inner.Error(msg, l.scrubKv(kv)...) }

// scrubKv walks the key/value pairs, replacing suspicious values.
// Keys are not redacted (they're known field names). Non-string values
// pass through unchanged — they're cheap to log and never carry prose.
func (l *scrubbingLogger) scrubKv(kv []any) []any {
	if len(kv) == 0 {
		return kv
	}
	out := make([]any, len(kv))
	for i, v := range kv {
		// Odd index = value; even index = key.
		if i%2 == 1 {
			out[i] = l.scrubValue(v)
		} else {
			out[i] = v
		}
	}
	return out
}

func (l *scrubbingLogger) scrubValue(v any) any {
	s, ok := v.(string)
	if !ok {
		return v
	}
	return l.scrubString(s)
}

// ScrubString is exported only for testing. Production callers should
// go through the logger wrapper.
func ScrubString(s string, maxLen int) string {
	if maxLen <= 0 {
		maxLen = 300
	}
	return (&scrubbingLogger{maxLen: maxLen}).scrubString(s)
}

func (l *scrubbingLogger) scrubString(s string) string {
	if looksLikePayload(s) {
		return "<scrubbed: payload-shaped>"
	}
	if len(s) > l.maxLen {
		return "<scrubbed: " + lenWord(s) + " chars>"
	}
	return s
}

// looksLikePayload returns true when s matches one of the heuristics
// for "this is a chunk of source code or a diff." Cheap byte-level
// checks, no regex.
func looksLikePayload(s string) bool {
	if strings.Contains(s, "@@") && strings.Contains(s, "\n") {
		return true
	}
	if strings.Contains(s, "diff --git") {
		return true
	}
	if strings.Contains(s, "```") {
		return true
	}
	// 3+ consecutive newlines is a prose/code smell — error messages
	// almost never carry this much vertical structure.
	if strings.Contains(s, "\n\n\n") {
		return true
	}
	return false
}

func lenWord(s string) string {
	// Quantize to keep length-side-channel narrow but useful.
	n := len(s)
	switch {
	case n < 500:
		return "~300"
	case n < 1000:
		return "~750"
	case n < 5000:
		return "~2500"
	case n < 20000:
		return "~10000"
	}
	return ">20000"
}

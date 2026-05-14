package obsstdout

import (
	"strings"
	"testing"
)

func TestScrubString_PassesThroughShortPlainText(t *testing.T) {
	got := ScrubString("review failed for pr 42", 300)
	if got != "review failed for pr 42" {
		t.Errorf("plain short string was scrubbed: %q", got)
	}
}

func TestScrubString_TruncatesOversize(t *testing.T) {
	in := strings.Repeat("a", 800)
	got := ScrubString(in, 300)
	if !strings.HasPrefix(got, "<scrubbed:") {
		t.Errorf("expected scrubbed marker, got %q", got)
	}
}

func TestScrubString_RedactsDiff(t *testing.T) {
	in := "@@ -1,3 +1,4 @@\nfunc foo() {\n+\treturn nil\n}\n"
	got := ScrubString(in, 1000)
	if !strings.Contains(got, "scrubbed") {
		t.Errorf("expected diff to be scrubbed: %q", got)
	}
}

func TestScrubString_RedactsGitDiffHeader(t *testing.T) {
	in := "diff --git a/foo.go b/foo.go"
	got := ScrubString(in, 1000)
	if !strings.Contains(got, "scrubbed") {
		t.Errorf("expected git diff header to be scrubbed: %q", got)
	}
}

func TestScrubString_RedactsCodeFence(t *testing.T) {
	in := "see this snippet:\n```go\nfor i := range xs { ... }\n```"
	got := ScrubString(in, 1000)
	if !strings.Contains(got, "scrubbed") {
		t.Errorf("expected code-fence to be scrubbed: %q", got)
	}
}

func TestScrubString_RedactsBigVerticalProse(t *testing.T) {
	in := "line\n\n\nanother"
	got := ScrubString(in, 1000)
	if !strings.Contains(got, "scrubbed") {
		t.Errorf("expected 3+ newlines to be scrubbed: %q", got)
	}
}

func TestScrubString_NormalErrorPasses(t *testing.T) {
	in := "context deadline exceeded; retried 3 times"
	got := ScrubString(in, 300)
	if got != in {
		t.Errorf("normal error was scrubbed: %q", got)
	}
}

// Logger-level test: the scrubber should fire on values only, never keys.
func TestScrubbingLogger_KeysUntouched(t *testing.T) {
	captured := []any{}
	mock := &captureLogger{kv: &captured}
	wrapper := NewScrubbingLogger(mock, 50)
	wrapper.Info("hi", "key_that_is_long_enough_that_it_would_trip_the_length_check", "shortval")
	if len(captured) != 2 {
		t.Fatalf("expected 2 kv entries, got %d", len(captured))
	}
	// First entry is the key, second is the value.
	if captured[0].(string) != "key_that_is_long_enough_that_it_would_trip_the_length_check" {
		t.Errorf("key was scrubbed: %v", captured[0])
	}
	if captured[1].(string) != "shortval" {
		t.Errorf("value modified: %v", captured[1])
	}
}

type captureLogger struct{ kv *[]any }

func (c *captureLogger) Info(_ string, kv ...any)  { *c.kv = append(*c.kv, kv...) }
func (c *captureLogger) Warn(_ string, kv ...any)  { *c.kv = append(*c.kv, kv...) }
func (c *captureLogger) Error(_ string, kv ...any) { *c.kv = append(*c.kv, kv...) }

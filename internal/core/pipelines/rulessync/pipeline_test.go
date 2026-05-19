package rulessync

import (
	"strings"
	"testing"
)

func TestParseRule_HappyPath(t *testing.T) {
	src := "---\nscope: \"path:**/*.go\"\ncategory: security\nseverity: high\ntitle: No raw SQL\n---\n\nUse the query builder, never string-concat user input into SQL.\n"
	fm, body, err := parseRule([]byte(src))
	if err != nil {
		t.Fatalf("parseRule error: %v", err)
	}
	if fm.Title != "No raw SQL" {
		t.Errorf("title: got %q", fm.Title)
	}
	if fm.Scope != "path:**/*.go" {
		t.Errorf("scope: got %q", fm.Scope)
	}
	if !strings.HasPrefix(body, "Use the query builder") {
		t.Errorf("body: got %q", body)
	}
}

func TestParseRule_CRLF(t *testing.T) {
	// Windows-edited files often use CRLF; the parser must accept them.
	src := "---\r\ntitle: Foo\r\n---\r\n\r\nbody text\r\n"
	fm, body, err := parseRule([]byte(src))
	if err != nil {
		t.Fatalf("parseRule error: %v", err)
	}
	if fm.Title != "Foo" {
		t.Errorf("title: got %q", fm.Title)
	}
	if !strings.Contains(body, "body text") {
		t.Errorf("body: got %q", body)
	}
}

func TestParseRule_BOMStripped(t *testing.T) {
	src := []byte("\xEF\xBB\xBF---\ntitle: Foo\n---\n\nbody\n")
	fm, _, err := parseRule(src)
	if err != nil {
		t.Fatalf("parseRule error: %v", err)
	}
	if fm.Title != "Foo" {
		t.Errorf("title: got %q", fm.Title)
	}
}

func TestParseRule_MissingFrontmatter(t *testing.T) {
	_, _, err := parseRule([]byte("title: nope\n\nbody\n"))
	if err == nil {
		t.Fatal("expected error for missing frontmatter")
	}
}

func TestParseRule_MissingTitle(t *testing.T) {
	src := "---\nscope: \"path:**/*.go\"\n---\n\nbody\n"
	_, _, err := parseRule([]byte(src))
	if err == nil {
		t.Fatal("expected error for missing title")
	}
}

func TestParseRule_UnterminatedFrontmatter(t *testing.T) {
	src := "---\ntitle: Foo\n\nbody without terminator\n"
	_, _, err := parseRule([]byte(src))
	if err == nil {
		t.Fatal("expected error for unterminated frontmatter")
	}
}

func TestContentHash_Deterministic(t *testing.T) {
	a := contentHash("hello world")
	b := contentHash("hello world")
	if a != b {
		t.Errorf("hash should be deterministic; got %q vs %q", a, b)
	}
	if contentHash("a") == contentHash("b") {
		t.Errorf("hash should differ for different inputs")
	}
}

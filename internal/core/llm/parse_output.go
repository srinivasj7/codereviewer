// Package llm holds LLM-side core logic: output parsing and retry/fallback
// orchestration. The LLM adapter implementation lives in internal/adapters.
package llm

import (
	"encoding/json"
	"fmt"
	"strings"

	"codereviewer/internal/schemas"
)

var validCategories = map[string]struct{}{
	"bug": {}, "security": {}, "style": {}, "suggestion": {}, "question": {},
}

var validSeverities = map[string]struct{}{
	"high": {}, "medium": {}, "low": {},
}

// ParseOutput parses and validates the LLM's JSON-array response per
// Appendix A. Markdown fences are tolerated; everything else must match
// the schema or the call returns an error and the run fails open.
func ParseOutput(raw string) (schemas.LlmOutput, error) {
	trimmed := stripFences(strings.TrimSpace(raw))
	var comments schemas.LlmOutput
	if err := json.Unmarshal([]byte(trimmed), &comments); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	for i := range comments {
		if err := validateComment(comments[i]); err != nil {
			return nil, fmt.Errorf("comment %d: %w", i, err)
		}
	}
	return comments, nil
}

func validateComment(c schemas.RawComment) error {
	if strings.TrimSpace(c.File) == "" {
		return fmt.Errorf("missing file")
	}
	if c.StartLine < 1 {
		return fmt.Errorf("invalid start_line %d", c.StartLine)
	}
	if c.EndLine < c.StartLine {
		return fmt.Errorf("end_line %d < start_line %d", c.EndLine, c.StartLine)
	}
	if strings.TrimSpace(c.Comment) == "" {
		return fmt.Errorf("missing comment text")
	}
	if _, ok := validCategories[c.Category]; !ok {
		return fmt.Errorf("invalid category %q", c.Category)
	}
	if _, ok := validSeverities[c.Severity]; !ok {
		return fmt.Errorf("invalid severity %q", c.Severity)
	}
	return nil
}

func stripFences(s string) string {
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

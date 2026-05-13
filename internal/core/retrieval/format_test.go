package retrieval

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"codereviewer/internal/ports/store"
)

func TestFormatCode_RendersLocatorHeader(t *testing.T) {
	hits := []store.CodeChunkHit{{
		FilePath:   "src/handler.ts",
		SymbolName: "handle",
		StartLine:  10,
		EndLine:    20,
		Content:    "function handle() { return 1; }",
	}}
	out := FormatCode(hits)
	require.Len(t, out, 1)
	assert.Contains(t, out[0], "src/handler.ts:10-20")
	assert.Contains(t, out[0], "(handle)")
	assert.Contains(t, out[0], "function handle()")
}

func TestFormatCode_LocatorWithoutSymbol(t *testing.T) {
	hits := []store.CodeChunkHit{{
		FilePath:  "src/handler.ts",
		StartLine: 1,
		EndLine:   5,
		Content:   "block content",
	}}
	out := FormatCode(hits)
	require.Len(t, out, 1)
	assert.Contains(t, out[0], "src/handler.ts:1-5")
	assert.NotContains(t, out[0], "()")
}

func TestFormatComments_AnnotatesWithOutcome(t *testing.T) {
	hits := []store.CommentHit{
		{FilePath: "a.ts", CommentText: "ship it", Outcome: store.OutcomeAccepted},
		{FilePath: "b.ts", CommentText: "nit", Outcome: store.OutcomeDismissed},
		{FilePath: "c.ts", CommentText: "?", Outcome: ""},
	}
	out := FormatComments(hits)
	require.Len(t, out, 3)
	assert.Contains(t, out[0], "[ACCEPTED]")
	assert.Contains(t, out[1], "[DISMISSED]")
	assert.Contains(t, out[2], "[PENDING]", "empty outcome should render as PENDING")
}

func TestFormatRules_TitleThenDescription(t *testing.T) {
	rules := []store.Rule{{
		Title:       "SQL migrations need down",
		Description: "Every file under migrations/ must export a down function.",
	}}
	out := FormatRules(rules)
	require.Len(t, out, 1)
	assert.Contains(t, out[0], "SQL migrations need down\n")
	assert.Contains(t, out[0], "must export a down function")
}

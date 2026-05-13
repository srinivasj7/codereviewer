package llm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseOutput_Valid(t *testing.T) {
	raw := `[{"file":"a.ts","start_line":1,"end_line":2,"comment":"x","category":"bug","severity":"high"}]`
	out, err := ParseOutput(raw)
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, "bug", out[0].Category)
	assert.Equal(t, "high", out[0].Severity)
}

func TestParseOutput_StripsFences(t *testing.T) {
	raw := "```json\n[{\"file\":\"a.ts\",\"start_line\":1,\"end_line\":2,\"comment\":\"x\",\"category\":\"bug\",\"severity\":\"high\"}]\n```"
	out, err := ParseOutput(raw)
	require.NoError(t, err)
	require.Len(t, out, 1)
}

func TestParseOutput_EmptyArray(t *testing.T) {
	out, err := ParseOutput("[]")
	require.NoError(t, err)
	assert.Empty(t, out)
}

func TestParseOutput_RejectsInvalidCategory(t *testing.T) {
	raw := `[{"file":"a.ts","start_line":1,"end_line":2,"comment":"x","category":"made-up","severity":"high"}]`
	_, err := ParseOutput(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid category")
}

func TestParseOutput_RejectsInvalidSeverity(t *testing.T) {
	raw := `[{"file":"a.ts","start_line":1,"end_line":2,"comment":"x","category":"bug","severity":"urgent"}]`
	_, err := ParseOutput(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid severity")
}

func TestParseOutput_RejectsInvertedLineRange(t *testing.T) {
	raw := `[{"file":"a.ts","start_line":5,"end_line":3,"comment":"x","category":"bug","severity":"high"}]`
	_, err := ParseOutput(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "end_line")
}

func TestParseOutput_RejectsMissingFile(t *testing.T) {
	raw := `[{"file":"","start_line":1,"end_line":1,"comment":"x","category":"bug","severity":"high"}]`
	_, err := ParseOutput(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing file")
}

func TestParseOutput_RejectsMalformedJSON(t *testing.T) {
	_, err := ParseOutput(`not json`)
	require.Error(t, err)
}

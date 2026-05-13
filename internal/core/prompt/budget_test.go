package prompt

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lengthEstimator is a deterministic stand-in for tiktoken; one byte = one "token".
// It makes drop-order assertions trivial to reason about.
func lengthEstimator(s string) int { return len(s) }

func TestAssemble_AllFits(t *testing.T) {
	in := Inputs{
		SystemPrompt:       "sys",
		Diff:               "diff",
		RelatedCode:        []string{"code-chunk-1", "code-chunk-2"},
		PastReviews:        []string{"past-review-1"},
		Rules:              []string{"rule-1"},
		ClosingInstruction: "close",
	}
	a := Assemble(in, 1_000_000, lengthEstimator)
	require.False(t, a.DiffOverflow)
	assert.Empty(t, a.Dropped)
	assert.Equal(t, "sys", a.SystemPrompt)
	assert.Contains(t, a.UserPrompt, "diff")
	assert.Contains(t, a.UserPrompt, "code-chunk-1")
	assert.Contains(t, a.UserPrompt, "past-review-1")
	assert.Contains(t, a.UserPrompt, "rule-1")
}

func TestAssemble_DropsPastReviewsFirst(t *testing.T) {
	in := Inputs{
		SystemPrompt:       "s",
		Diff:               "d",
		RelatedCode:        []string{"c"},
		PastReviews:        []string{strings.Repeat("p", 200)},
		Rules:              []string{"r"},
		ClosingInstruction: "x",
	}
	a := Assemble(in, 80, lengthEstimator)
	require.False(t, a.DiffOverflow)
	require.Equal(t, []Section{SectionPastReviews}, a.Dropped)
	assert.NotContains(t, a.UserPrompt, "pp")
	assert.Contains(t, a.UserPrompt, "c")
	assert.Contains(t, a.UserPrompt, "r")
}

func TestAssemble_DropsRelatedCodeSecond(t *testing.T) {
	in := Inputs{
		SystemPrompt:       "s",
		Diff:               "d",
		RelatedCode:        []string{strings.Repeat("c", 200)},
		PastReviews:        []string{strings.Repeat("p", 200)},
		Rules:              []string{"r"},
		ClosingInstruction: "x",
	}
	a := Assemble(in, 80, lengthEstimator)
	require.False(t, a.DiffOverflow)
	assert.Equal(t, []Section{SectionPastReviews, SectionRelatedCode}, a.Dropped)
	assert.Contains(t, a.UserPrompt, "r")
}

func TestAssemble_DropsRulesLast(t *testing.T) {
	in := Inputs{
		SystemPrompt:       "s",
		Diff:               "d",
		RelatedCode:        []string{strings.Repeat("c", 200)},
		PastReviews:        []string{strings.Repeat("p", 200)},
		Rules:              []string{strings.Repeat("r", 200)},
		ClosingInstruction: "x",
	}
	a := Assemble(in, 60, lengthEstimator)
	require.False(t, a.DiffOverflow)
	assert.Equal(t, []Section{SectionPastReviews, SectionRelatedCode, SectionRules}, a.Dropped)
	assert.Contains(t, a.UserPrompt, "d")
	assert.NotContains(t, a.UserPrompt, "[RELATED CODE]")
	assert.NotContains(t, a.UserPrompt, "[APPLICABLE RULES]")
}

func TestAssemble_DiffNeverTrimmed_EvenIfOverflow(t *testing.T) {
	bigDiff := strings.Repeat("d", 500)
	in := Inputs{
		SystemPrompt:       "s",
		Diff:               bigDiff,
		RelatedCode:        []string{"c"},
		PastReviews:        []string{"p"},
		Rules:              []string{"r"},
		ClosingInstruction: "x",
	}
	a := Assemble(in, 50, lengthEstimator)
	assert.True(t, a.DiffOverflow, "expected DiffOverflow when diff alone exceeds cap")
	assert.Contains(t, a.UserPrompt, bigDiff, "diff content must survive even when overflow flagged")
	assert.Equal(t, []Section{SectionPastReviews, SectionRelatedCode, SectionRules}, a.Dropped)
}

func TestSection_String(t *testing.T) {
	assert.Equal(t, "past_reviews", SectionPastReviews.String())
	assert.Equal(t, "related_code", SectionRelatedCode.String())
	assert.Equal(t, "rules", SectionRules.String())
}

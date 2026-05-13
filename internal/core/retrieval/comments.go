package retrieval

import (
	"context"
	"fmt"
	"strings"

	"codereviewer/internal/ports"
	"codereviewer/internal/ports/store"
)

// DefaultCommentK is the design's "top-K = 8" default.
const DefaultCommentK = 8

// RetrieveComments searches review_comments by cosine similarity. The
// store implementation re-ranks by outcome (accepted boosted, dismissed
// penalized) per design §6.1 step 6.
func RetrieveComments(
	ctx context.Context,
	s store.CommentStore,
	repoId ports.RepoId,
	queryEmbedding []float32,
	k int,
) ([]store.CommentHit, error) {
	if s == nil || len(queryEmbedding) == 0 {
		return nil, nil
	}
	if k <= 0 {
		k = DefaultCommentK
	}
	return s.SearchByEmbedding(ctx, store.SearchComments{
		RepoId:    repoId,
		Embedding: queryEmbedding,
		K:         k,
	})
}

// FormatComments renders past comments as prompt strings annotated with
// outcome ([ACCEPTED] / [DISMISSED] / [DISCUSSED] / [PENDING]) so the
// LLM can weight them appropriately.
func FormatComments(hits []store.CommentHit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		tag := strings.ToUpper(string(h.Outcome))
		if tag == "" {
			tag = "PENDING"
		}
		header := fmt.Sprintf("[%s] %s", tag, h.FilePath)
		out = append(out, header+"\n"+h.CommentText)
	}
	return out
}

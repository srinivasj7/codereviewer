// Package retrieval orchestrates vector retrieval over the store ports.
// Each function is independent so the caller (the review pipeline) can
// embed the query once and share it across the code- and comment-
// retrieval calls in the same pass.
package retrieval

import (
	"context"
	"fmt"

	"codereviewer/internal/ports"
	"codereviewer/internal/ports/store"
)

// DefaultCodeK is the design's "top-K = 15" default.
const DefaultCodeK = 15

// RetrieveCode searches code_chunks by cosine similarity to the query
// embedding. sameFile is optional; when set, chunks from that path are
// re-ranked first (per design §6.1 step 5). k <= 0 uses DefaultCodeK.
func RetrieveCode(
	ctx context.Context,
	s store.CodeChunkStore,
	repoId ports.RepoId,
	queryEmbedding []float32,
	sameFile string,
	k int,
) ([]store.CodeChunkHit, error) {
	if s == nil || len(queryEmbedding) == 0 {
		return nil, nil
	}
	if k <= 0 {
		k = DefaultCodeK
	}
	return s.SearchByEmbedding(ctx, store.SearchCodeChunks{
		RepoId:            repoId,
		Embedding:         queryEmbedding,
		K:                 k,
		SameFileBoostPath: sameFile,
	})
}

// FormatCode renders code hits as prompt strings. Each entry carries
// enough locator info for the LLM to cite specific lines.
func FormatCode(hits []store.CodeChunkHit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		header := h.FilePath
		if h.StartLine > 0 {
			header = fmt.Sprintf("%s:%d-%d", h.FilePath, h.StartLine, h.EndLine)
		}
		if h.SymbolName != "" {
			header += " (" + h.SymbolName + ")"
		}
		out = append(out, header+"\n"+h.Content)
	}
	return out
}

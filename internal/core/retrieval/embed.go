package retrieval

import (
	"context"
	"fmt"

	"codereviewer/internal/ports"
	"codereviewer/internal/ports/store"
)

// MaxEmbedTokens caps the input size for embedding calls. text-embedding-3-*
// supports up to 8191 tokens; we leave headroom for the model's BOS/EOS.
const MaxEmbedTokens = 8000

// EmbedQuery embeds the query text, returning a single vector suitable
// for use with RetrieveCode and RetrieveComments. The embedding cache
// is consulted first so repeated reviews of the same head sha pay the
// embedding cost once.
//
// Returns (nil, nil) when llm is nil so the caller can skip retrieval
// gracefully — useful for tests that don't exercise the embedding path.
func EmbedQuery(
	ctx context.Context,
	llm ports.LlmGateway,
	cache store.EmbeddingCache,
	cacheKey string,
	text string,
	model string,
) ([]float32, error) {
	if llm == nil {
		return nil, nil
	}
	if cache != nil && cacheKey != "" {
		hit, err := cache.GetMany(ctx, []string{cacheKey})
		if err == nil {
			if v, ok := hit[cacheKey]; ok && len(v) > 0 {
				return v, nil
			}
		}
	}
	truncated := truncateToTokens(llm, text, model, MaxEmbedTokens)
	results, err := llm.Embed(ctx, []string{truncated}, ports.EmbedOpts{Model: model})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(results) == 0 || len(results[0].Vector) == 0 {
		return nil, fmt.Errorf("embed query: empty result")
	}
	vec := results[0].Vector
	if cache != nil && cacheKey != "" {
		// Best-effort cache write; failure here doesn't fail the call.
		_ = cache.PutMany(ctx, []store.EmbeddingCacheEntry{{Hash: cacheKey, Embedding: vec}})
	}
	return vec, nil
}

// truncateToTokens shortens text to roughly maxTokens, using the LLM's
// own EstimateTokens for accuracy and falling back to a char/4 cap.
func truncateToTokens(llm ports.LlmGateway, text, model string, maxTokens int) string {
	tokens := llm.EstimateTokens(text, model)
	if tokens <= maxTokens {
		return text
	}
	// Approximate: drop down to a char count that fits.
	ratio := float64(maxTokens) / float64(tokens)
	keep := int(float64(len(text)) * ratio)
	if keep < 0 {
		keep = 0
	}
	if keep > len(text) {
		keep = len(text)
	}
	return text[:keep]
}

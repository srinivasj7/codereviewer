package ports

import "context"

// LlmGateway is the LLM port. The pilot adapter targets a LiteLLM sidecar
// speaking the OpenAI wire format; routing/retries/spend tracking happen
// inside LiteLLM. Adapter implementations MUST implement primary/fallback
// tier routing internally and surface the model actually used.
type LlmGateway interface {
	Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
	Embed(ctx context.Context, texts []string, opts EmbedOpts) ([]EmbeddingResult, error)
	EstimateTokens(text, model string) int // tiktoken-based; same model arg as Chat
}

// LlmTier selects the primary or fallback model route.
type LlmTier string

const (
	LlmTierPrimary  LlmTier = "primary"
	LlmTierFallback LlmTier = "fallback"
)

// ChatRequest is a single completion call. SystemPrompt SHOULD be the
// cacheable prefix (stable across calls in the same pipeline) to enable
// prompt-prefix caching at the provider.
type ChatRequest struct {
	Tier            LlmTier
	SystemPrompt    string
	UserPrompt      string
	MaxOutputTokens int
	ResponseFormat  string // "json" | "text"
}

// ChatResponse carries the model's reply and accounting fields.
// TokensIn/Out and CostUsd MUST be populated by the adapter using the
// provider's response metadata (not estimated client-side).
type ChatResponse struct {
	Content   string
	TokensIn  int
	TokensOut int
	CostUsd   float64
	ModelUsed string
}

// EmbedOpts configures embedding calls.
type EmbedOpts struct {
	Model string // empty = adapter default
}

// EmbeddingResult is the embedding for one input.
type EmbeddingResult struct {
	Vector   []float32
	TokensIn int
	Model    string
}

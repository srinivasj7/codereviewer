// Package llmlitellm is the LlmGateway adapter that speaks OpenAI wire
// format to a LiteLLM proxy. The proxy handles routing, retries, and
// upstream credentials; this adapter is intentionally thin.
//
// Cost is computed client-side from a price table so the cost-cap path
// keeps working in slice 1. A slice-2 enhancement is to read
// x-litellm-response-cost from LiteLLM's response headers.
package llmlitellm

import (
	"context"
	"fmt"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"

	"codereviewer/internal/ports"
	"codereviewer/internal/schemas"
)

// Gateway is the LlmGateway adapter.
type Gateway struct {
	client          *openai.Client
	primaryModel    string
	fallbackModel   string
	embeddingsModel string
}

// New constructs a Gateway pointed at the LiteLLM URL in cfg.
func New(cfg schemas.LlmConfig) (*Gateway, error) {
	if cfg.GatewayURL == "" {
		return nil, fmt.Errorf("llm.gateway_url is required for litellm")
	}
	if cfg.PrimaryModelURL == "" {
		return nil, fmt.Errorf("llm.primary_model_url (model name) is required")
	}
	clientCfg := openai.DefaultConfig(cfg.APIKey)
	clientCfg.BaseURL = strings.TrimRight(cfg.GatewayURL, "/") + "/v1"
	return &Gateway{
		client:          openai.NewClientWithConfig(clientCfg),
		primaryModel:    cfg.PrimaryModelURL,
		fallbackModel:   cfg.FallbackModelURL,
		embeddingsModel: cfg.EmbeddingsURL,
	}, nil
}

// Chat sends a single-turn chat completion. Temperature is fixed at 0.1
// to favor deterministic output (review JSON is structural).
func (g *Gateway) Chat(ctx context.Context, req ports.ChatRequest) (ports.ChatResponse, error) {
	model := g.modelForTier(req.Tier)
	if model == "" {
		return ports.ChatResponse{}, fmt.Errorf("no model configured for tier %s", req.Tier)
	}

	ctx2, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	resp, err := g.client.CreateChatCompletion(ctx2, openai.ChatCompletionRequest{
		Model: model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: req.SystemPrompt},
			{Role: openai.ChatMessageRoleUser, Content: req.UserPrompt},
		},
		MaxTokens:   req.MaxOutputTokens,
		Temperature: 0.1,
	})
	if err != nil {
		return ports.ChatResponse{}, fmt.Errorf("chat (%s): %w", model, err)
	}
	if len(resp.Choices) == 0 {
		return ports.ChatResponse{}, fmt.Errorf("chat (%s): no choices returned", model)
	}

	p := priceFor(model)
	cost := float64(resp.Usage.PromptTokens)/1000*p.InputUsdPer1k +
		float64(resp.Usage.CompletionTokens)/1000*p.OutputUsdPer1k

	return ports.ChatResponse{
		Content:   resp.Choices[0].Message.Content,
		TokensIn:  resp.Usage.PromptTokens,
		TokensOut: resp.Usage.CompletionTokens,
		CostUsd:   cost,
		ModelUsed: model,
	}, nil
}

// Embed batches texts into one OpenAI embeddings call. opts.Model
// overrides the default embeddings model from config.
func (g *Gateway) Embed(ctx context.Context, texts []string, opts ports.EmbedOpts) ([]ports.EmbeddingResult, error) {
	model := g.embeddingsModel
	if opts.Model != "" {
		model = opts.Model
	}
	if model == "" {
		return nil, fmt.Errorf("no embeddings model configured")
	}
	if len(texts) == 0 {
		return nil, nil
	}

	ctx2, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	resp, err := g.client.CreateEmbeddings(ctx2, openai.EmbeddingRequest{
		Model: openai.EmbeddingModel(model),
		Input: texts,
	})
	if err != nil {
		return nil, fmt.Errorf("embed (%s): %w", model, err)
	}

	out := make([]ports.EmbeddingResult, len(resp.Data))
	for i, d := range resp.Data {
		out[i] = ports.EmbeddingResult{
			Vector:   d.Embedding,
			TokensIn: 0,
			Model:    model,
		}
	}
	return out, nil
}

// EstimateTokens is a coarse char/4 approximation. Slice 2 plugs in
// tiktoken-go for OpenAI-family models and anthropic-tokenizer for
// Claude-family models, selected by the model arg.
func (g *Gateway) EstimateTokens(text, _ string) int {
	return len(text) / 4
}

func (g *Gateway) modelForTier(tier ports.LlmTier) string {
	if tier == ports.LlmTierFallback {
		if g.fallbackModel != "" {
			return g.fallbackModel
		}
		return g.primaryModel
	}
	return g.primaryModel
}

// modelPricing holds USD-per-1k-token rates for one model.
type modelPricing struct {
	InputUsdPer1k  float64
	OutputUsdPer1k float64
}

// defaultPricing covers the models we expect to route through LiteLLM
// in pilot. Unknown models fall back to a GPT-4o-class estimate.
var defaultPricing = map[string]modelPricing{
	"gpt-4o":                {InputUsdPer1k: 0.005, OutputUsdPer1k: 0.015},
	"gpt-4o-mini":           {InputUsdPer1k: 0.00015, OutputUsdPer1k: 0.0006},
	"claude-3-5-sonnet":     {InputUsdPer1k: 0.003, OutputUsdPer1k: 0.015},
	"claude-3-5-haiku":      {InputUsdPer1k: 0.001, OutputUsdPer1k: 0.005},
	"claude-3-7-sonnet":     {InputUsdPer1k: 0.003, OutputUsdPer1k: 0.015},
	"claude-sonnet-4-5":     {InputUsdPer1k: 0.003, OutputUsdPer1k: 0.015},
	"claude-sonnet-4-6":     {InputUsdPer1k: 0.003, OutputUsdPer1k: 0.015},
	"claude-opus-4-7":       {InputUsdPer1k: 0.015, OutputUsdPer1k: 0.075},
	"claude-haiku-4-5":      {InputUsdPer1k: 0.001, OutputUsdPer1k: 0.005},
}

func priceFor(model string) modelPricing {
	if p, ok := defaultPricing[model]; ok {
		return p
	}
	return modelPricing{InputUsdPer1k: 0.003, OutputUsdPer1k: 0.015}
}

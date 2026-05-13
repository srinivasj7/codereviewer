package fakes

import (
	"context"
	"fmt"
	"sync"

	"codereviewer/internal/ports"
)

// Llm is an in-memory LlmGateway that returns a configurable canned
// response and records every call. EstimateTokens uses len/4.
type Llm struct {
	mu              sync.Mutex
	chatResponse    ports.ChatResponse
	chatErr         error
	embedResponse   [][]float32
	embedErr        error
	chatCalls       []ports.ChatRequest
	embedCalls      []EmbedCall
}

// EmbedCall records one Embed invocation.
type EmbedCall struct {
	Texts []string
	Model string
}

// NewLlm returns a fake with a default empty-array chat response.
func NewLlm() *Llm {
	return &Llm{
		chatResponse: ports.ChatResponse{
			Content:   `[]`,
			TokensIn:  100,
			TokensOut: 50,
			CostUsd:   0.01,
			ModelUsed: "fake-primary",
		},
	}
}

// SetChatResponse sets the response returned by Chat.
func (l *Llm) SetChatResponse(r ports.ChatResponse) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.chatResponse = r
}

// SetChatErr makes Chat return err on every call (overrides SetChatResponse).
func (l *Llm) SetChatErr(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.chatErr = err
}

// ChatCalls returns a snapshot of recorded chat calls.
func (l *Llm) ChatCalls() []ports.ChatRequest {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]ports.ChatRequest, len(l.chatCalls))
	copy(out, l.chatCalls)
	return out
}

// EmbedCalls returns a snapshot of recorded embed calls.
func (l *Llm) EmbedCalls() []EmbedCall {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]EmbedCall, len(l.embedCalls))
	copy(out, l.embedCalls)
	return out
}

// Chat returns the canned response or the configured error.
func (l *Llm) Chat(_ context.Context, req ports.ChatRequest) (ports.ChatResponse, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.chatCalls = append(l.chatCalls, req)
	if l.chatErr != nil {
		return ports.ChatResponse{}, l.chatErr
	}
	return l.chatResponse, nil
}

// Embed returns the canned response (or zero vectors if unset).
func (l *Llm) Embed(_ context.Context, texts []string, opts ports.EmbedOpts) ([]ports.EmbeddingResult, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.embedCalls = append(l.embedCalls, EmbedCall{Texts: texts, Model: opts.Model})
	if l.embedErr != nil {
		return nil, l.embedErr
	}
	if len(l.embedResponse) == 0 {
		out := make([]ports.EmbeddingResult, len(texts))
		for i, t := range texts {
			out[i] = ports.EmbeddingResult{
				Vector:   make([]float32, 1024),
				TokensIn: len(t) / 4,
				Model:    "fake-embed",
			}
		}
		return out, nil
	}
	if len(l.embedResponse) != len(texts) {
		return nil, fmt.Errorf("fake llm: embedResponse length mismatch (have %d, want %d)", len(l.embedResponse), len(texts))
	}
	out := make([]ports.EmbeddingResult, len(texts))
	for i, t := range texts {
		out[i] = ports.EmbeddingResult{
			Vector:   l.embedResponse[i],
			TokensIn: len(t) / 4,
			Model:    "fake-embed",
		}
	}
	return out, nil
}

// EstimateTokens approximates token count via len/4.
func (l *Llm) EstimateTokens(text, _ string) int {
	return len(text) / 4
}

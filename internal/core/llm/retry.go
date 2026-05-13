package llm

import (
	"context"
	"errors"
	"fmt"
	"time"

	"codereviewer/internal/ports"
)

// RetryPolicy configures the primary→fallback escalation per design §8.
type RetryPolicy struct {
	MaxAttemptsPrimary  int
	MaxAttemptsFallback int
	Backoff             []time.Duration
}

// DefaultRetryPolicy matches design §8: 3× exponential backoff on
// primary (1s, 2s, 4s), then 3× on fallback with the same backoff.
var DefaultRetryPolicy = RetryPolicy{
	MaxAttemptsPrimary:  3,
	MaxAttemptsFallback: 3,
	Backoff:             []time.Duration{time.Second, 2 * time.Second, 4 * time.Second},
}

// ChatFn is the bound Chat method of an LlmGateway, captured for retry.
type ChatFn func(ctx context.Context, req ports.ChatRequest) (ports.ChatResponse, error)

// ChatWithRetry tries primary tier with backoff, then escalates to
// fallback. Returns the first successful response; otherwise wraps both
// terminal errors.
func ChatWithRetry(ctx context.Context, fn ChatFn, req ports.ChatRequest, policy RetryPolicy) (ports.ChatResponse, error) {
	primaryErr := errors.New("primary not attempted")
	if policy.MaxAttemptsPrimary > 0 {
		resp, err := tryTier(ctx, fn, req, ports.LlmTierPrimary, policy.MaxAttemptsPrimary, policy.Backoff)
		if err == nil {
			return resp, nil
		}
		primaryErr = err
	}
	if policy.MaxAttemptsFallback <= 0 {
		return ports.ChatResponse{}, fmt.Errorf("LLM call failed: %w", primaryErr)
	}
	resp, err := tryTier(ctx, fn, req, ports.LlmTierFallback, policy.MaxAttemptsFallback, policy.Backoff)
	if err == nil {
		return resp, nil
	}
	return ports.ChatResponse{}, fmt.Errorf("LLM unavailable: %w", errors.Join(primaryErr, err))
}

func tryTier(ctx context.Context, fn ChatFn, req ports.ChatRequest, tier ports.LlmTier, maxAttempts int, backoff []time.Duration) (ports.ChatResponse, error) {
	req.Tier = tier
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		resp, err := fn(ctx, req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if attempt >= maxAttempts-1 {
			break
		}
		wait := backoffFor(backoff, attempt)
		if wait <= 0 {
			continue
		}
		select {
		case <-ctx.Done():
			return ports.ChatResponse{}, ctx.Err()
		case <-time.After(wait):
		}
	}
	return ports.ChatResponse{}, fmt.Errorf("%s tier exhausted after %d attempts: %w", tier, maxAttempts, lastErr)
}

func backoffFor(backoff []time.Duration, attempt int) time.Duration {
	if len(backoff) == 0 {
		return 0
	}
	if attempt < len(backoff) {
		return backoff[attempt]
	}
	return backoff[len(backoff)-1]
}

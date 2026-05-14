// Package contextadhoc is a ContextProvider that returns operator-
// attached context for a specific PR. Storage is in pr_context_items
// (managed via ContextStore); this provider is read-only.
//
// Items can be attached via the `/context` slash command on a PR or
// via the admin UI form. Both write into the same table; this
// provider reads them at review time.
package contextadhoc

import (
	"context"
	"strings"

	"codereviewer/internal/ports"
	"codereviewer/internal/ports/store"
)

// Provider implements ports.ContextProvider.
type Provider struct {
	store   store.ContextStore
	maxN    int
	obs     ports.Obs
}

// New constructs a Provider. maxItems caps how many context items are
// surfaced per review — operators can spam /context, but the prompt
// stays bounded. Set to 0 for the default (10).
func New(store store.ContextStore, maxItems int, obs ports.Obs) *Provider {
	if maxItems <= 0 {
		maxItems = 10
	}
	return &Provider{store: store, maxN: maxItems, obs: obs}
}

// Name implements ports.ContextProvider.
func (p *Provider) Name() string { return "ad-hoc" }

// Fetch implements ports.ContextProvider. Items are sorted newest-first
// by the store; we cap at maxN.
func (p *Provider) Fetch(ctx context.Context, ref ports.PrRef) ([]ports.ContextItem, error) {
	if p.store == nil {
		return nil, nil
	}
	items, err := p.store.ListPrContext(ctx, ref)
	if err != nil {
		p.obs.Logger.Warn("ad-hoc: list failed", "err", err.Error())
		return nil, nil
	}
	if len(items) > p.maxN {
		items = items[:p.maxN]
	}
	out := make([]ports.ContextItem, 0, len(items))
	for _, it := range items {
		body := strings.TrimSpace(it.Body)
		if body == "" {
			continue
		}
		title := it.Title
		if title == "" {
			title = "Operator note (" + it.Source + ")"
		}
		out = append(out, ports.ContextItem{
			Source:   p.Name(),
			Title:    title,
			Body:     body,
			Priority: 40, // below repo instructions; above retrieval
		})
	}
	return out, nil
}

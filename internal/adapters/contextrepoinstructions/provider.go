// Package contextrepoinstructions is a ContextProvider that returns
// repository-level review instructions. It checks two sources, in
// order of precedence:
//
//  1. `.codereviewer.md` at the PR's head sha. If present and
//     non-empty, this overrides everything else — repo owners can
//     self-service their own conventions.
//  2. The InstructionSet assigned to the repo via the admin UI
//     (table repo_instruction_sets joined to instruction_sets).
//
// Returning no items + nil error is the success path when neither
// source is configured. Upstream failures (file fetch error, DB error)
// also collapse to "no items" so a flaky source can't fail the review.
package contextrepoinstructions

import (
	"context"
	"strings"

	"codereviewer/internal/ports"
	"codereviewer/internal/ports/store"
)

// repoFile is the conventional filename. Lives at the repo root; not
// configurable on purpose so contributors always know where to look.
const repoFile = ".codereviewer.md"

// Provider is the ContextProvider implementation.
type Provider struct {
	vcs   ports.VcsSource
	store store.ContextStore
	obs   ports.Obs
}

// New returns a Provider that pulls from the supplied VcsSource and
// ContextStore.
func New(vcs ports.VcsSource, store store.ContextStore, obs ports.Obs) *Provider {
	return &Provider{vcs: vcs, store: store, obs: obs}
}

// Name implements ports.ContextProvider.
func (p *Provider) Name() string { return "repo-instructions" }

// Fetch implements ports.ContextProvider.
func (p *Provider) Fetch(ctx context.Context, ref ports.PrRef) ([]ports.ContextItem, error) {
	if body := p.fetchFile(ctx, ref); body != "" {
		return []ports.ContextItem{{
			Source:   p.Name(),
			Title:    "Repository conventions (from " + repoFile + ")",
			Body:     body,
			Priority: 50, // above ad-hoc, below rules
		}}, nil
	}
	if p.store == nil {
		return nil, nil
	}
	set, found, err := p.store.GetSetForRepo(ctx, ref.RepoId)
	if err != nil {
		p.obs.Logger.Warn("repo-instructions: get set failed",
			"repo_id", string(ref.RepoId), "err", err.Error())
		return nil, nil
	}
	if !found || strings.TrimSpace(set.Body) == "" {
		return nil, nil
	}
	return []ports.ContextItem{{
		Source:   p.Name(),
		Title:    "Repository conventions (" + set.Name + ")",
		Body:     set.Body,
		Priority: 50,
	}}, nil
}

// fetchFile tries to read `.codereviewer.md` at the PR's head sha.
// Returns "" on any failure — providers must never fail loudly.
func (p *Provider) fetchFile(ctx context.Context, ref ports.PrRef) string {
	if p.vcs == nil {
		return ""
	}
	content, err := p.vcs.FetchFileAt(ctx, ref.RepoId, ref.HeadSha, repoFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(content)
}

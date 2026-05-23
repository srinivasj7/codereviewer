// Package contextgithubissues is a ContextProvider that fetches
// referenced GitHub issues (and PR descriptions, which share the
// /issues/:n endpoint shape). Reuses the existing GitHub App auth via
// a separately-constructed go-github client.
//
// References are extracted from the PR's title, branch name, and body:
//   - "#123"          → current repo, issue 123
//   - "owner/repo#45" → that owner/repo, issue 45
//
// Errors and per-issue 404s are absorbed silently.
package contextgithubissues

import (
	"context"

	"github.com/google/go-github/v66/github"

	"codereviewer/internal/adapters/contextissues"
	"codereviewer/internal/ports"
)

// Provider implements ports.ContextProvider for GitHub issues.
type Provider struct {
	client *github.Client
	vcs    ports.VcsRegistry
	obs    ports.Obs
}

// New constructs a Provider. client is a go-github client already
// authenticated against the GitHub App installation; in production
// callers reuse the same client built by vcsgithub.New. The registry
// is resolved per-ref so PRs on other VCSes can still reference public
// GitHub issues — only the PR-meta fetch uses the ref's own adapter.
func New(client *github.Client, vcs ports.VcsRegistry, obs ports.Obs) *Provider {
	return &Provider{client: client, vcs: vcs, obs: obs}
}

// Name implements ports.ContextProvider.
func (p *Provider) Name() string { return "github-issues" }

// Fetch implements ports.ContextProvider.
func (p *Provider) Fetch(ctx context.Context, ref ports.PrRef) ([]ports.ContextItem, error) {
	vcs, err := p.vcs.For(ref.ProviderOrDefault())
	if err != nil {
		p.obs.Logger.Warn("github-issues: vcs registry lookup failed",
			"provider", string(ref.ProviderOrDefault()), "err", err.Error())
		return nil, nil
	}
	meta, err := vcs.FetchPrMeta(ctx, ref)
	if err != nil {
		p.obs.Logger.Warn("github-issues: pr meta failed", "err", err.Error())
		return nil, nil
	}
	refs := contextissues.GithubIssueRefs(meta.Title, meta.BranchName, meta.Body)
	if len(refs) == 0 {
		return nil, nil
	}
	defaultOwner, defaultRepo := splitRepoId(string(ref.RepoId))
	var items []ports.ContextItem
	for _, r := range refs {
		owner, repo, num, ok := contextissues.SplitGithubRef(r)
		if !ok {
			continue
		}
		if owner == "" {
			owner, repo = defaultOwner, defaultRepo
		}
		issue, _, err := p.client.Issues.Get(ctx, owner, repo, num)
		if err != nil {
			// 404 / private / typo — absorb.
			continue
		}
		items = append(items, ports.ContextItem{
			Source:   p.Name(),
			Title:    r + " — " + issue.GetTitle(),
			Body:     issue.GetBody(),
			Priority: 55,
		})
	}
	return items, nil
}

func splitRepoId(id string) (string, string) {
	for i := 0; i < len(id); i++ {
		if id[i] == '/' {
			return id[:i], id[i+1:]
		}
	}
	return id, ""
}

package ports

import "context"

// ContextProvider returns extra prompt-context items for a PR. Examples:
// repo-level review instructions, linked JIRA tickets, or ad-hoc notes
// attached via the admin UI. Implementations are stateless w.r.t. the
// review pipeline — the pipeline holds the provider list and iterates
// at review time.
//
// Providers MUST be safe under partial failure. A provider that fails
// to reach its upstream (JIRA down, repo file missing) should return
// nil items + nil error, not propagate the failure. The pipeline cannot
// distinguish "no items" from "failed silently"; that's intentional —
// the review must still post even if one source is unreachable.
type ContextProvider interface {
	// Name identifies the provider for logs/metrics. Stable across
	// process restarts (e.g. "jira", "github-issues", "repo-instructions").
	Name() string

	// Fetch returns items for the given PR. The implementation decides
	// whether the items are static (repo instructions) or PR-specific
	// (linked issues). The empty slice and nil error are both OK.
	Fetch(ctx context.Context, ref PrRef) ([]ContextItem, error)
}

// ContextItem is one chunk of prompt context. Priority drives drop
// order under token pressure (higher = kept longer). The prompt
// assembler renders each as a labeled section.
//
// Source is used by the assembler to group items (so all JIRA tickets
// appear under one heading) and to attribute in logs. Title is shown
// as the per-item heading; Body is the content.
type ContextItem struct {
	Source   string // "jira" | "github-issues" | "linear" | "repo-instructions" | "ad-hoc"
	Title    string
	Body     string
	Priority int // 0 = lowest; higher items survive longer when budget tightens
}

// Package ports defines all external-dependency contracts for the system.
//
// Every adapter in internal/adapters implements one or more of these
// interfaces. Application code (internal/core) depends on these interfaces,
// never on concrete adapters. The composition root in cmd/<app>/main.go is
// the only place adapter constructors are referenced.
package ports

// TenantId is the multi-tenancy partition key. Single-tenant deployments
// use one fixed value; multi-tenant deployments map customers to tenants.
type TenantId string

// RepoId identifies a repository within a tenant.
type RepoId string

// VcsProvider is the canonical name of a VCS adapter (slice 6B).
// Used to route a PrRef/RepoRef to the right VcsSource in deployments
// that serve multiple providers simultaneously. Empty value treated
// as "github" everywhere for backward compatibility with rows written
// before slice 6B.
type VcsProvider string

const (
	VcsProviderGitHub    VcsProvider = "github"
	VcsProviderBitbucket VcsProvider = "bitbucket"
)

// PrRef is the canonical pull-request locator used across pipelines and
// the bus. The HeadSha is part of the idempotency key — re-runs against
// a new head are distinct jobs.
type PrRef struct {
	TenantId TenantId
	RepoId   RepoId
	PrNumber int
	HeadSha  string
	// Provider routes the ref to the right VcsSource in multi-VCS
	// deployments. Empty value means "github" (the historical default).
	Provider VcsProvider
}

// RepoRef identifies a repo plus the metadata needed to talk to its VCS.
type RepoRef struct {
	TenantId      TenantId
	RepoId        RepoId
	Owner         string
	Name          string
	DefaultBranch string
	Enabled       bool
	// Provider names the VCS adapter that owns this repo. Empty value
	// means "github" — both for in-memory test refs and for rows written
	// before slice 6B's repos.provider column existed.
	Provider VcsProvider
}

// ProviderOrDefault returns the ref's Provider with empty treated as
// github. Use this everywhere a provider key is needed so back-compat
// stays implicit rather than a remember-to-default trap at each site.
func (r PrRef) ProviderOrDefault() VcsProvider {
	if r.Provider == "" {
		return VcsProviderGitHub
	}
	return r.Provider
}

// ProviderOrDefault — same semantics for RepoRef.
func (r RepoRef) ProviderOrDefault() VcsProvider {
	if r.Provider == "" {
		return VcsProviderGitHub
	}
	return r.Provider
}

// Trigger names the reason a pipeline run started. Stored in pr_runs.
type Trigger string

const (
	TriggerPrOpened     Trigger = "pr-opened"
	TriggerPrUpdated    Trigger = "pr-updated"
	TriggerSlashCommand Trigger = "slash-command"
	TriggerManual       Trigger = "manual"
)

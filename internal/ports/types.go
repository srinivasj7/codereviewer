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

// PrRef is the canonical pull-request locator used across pipelines and
// the bus. The HeadSha is part of the idempotency key — re-runs against
// a new head are distinct jobs.
type PrRef struct {
	TenantId TenantId
	RepoId   RepoId
	PrNumber int
	HeadSha  string
}

// RepoRef identifies a repo plus the metadata needed to talk to its VCS.
type RepoRef struct {
	TenantId      TenantId
	RepoId        RepoId
	Owner         string
	Name          string
	DefaultBranch string
}

// Trigger names the reason a pipeline run started. Stored in pr_runs.
type Trigger string

const (
	TriggerPrOpened     Trigger = "pr-opened"
	TriggerPrUpdated    Trigger = "pr-updated"
	TriggerSlashCommand Trigger = "slash-command"
	TriggerManual       Trigger = "manual"
)

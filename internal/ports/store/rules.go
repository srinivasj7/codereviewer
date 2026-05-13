package store

import (
	"context"

	"codereviewer/internal/ports"
)

// RuleStore manages the rules table. UpsertFromRepo is called by the
// rules-sync pipeline on every push to the external rules repo;
// TombstoneMissing soft-disables rules removed in a later sync.
type RuleStore interface {
	UpsertFromRepo(ctx context.Context, sourceCommit string, rules []RuleUpsert) error
	ListForScope(ctx context.Context, repoId ports.RepoId, paths []string) ([]Rule, error)
	TombstoneMissing(ctx context.Context, sourceCommit string, knownIds []RuleId) (int, error)
}

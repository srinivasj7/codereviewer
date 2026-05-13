package retrieval

import (
	"context"

	"codereviewer/internal/ports"
	"codereviewer/internal/ports/store"
)

// RuleRetriever fetches scope-matching rules for the changed files.
type RuleRetriever interface {
	RetrieveForFiles(ctx context.Context, repoId ports.RepoId, paths []string) ([]store.Rule, error)
}

// NoopRuleRetriever returns no rules.
type NoopRuleRetriever struct{}

// RetrieveForFiles always returns empty.
func (NoopRuleRetriever) RetrieveForFiles(_ context.Context, _ ports.RepoId, _ []string) ([]store.Rule, error) {
	return nil, nil
}

package retrieval

import (
	"context"

	"codereviewer/internal/ports"
	"codereviewer/internal/ports/store"
)

// RetrieveRules returns the enabled rules whose scope matches any path.
// Pure store call; no embedding required.
func RetrieveRules(
	ctx context.Context,
	s store.RuleStore,
	repoId ports.RepoId,
	paths []string,
) ([]store.Rule, error) {
	if s == nil {
		return nil, nil
	}
	return s.ListForScope(ctx, repoId, paths)
}

// FormatRules renders rules as prompt strings. Title and body are
// separated by a newline so the LLM can quickly distinguish them.
func FormatRules(rules []store.Rule) []string {
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		out = append(out, r.Title+"\n"+r.Description)
	}
	return out
}

package ports

import "context"

// RulesSource fetches rule files from an external git repository.
// The pilot adapter clones a remote URL; the testing fake returns
// in-memory content.
type RulesSource interface {
	FetchAt(ctx context.Context, gitUrl, ref string) (RulesSnapshot, error)
}

// RulesSnapshot is the result of one fetch. CommitSha is the git provenance
// stored in the rules table for audit.
type RulesSnapshot struct {
	CommitSha string
	Files     []RawRuleFile
}

// RawRuleFile is one rule before parsing. Path is repo-relative.
type RawRuleFile struct {
	Path    string
	Content []byte
}

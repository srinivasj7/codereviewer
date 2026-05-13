// Package rulessync clones the rules repo, parses YAML frontmatter +
// markdown body for each rule file, embeds (title || body), and
// upserts via RuleStore. Rules removed from the repo since the last
// sync are tombstoned (enabled=false) but not deleted, so audit
// queries can still reach them.
package rulessync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"codereviewer/internal/ports"
	"codereviewer/internal/ports/store"
)

// Deps holds the rulessync pipeline's collaborators.
type Deps struct {
	Source         ports.RulesSource
	Llm            ports.LlmGateway
	Obs            ports.Obs
	Rules          store.RuleStore
	EmbeddingCache store.EmbeddingCache
	EmbeddingModel string
	GitUrl         string
	Branch         string
}

// Pipeline is the rules-sync use case.
type Pipeline struct {
	deps Deps
}

// NewPipeline returns a Pipeline ready to Run.
func NewPipeline(deps Deps) *Pipeline { return &Pipeline{deps: deps} }

// Args parametrize one sync invocation.
type Args struct {
	TenantId ports.TenantId
}

// Run executes a full sync. Returns the number of rules upserted.
func (p *Pipeline) Run(ctx context.Context, args Args) (int, error) {
	snap, err := p.deps.Source.FetchAt(ctx, p.deps.GitUrl, p.deps.Branch)
	if err != nil {
		return 0, fmt.Errorf("fetch: %w", err)
	}
	p.deps.Obs.Logger.Info("rules-sync: fetched",
		"git_url", p.deps.GitUrl,
		"branch", p.deps.Branch,
		"commit_sha", snap.CommitSha,
		"files", len(snap.Files),
	)

	rules, embedText := parseFiles(snap.Files, args.TenantId, p.deps.Obs)
	if len(rules) == 0 {
		p.deps.Obs.Logger.Info("rules-sync: no parseable rules")
		return 0, nil
	}

	// Embed via cache so identical titles/bodies across runs don't re-pay.
	if err := p.embed(ctx, rules, embedText); err != nil {
		return 0, err
	}

	if err := p.deps.Rules.UpsertFromRepo(ctx, snap.CommitSha, rules); err != nil {
		return 0, fmt.Errorf("upsert: %w", err)
	}

	knownIds := make([]store.RuleId, len(rules))
	for i, r := range rules {
		knownIds[i] = r.RuleId
	}
	tombstoned, err := p.deps.Rules.TombstoneMissing(ctx, snap.CommitSha, knownIds)
	if err != nil {
		p.deps.Obs.Logger.Warn("rules-sync: tombstone failed", "err", err.Error())
	}

	p.deps.Obs.Logger.Info("rules-sync: done",
		"upserted", len(rules),
		"tombstoned", tombstoned,
	)
	return len(rules), nil
}

func (p *Pipeline) embed(ctx context.Context, rules []store.RuleUpsert, texts []string) error {
	hashes := make([]string, len(rules))
	for i := range rules {
		hashes[i] = contentHash(texts[i])
	}

	cached, err := p.deps.EmbeddingCache.GetMany(ctx, hashes)
	if err != nil {
		return fmt.Errorf("cache lookup: %w", err)
	}

	var todoHashes []string
	var todoTexts []string
	for i, h := range hashes {
		if _, ok := cached[h]; ok {
			continue
		}
		todoHashes = append(todoHashes, h)
		todoTexts = append(todoTexts, texts[i])
	}
	if len(todoTexts) > 0 {
		results, err := p.deps.Llm.Embed(ctx, todoTexts, ports.EmbedOpts{Model: p.deps.EmbeddingModel})
		if err != nil {
			return fmt.Errorf("embed: %w", err)
		}
		if len(results) != len(todoHashes) {
			return fmt.Errorf("embed length mismatch: %d vs %d", len(results), len(todoHashes))
		}
		entries := make([]store.EmbeddingCacheEntry, len(results))
		for i, r := range results {
			entries[i] = store.EmbeddingCacheEntry{Hash: todoHashes[i], Embedding: r.Vector}
			cached[todoHashes[i]] = r.Vector
		}
		if err := p.deps.EmbeddingCache.PutMany(ctx, entries); err != nil {
			p.deps.Obs.Logger.Warn("rules-sync: cache put failed", "err", err.Error())
		}
	}

	for i := range rules {
		rules[i].Embedding = cached[hashes[i]]
	}
	return nil
}

// frontMatter is the YAML block at the top of each rule file.
type frontMatter struct {
	Scope    string `yaml:"scope"`
	Category string `yaml:"category"`
	Severity string `yaml:"severity"`
	Title    string `yaml:"title"`
}

func parseFiles(files []ports.RawRuleFile, tenantId ports.TenantId, obs ports.Obs) ([]store.RuleUpsert, []string) {
	var rules []store.RuleUpsert
	var texts []string
	for _, f := range files {
		fm, body, err := parseRule(f.Content)
		if err != nil {
			obs.Logger.Warn("rules-sync: skipping unparseable file",
				"path", f.Path, "err", err.Error())
			continue
		}
		rules = append(rules, store.RuleUpsert{
			RuleId:      store.RuleId(f.Path),
			TenantId:    tenantId,
			Scope:       fm.Scope,
			Title:       fm.Title,
			Description: body,
		})
		texts = append(texts, fm.Title+"\n"+body)
	}
	return rules, texts
}

// utf8BOMBytes is the byte sequence Windows editors prepend to UTF-8
// files. Declared as bytes (not a string literal) so the Go parser
// doesn't see a literal BOM in this source file.
var utf8BOMBytes = []byte{0xEF, 0xBB, 0xBF}

// parseRule splits a rule file into its YAML frontmatter and body.
// The frontmatter is delimited by `---` lines per the design's
// Appendix B example. A missing or empty title rejects the file.
func parseRule(content []byte) (frontMatter, string, error) {
	content = stripBOM(content)
	s := string(content)
	if !strings.HasPrefix(s, "---\n") && !strings.HasPrefix(s, "---\r\n") {
		return frontMatter{}, "", fmt.Errorf("no frontmatter")
	}
	rest := strings.TrimPrefix(s, "---\n")
	rest = strings.TrimPrefix(rest, "---\r\n")
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return frontMatter{}, "", fmt.Errorf("unterminated frontmatter")
	}
	fmText := rest[:idx]
	body := rest[idx+len("\n---"):]
	body = strings.TrimLeft(body, "\r\n")

	var fm frontMatter
	if err := yaml.Unmarshal([]byte(fmText), &fm); err != nil {
		return fm, "", fmt.Errorf("yaml: %w", err)
	}
	if strings.TrimSpace(fm.Title) == "" {
		return fm, "", fmt.Errorf("title is required")
	}
	return fm, strings.TrimSpace(body), nil
}

func stripBOM(b []byte) []byte {
	if len(b) >= len(utf8BOMBytes) && string(b[:len(utf8BOMBytes)]) == string(utf8BOMBytes) {
		return b[len(utf8BOMBytes):]
	}
	return b
}

func contentHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

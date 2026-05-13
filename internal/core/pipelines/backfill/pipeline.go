// Package backfill ingests historical PR comments into review_comments
// for retrieval (design §6.4). Re-running with a larger window extends
// the history without duplicating rows (CommentStore.Upsert is
// idempotent on github_id); reducing the window is a no-op.
package backfill

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"codereviewer/internal/ports"
	"codereviewer/internal/ports/store"
)

// Deps holds the backfill pipeline's collaborators.
type Deps struct {
	Vcs            ports.VcsSource
	Llm            ports.LlmGateway
	Obs            ports.Obs
	Comments       store.CommentStore
	EmbeddingCache store.EmbeddingCache
	EmbeddingModel string
}

// Pipeline is the historical-comment ingestion use case.
type Pipeline struct {
	deps Deps
}

// NewPipeline returns a Pipeline ready to Run.
func NewPipeline(deps Deps) *Pipeline { return &Pipeline{deps: deps} }

// Args parametrize one backfill invocation.
type Args struct {
	TenantId   ports.TenantId
	RepoId     ports.RepoId
	WindowDays int
	Now        time.Time // pass time.Now() at call site; injected for tests
}

// Run paginates closed PRs in the window, ingests their review comments,
// embeds the text (caching by content hash), and upserts into
// review_comments. Returns the number of comments upserted.
func (p *Pipeline) Run(ctx context.Context, args Args) (int, error) {
	if args.WindowDays <= 0 {
		args.WindowDays = 270
	}
	if args.Now.IsZero() {
		args.Now = time.Now()
	}
	since := args.Now.AddDate(0, 0, -args.WindowDays)

	prNumbers, err := p.deps.Vcs.SearchClosedPrs(ctx, args.RepoId, since)
	if err != nil {
		return 0, fmt.Errorf("search closed prs: %w", err)
	}
	p.deps.Obs.Logger.Info("backfill: starting",
		"repo_id", string(args.RepoId),
		"window_days", args.WindowDays,
		"pr_count", len(prNumbers),
	)

	total := 0
	for _, prNumber := range prNumbers {
		n, err := p.processPr(ctx, args, prNumber)
		if err != nil {
			p.deps.Obs.Logger.Warn("backfill: pr failed; continuing",
				"pr_number", prNumber, "err", err.Error())
			continue
		}
		total += n
	}
	p.deps.Obs.Logger.Info("backfill: done",
		"repo_id", string(args.RepoId),
		"comments_upserted", total,
	)
	return total, nil
}

func (p *Pipeline) processPr(ctx context.Context, args Args, prNumber int) (int, error) {
	ref := ports.PrRef{TenantId: args.TenantId, RepoId: args.RepoId, PrNumber: prNumber}
	comments, err := p.deps.Vcs.ListPrComments(ctx, ref)
	if err != nil {
		return 0, fmt.Errorf("list pr %d: %w", prNumber, err)
	}
	if len(comments) == 0 {
		return 0, nil
	}

	hashes := make([]string, 0, len(comments))
	texts := make([]string, 0, len(comments))
	indexByHash := make(map[string]int, len(comments))
	for i := range comments {
		text := embedText(comments[i])
		if text == "" {
			continue
		}
		hash := contentHash(text)
		if _, dup := indexByHash[hash]; dup {
			continue
		}
		indexByHash[hash] = i
		hashes = append(hashes, hash)
		texts = append(texts, text)
	}

	cached, err := p.deps.EmbeddingCache.GetMany(ctx, hashes)
	if err != nil {
		return 0, fmt.Errorf("embedding cache: %w", err)
	}
	var toEmbedHashes []string
	var toEmbedTexts []string
	for j, h := range hashes {
		if _, ok := cached[h]; ok {
			continue
		}
		toEmbedHashes = append(toEmbedHashes, h)
		toEmbedTexts = append(toEmbedTexts, texts[j])
	}
	if len(toEmbedTexts) > 0 {
		results, err := p.deps.Llm.Embed(ctx, toEmbedTexts, ports.EmbedOpts{Model: p.deps.EmbeddingModel})
		if err != nil {
			return 0, fmt.Errorf("embed pr %d: %w", prNumber, err)
		}
		if len(results) != len(toEmbedHashes) {
			return 0, fmt.Errorf("embed length mismatch: got %d for %d hashes", len(results), len(toEmbedHashes))
		}
		entries := make([]store.EmbeddingCacheEntry, len(results))
		for i, r := range results {
			entries[i] = store.EmbeddingCacheEntry{Hash: toEmbedHashes[i], Embedding: r.Vector}
			cached[toEmbedHashes[i]] = r.Vector
		}
		if err := p.deps.EmbeddingCache.PutMany(ctx, entries); err != nil {
			p.deps.Obs.Logger.Warn("backfill: cache put failed", "err", err.Error())
		}
	}

	upserted := 0
	for i := range comments {
		c := &comments[i]
		text := embedText(*c)
		if text == "" {
			continue
		}
		hash := contentHash(text)
		vec := cached[hash]
		outcome, signal := outcomeFromReactions(c.ReactionsPlusOne, c.ReactionsMinusOne)
		githubId := c.ExternalId
		if _, err := p.deps.Comments.Upsert(ctx, store.CommentUpsert{
			TenantId:      args.TenantId,
			RepoId:        args.RepoId,
			PrNumber:      prNumber,
			Source:        "human",
			GithubId:      &githubId,
			FilePath:      c.Path,
			StartLine:     c.StartLine,
			EndLine:       c.EndLine,
			DiffHunk:      c.DiffHunk,
			CommentText:   c.Body,
			Outcome:       outcome,
			OutcomeSignal: signal,
			Embedding:     vec,
		}); err != nil {
			p.deps.Obs.Logger.Warn("backfill: upsert failed",
				"pr_number", prNumber, "comment_id", c.ExternalId, "err", err.Error())
			continue
		}
		upserted++
	}
	p.deps.Obs.Logger.Info("backfill: pr ingested",
		"pr_number", prNumber,
		"comments_total", len(comments),
		"comments_upserted", upserted,
		"comments_embedded", len(toEmbedTexts),
	)
	return upserted, nil
}

// embedText is the canonical "what we embed for one comment": the body
// concatenated with the diff hunk so retrieval finds comments that
// were made about similar code, not just similar prose.
func embedText(c ports.HumanComment) string {
	if c.Body == "" && c.DiffHunk == "" {
		return ""
	}
	if c.DiffHunk == "" {
		return c.Body
	}
	if c.Body == "" {
		return c.DiffHunk
	}
	return c.Body + "\n\n" + c.DiffHunk
}

// outcomeFromReactions is the slice-3 heuristic. It is intentionally
// coarse: a thumbs-up wins out, a thumbs-down marks dismissal, anything
// else stays pending. The feedback pipeline in slice 4 will overwrite
// these as it processes implicit signals (line-changed-after-comment).
func outcomeFromReactions(plusOne, minusOne int) (store.Outcome, store.OutcomeSignal) {
	switch {
	case plusOne > minusOne:
		return store.OutcomeAccepted, store.SignalThumbsUp
	case minusOne > plusOne:
		return store.OutcomeDismissed, store.SignalThumbsDown
	}
	return store.OutcomePending, ""
}

func contentHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

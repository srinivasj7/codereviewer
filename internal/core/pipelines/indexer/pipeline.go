// Package indexer implements the incremental indexing pipeline (design
// §6.2). On each push to the default branch, the changed files are
// parsed into symbol-bounded chunks, embedded (with content-hash
// deduplication via the embedding cache), and upserted into code_chunks.
package indexer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"codereviewer/internal/ports"
	"codereviewer/internal/ports/store"
	"codereviewer/internal/schemas"
)

// Deps holds the indexer pipeline's collaborators.
type Deps struct {
	Vcs            ports.VcsRegistry
	Llm            ports.LlmGateway
	Parser         ports.ParserRegistry
	Obs            ports.Obs
	CodeChunks     store.CodeChunkStore
	EmbeddingCache store.EmbeddingCache
	EmbeddingModel string
}

// Pipeline indexes default-branch pushes.
type Pipeline struct {
	deps Deps
}

// NewPipeline returns a Pipeline. All Deps fields must be set; the
// pipeline does not allocate any external resources of its own.
func NewPipeline(deps Deps) *Pipeline {
	return &Pipeline{deps: deps}
}

// Handle is the bus consumer entry point.
func (p *Pipeline) Handle(ctx context.Context, payload []byte, cctx ports.ConsumeCtx) error {
	var job schemas.IndexJob
	if err := json.Unmarshal(payload, &job); err != nil {
		_ = cctx.Nack(fmt.Sprintf("invalid index job: %v", err))
		return fmt.Errorf("unmarshal index job: %w", err)
	}
	if err := p.process(ctx, job); err != nil {
		p.deps.Obs.Logger.Error("indexer pipeline failed",
			"err", err.Error(),
			"repo_id", string(job.RepoId),
			"head_sha", job.HeadSha,
		)
	}
	return cctx.Ack()
}

func (p *Pipeline) process(ctx context.Context, job schemas.IndexJob) error {
	provider := job.Provider
	if provider == "" {
		provider = ports.VcsProviderGitHub
	}
	vcs, err := p.deps.Vcs.For(provider)
	if err != nil {
		return fmt.Errorf("vcs registry: %w", err)
	}
	files, err := vcs.ListChangedFiles(ctx, job.RepoId, job.BeforeSha, job.HeadSha)
	if err != nil {
		return fmt.Errorf("list changed files: %w", err)
	}

	chunksByHash, scannedFiles := p.collectChunks(ctx, vcs, job, files)
	if len(chunksByHash) == 0 {
		p.deps.Obs.Logger.Info("indexer: no chunks to upsert",
			"repo_id", string(job.RepoId),
			"head_sha", job.HeadSha,
			"files_scanned", scannedFiles,
		)
		return nil
	}

	hashes := make([]string, 0, len(chunksByHash))
	for h := range chunksByHash {
		hashes = append(hashes, h)
	}

	cached, err := p.deps.EmbeddingCache.GetMany(ctx, hashes)
	if err != nil {
		return fmt.Errorf("embedding cache lookup: %w", err)
	}

	embedHashes, embedTexts := splitForEmbedding(hashes, chunksByHash, cached)
	if len(embedTexts) > 0 {
		results, err := p.deps.Llm.Embed(ctx, embedTexts, ports.EmbedOpts{Model: p.deps.EmbeddingModel})
		if err != nil {
			return fmt.Errorf("embed: %w", err)
		}
		if len(results) != len(embedHashes) {
			return fmt.Errorf("embed: got %d results for %d hashes", len(results), len(embedHashes))
		}
		entries := make([]store.EmbeddingCacheEntry, len(results))
		for i, r := range results {
			entries[i] = store.EmbeddingCacheEntry{Hash: embedHashes[i], Embedding: r.Vector}
			cached[embedHashes[i]] = r.Vector
		}
		if err := p.deps.EmbeddingCache.PutMany(ctx, entries); err != nil {
			p.deps.Obs.Logger.Warn("embedding cache put failed", "err", err.Error())
		}
	}

	upserts := make([]store.CodeChunkUpsert, 0, len(chunksByHash))
	for h, group := range chunksByHash {
		vec, ok := cached[h]
		if !ok {
			p.deps.Obs.Logger.Warn("missing embedding after lookup", "hash", h)
			continue
		}
		for i := range group {
			group[i].Embedding = vec
			upserts = append(upserts, group[i])
		}
	}

	if err := p.deps.CodeChunks.UpsertMany(ctx, upserts); err != nil {
		return fmt.Errorf("upsert chunks: %w", err)
	}

	p.deps.Obs.Logger.Info("indexer: upserted chunks",
		"repo_id", string(job.RepoId),
		"head_sha", job.HeadSha,
		"files_scanned", scannedFiles,
		"chunks_upserted", len(upserts),
		"chunks_embedded", len(embedTexts),
		"chunks_cache_hit", len(hashes)-len(embedTexts),
	)
	return nil
}

func (p *Pipeline) collectChunks(ctx context.Context, vcs ports.VcsSource, job schemas.IndexJob, files []ports.ChangedFile) (map[string][]store.CodeChunkUpsert, int) {
	chunksByHash := make(map[string][]store.CodeChunkUpsert)
	scanned := 0
	for _, f := range files {
		if !p.deps.Parser.Supports(f.Path) {
			continue
		}
		if f.Status == "removed" {
			// Slice 4 enhancement: soft-delete chunks for removed files.
			continue
		}
		content, err := vcs.FetchFileAt(ctx, job.RepoId, job.HeadSha, f.Path)
		if err != nil {
			p.deps.Obs.Logger.Warn("indexer: fetch file failed",
				"path", f.Path, "err", err.Error())
			continue
		}
		chunks, err := p.deps.Parser.ParseChunks(f.Path, content)
		if err != nil {
			p.deps.Obs.Logger.Warn("indexer: parse failed",
				"path", f.Path, "err", err.Error())
			continue
		}
		scanned++
		for _, c := range chunks {
			hash := contentHash(c.Content)
			chunksByHash[hash] = append(chunksByHash[hash], store.CodeChunkUpsert{
				TenantId:    job.TenantId,
				RepoId:      job.RepoId,
				FilePath:    f.Path,
				SymbolName:  c.SymbolName,
				SymbolKind:  c.SymbolKind,
				StartLine:   c.StartLine,
				EndLine:     c.EndLine,
				Content:     c.Content,
				ContentHash: hash,
				CommitSha:   job.HeadSha,
			})
		}
	}
	return chunksByHash, scanned
}

func splitForEmbedding(
	hashes []string,
	chunksByHash map[string][]store.CodeChunkUpsert,
	cached map[string][]float32,
) (toEmbedHashes []string, toEmbedTexts []string) {
	for _, h := range hashes {
		if _, ok := cached[h]; ok {
			continue
		}
		group := chunksByHash[h]
		if len(group) == 0 {
			continue
		}
		toEmbedHashes = append(toEmbedHashes, h)
		toEmbedTexts = append(toEmbedTexts, group[0].Content)
	}
	return
}

func contentHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

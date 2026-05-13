// Package storepostgres implements all 7 store sub-ports against a
// single Postgres database via pgx/v5. Hand-written queries; pgvector
// types are exchanged via github.com/pgvector/pgvector-go which
// implements pgx's Scan and Value interfaces.
//
// Connection management: one shared *pgxpool.Pool, constructed via
// NewPool and passed into each store. The Stores struct bundles them
// for the boot composition root.
package storepostgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool opens a pgxpool against the connection URL. Caller is
// responsible for Close at shutdown.
func NewPool(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse postgres url: %w", err)
	}
	// Conservative defaults — production deploys should tune via the URL.
	cfg.MaxConns = 20
	cfg.MinConns = 2
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open postgres pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return pool, nil
}

// Stores bundles the nine store implementations sharing one pool.
type Stores struct {
	Pool           *pgxpool.Pool
	Repos          *RepoStore
	CodeChunks     *CodeChunkStore
	Comments       *CommentStore
	Rules          *RuleStore
	PrRuns         *PrRunStore
	Feedback       *FeedbackStore
	CostCaps       *CostCapStore
	EmbeddingCache *EmbeddingCache
	Settings       *SettingsStore
}

// NewStores constructs all nine stores against a single pool.
func NewStores(pool *pgxpool.Pool) *Stores {
	return &Stores{
		Pool:           pool,
		Repos:          &RepoStore{pool: pool},
		CodeChunks:     &CodeChunkStore{pool: pool},
		Comments:       &CommentStore{pool: pool},
		Rules:          &RuleStore{pool: pool},
		PrRuns:         &PrRunStore{pool: pool},
		Feedback:       &FeedbackStore{pool: pool},
		CostCaps:       &CostCapStore{pool: pool, defaultDailyUsdCap: 5.00, defaultPerPrTokenCap: 30000},
		EmbeddingCache: &EmbeddingCache{pool: pool},
		Settings:       &SettingsStore{pool: pool},
	}
}

// Close releases the pool.
func (s *Stores) Close() { s.Pool.Close() }

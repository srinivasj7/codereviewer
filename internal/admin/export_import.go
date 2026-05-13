package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pelletier/go-toml/v2"

	"codereviewer/internal/config"
	"codereviewer/internal/schemas"
)

// countTable returns SELECT COUNT(*) FROM <table>. Returns 0 on any
// error — the dashboard treats missing counts as informational, not
// blocking.
func countTable(ctx context.Context, anyPool any, table string) int {
	pool, ok := anyPool.(*pgxpool.Pool)
	if !ok {
		return 0
	}
	var n int
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n)
	return n
}

// SnapshotKind tags an export blob's intent. Imports use it to validate
// the uploaded file matches the chosen endpoint.
type SnapshotKind string

const (
	SnapshotConfig SnapshotKind = "config"
	SnapshotData   SnapshotKind = "data"
)

// DataSnapshot is the export payload for the selective DB dump. The
// retrieval-relevant tables (code_chunks, rules, review_comments) are
// the durable payload. tenants and repos are included as parent rows
// so a cold-start import into a fresh database satisfies foreign-key
// constraints before the gateway has a chance to auto-register them.
// pr_runs (audit), caches, and feedback_events are still out of scope.
type DataSnapshot struct {
	Kind       SnapshotKind   `json:"kind"`
	Version    int            `json:"version"`
	ExportedAt time.Time      `json:"exported_at"`
	Tenants    []tenantRow    `json:"tenants"`
	Repos      []repoRow      `json:"repos"`
	CodeChunks []codeChunkRow `json:"code_chunks"`
	Rules      []ruleRow      `json:"rules"`
	Comments   []commentRow   `json:"review_comments"`
}

type tenantRow struct {
	TenantId string `json:"tenant_id"`
	Name     string `json:"name"`
}

type repoRow struct {
	RepoId             string `json:"repo_id"`
	TenantId           string `json:"tenant_id"`
	Owner              string `json:"owner"`
	Name               string `json:"name"`
	DefaultBranch      string `json:"default_branch"`
	IndexedCommitSha   string `json:"indexed_commit_sha,omitempty"`
	BackfillWindowDays int    `json:"backfill_window_days"`
	Enabled            bool   `json:"enabled"`
}

type codeChunkRow struct {
	ChunkId     string    `json:"chunk_id"`
	TenantId    string    `json:"tenant_id"`
	RepoId      string    `json:"repo_id"`
	FilePath    string    `json:"file_path"`
	SymbolName  string    `json:"symbol_name"`
	SymbolKind  string    `json:"symbol_kind"`
	StartLine   int       `json:"start_line"`
	EndLine     int       `json:"end_line"`
	Content     string    `json:"content"`
	ContentHash string    `json:"content_hash"`
	CommitSha   string    `json:"commit_sha"`
	Embedding   []float32 `json:"embedding"`
}

type ruleRow struct {
	RuleId       string    `json:"rule_id"`
	TenantId     string    `json:"tenant_id"`
	Scope        string    `json:"scope"`
	Title        string    `json:"title"`
	Description  string    `json:"description"`
	Enabled      bool      `json:"enabled"`
	SourceCommit string    `json:"source_commit"`
	Embedding    []float32 `json:"embedding"`
}

type commentRow struct {
	CommentId     string    `json:"comment_id"`
	TenantId      string    `json:"tenant_id"`
	RepoId        string    `json:"repo_id"`
	PrNumber      int       `json:"pr_number"`
	Source        string    `json:"source"`
	GithubId      *int64    `json:"github_id,omitempty"`
	FilePath      string    `json:"file_path"`
	StartLine     int       `json:"start_line"`
	EndLine       int       `json:"end_line"`
	DiffHunk      string    `json:"diff_hunk"`
	CommentText   string    `json:"comment_text"`
	Category      string    `json:"category"`
	Outcome       string    `json:"outcome"`
	OutcomeSignal string    `json:"outcome_signal"`
	Embedding     []float32 `json:"embedding,omitempty"`
}

// ExportData reads the included tables and returns a snapshot.
// Order matters for the eventual import: tenants first, then repos
// (FK on tenant_id), then code_chunks/comments/rules.
func ExportData(ctx context.Context, anyPool any) (DataSnapshot, error) {
	pool, ok := anyPool.(*pgxpool.Pool)
	if !ok {
		return DataSnapshot{}, errors.New("export: pool is not a *pgxpool.Pool")
	}
	snap := DataSnapshot{Kind: SnapshotData, Version: 1, ExportedAt: time.Now().UTC()}

	tenants, err := exportTenants(ctx, pool)
	if err != nil {
		return snap, fmt.Errorf("export tenants: %w", err)
	}
	snap.Tenants = tenants

	repos, err := exportRepos(ctx, pool)
	if err != nil {
		return snap, fmt.Errorf("export repos: %w", err)
	}
	snap.Repos = repos

	chunks, err := exportCodeChunks(ctx, pool)
	if err != nil {
		return snap, fmt.Errorf("export code_chunks: %w", err)
	}
	snap.CodeChunks = chunks

	rules, err := exportRules(ctx, pool)
	if err != nil {
		return snap, fmt.Errorf("export rules: %w", err)
	}
	snap.Rules = rules

	comments, err := exportComments(ctx, pool)
	if err != nil {
		return snap, fmt.Errorf("export review_comments: %w", err)
	}
	snap.Comments = comments
	return snap, nil
}

func exportTenants(ctx context.Context, pool *pgxpool.Pool) ([]tenantRow, error) {
	rows, err := pool.Query(ctx, `SELECT tenant_id, name FROM tenants ORDER BY tenant_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []tenantRow
	for rows.Next() {
		var t tenantRow
		if err := rows.Scan(&t.TenantId, &t.Name); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func exportRepos(ctx context.Context, pool *pgxpool.Pool) ([]repoRow, error) {
	rows, err := pool.Query(ctx, `
SELECT repo_id, tenant_id, owner, name, default_branch,
       COALESCE(indexed_commit_sha,''), COALESCE(backfill_window_days, 270),
       COALESCE(enabled, true)
FROM repos ORDER BY repo_id
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []repoRow
	for rows.Next() {
		var r repoRow
		if err := rows.Scan(&r.RepoId, &r.TenantId, &r.Owner, &r.Name, &r.DefaultBranch,
			&r.IndexedCommitSha, &r.BackfillWindowDays, &r.Enabled); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func exportCodeChunks(ctx context.Context, pool *pgxpool.Pool) ([]codeChunkRow, error) {
	rows, err := pool.Query(ctx, `
SELECT chunk_id, tenant_id, repo_id, file_path,
       COALESCE(symbol_name,''), COALESCE(symbol_kind,''),
       start_line, end_line, content, content_hash,
       commit_sha, embedding
FROM code_chunks ORDER BY chunk_id
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []codeChunkRow
	for rows.Next() {
		var c codeChunkRow
		var emb pgvectorScanner
		if err := rows.Scan(&c.ChunkId, &c.TenantId, &c.RepoId, &c.FilePath,
			&c.SymbolName, &c.SymbolKind, &c.StartLine, &c.EndLine,
			&c.Content, &c.ContentHash, &c.CommitSha, &emb); err != nil {
			return nil, err
		}
		c.Embedding = emb.Slice()
		out = append(out, c)
	}
	return out, rows.Err()
}

func exportRules(ctx context.Context, pool *pgxpool.Pool) ([]ruleRow, error) {
	rows, err := pool.Query(ctx, `
SELECT rule_id, tenant_id, COALESCE(scope,''), title, description,
       enabled, COALESCE(source_commit,''), embedding
FROM rules ORDER BY rule_id
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ruleRow
	for rows.Next() {
		var r ruleRow
		var emb pgvectorScanner
		if err := rows.Scan(&r.RuleId, &r.TenantId, &r.Scope, &r.Title, &r.Description,
			&r.Enabled, &r.SourceCommit, &emb); err != nil {
			return nil, err
		}
		r.Embedding = emb.Slice()
		out = append(out, r)
	}
	return out, rows.Err()
}

func exportComments(ctx context.Context, pool *pgxpool.Pool) ([]commentRow, error) {
	rows, err := pool.Query(ctx, `
SELECT comment_id::text, tenant_id, repo_id, pr_number, source, github_id,
       COALESCE(file_path,''), COALESCE(start_line,0), COALESCE(end_line,0),
       COALESCE(diff_hunk,''), comment_text, COALESCE(category,''),
       COALESCE(outcome,'pending'), COALESCE(outcome_signal,''), embedding
FROM review_comments ORDER BY comment_id
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []commentRow
	for rows.Next() {
		var c commentRow
		var emb pgvectorScanner
		if err := rows.Scan(&c.CommentId, &c.TenantId, &c.RepoId, &c.PrNumber, &c.Source, &c.GithubId,
			&c.FilePath, &c.StartLine, &c.EndLine, &c.DiffHunk, &c.CommentText, &c.Category,
			&c.Outcome, &c.OutcomeSignal, &emb); err != nil {
			return nil, err
		}
		c.Embedding = emb.Slice()
		out = append(out, c)
	}
	return out, rows.Err()
}

// ImportData upserts the rows in snap into their respective tables.
// Existing rows are overwritten by primary key; rows not in snap are
// left in place (import is additive, not destructive).
func ImportData(ctx context.Context, anyPool any, snap DataSnapshot) error {
	if snap.Kind != SnapshotData {
		return fmt.Errorf("import: expected kind=%q, got %q", SnapshotData, snap.Kind)
	}
	pool, ok := anyPool.(*pgxpool.Pool)
	if !ok {
		return errors.New("import: pool is not a *pgxpool.Pool")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// tenants and repos must land first so the FKs on code_chunks /
	// review_comments are satisfied. Both are upserts so re-running an
	// import on a partially-populated DB is safe.
	for _, t := range snap.Tenants {
		_, err := tx.Exec(ctx, `
INSERT INTO tenants (tenant_id, name) VALUES ($1, $2)
ON CONFLICT (tenant_id) DO UPDATE SET name = EXCLUDED.name
`, t.TenantId, t.Name)
		if err != nil {
			return fmt.Errorf("upsert tenant %s: %w", t.TenantId, err)
		}
	}

	for _, r := range snap.Repos {
		_, err := tx.Exec(ctx, `
INSERT INTO repos (repo_id, tenant_id, owner, name, default_branch,
                   indexed_commit_sha, backfill_window_days, enabled)
VALUES ($1,$2,$3,$4,$5,NULLIF($6,''),$7,$8)
ON CONFLICT (repo_id) DO UPDATE SET
  default_branch        = EXCLUDED.default_branch,
  indexed_commit_sha    = EXCLUDED.indexed_commit_sha,
  backfill_window_days  = EXCLUDED.backfill_window_days,
  enabled               = EXCLUDED.enabled
`, r.RepoId, r.TenantId, r.Owner, r.Name, r.DefaultBranch,
			r.IndexedCommitSha, r.BackfillWindowDays, r.Enabled)
		if err != nil {
			return fmt.Errorf("upsert repo %s: %w", r.RepoId, err)
		}
	}

	for _, c := range snap.CodeChunks {
		_, err := tx.Exec(ctx, `
INSERT INTO code_chunks (chunk_id, tenant_id, repo_id, file_path, symbol_name, symbol_kind,
  start_line, end_line, content, content_hash, commit_sha, embedding)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT (chunk_id) DO UPDATE SET
  content = EXCLUDED.content,
  content_hash = EXCLUDED.content_hash,
  commit_sha = EXCLUDED.commit_sha,
  embedding = EXCLUDED.embedding
`, c.ChunkId, c.TenantId, c.RepoId, c.FilePath, c.SymbolName, c.SymbolKind,
			c.StartLine, c.EndLine, c.Content, c.ContentHash, c.CommitSha, vectorLiteral(c.Embedding))
		if err != nil {
			return fmt.Errorf("upsert code_chunk %s: %w", c.ChunkId, err)
		}
	}

	for _, r := range snap.Rules {
		_, err := tx.Exec(ctx, `
INSERT INTO rules (rule_id, tenant_id, scope, title, description, enabled, source_commit, embedding)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (rule_id) DO UPDATE SET
  scope = EXCLUDED.scope,
  title = EXCLUDED.title,
  description = EXCLUDED.description,
  enabled = EXCLUDED.enabled,
  source_commit = EXCLUDED.source_commit,
  embedding = EXCLUDED.embedding
`, r.RuleId, r.TenantId, r.Scope, r.Title, r.Description,
			r.Enabled, r.SourceCommit, vectorLiteral(r.Embedding))
		if err != nil {
			return fmt.Errorf("upsert rule %s: %w", r.RuleId, err)
		}
	}

	for _, c := range snap.Comments {
		_, err := tx.Exec(ctx, `
INSERT INTO review_comments (comment_id, tenant_id, repo_id, pr_number, source, github_id,
  file_path, start_line, end_line, diff_hunk, comment_text, category, outcome, outcome_signal, embedding)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
ON CONFLICT (comment_id) DO UPDATE SET
  comment_text = EXCLUDED.comment_text,
  outcome = EXCLUDED.outcome,
  outcome_signal = EXCLUDED.outcome_signal,
  embedding = EXCLUDED.embedding
`, c.CommentId, c.TenantId, c.RepoId, c.PrNumber, c.Source, c.GithubId,
			c.FilePath, c.StartLine, c.EndLine, c.DiffHunk, c.CommentText, c.Category,
			c.Outcome, c.OutcomeSignal, vectorLiteral(c.Embedding))
		if err != nil {
			return fmt.Errorf("upsert comment %s: %w", c.CommentId, err)
		}
	}

	return tx.Commit(ctx)
}

// ExportConfig writes the runtime-tunable settings as TOML. Only keys
// listed in config.OverlayKeys are included; nothing from the
// SecretsProvider is serialized. Bootstrap TOML (postgres URL etc.)
// stays in the operator's source-of-truth file and is intentionally
// out of scope here.
type ConfigSnapshot struct {
	Kind       SnapshotKind      `toml:"kind"`
	Version    int               `toml:"version"`
	ExportedAt time.Time         `toml:"exported_at"`
	Settings   map[string]string `toml:"settings"`
}

func ExportConfig(ctx context.Context, cfg *schemas.Config) (ConfigSnapshot, error) {
	snap := ConfigSnapshot{
		Kind:       SnapshotConfig,
		Version:    1,
		ExportedAt: time.Now().UTC(),
		Settings:   map[string]string{},
	}
	for _, k := range config.OverlayKeys {
		snap.Settings[k] = config.ReadCurrent(cfg, k)
	}
	return snap, nil
}

// ImportConfig writes each (key, value) pair from snap into the
// SettingsStore. Unknown keys are rejected — admins can't smuggle
// arbitrary settings through an imported file.
func ImportConfig(ctx context.Context, snap ConfigSnapshot, settingsStore interface {
	Set(ctx context.Context, key, value, updatedBy string) error
}, updatedBy string) error {
	if snap.Kind != SnapshotConfig {
		return fmt.Errorf("import: expected kind=%q, got %q", SnapshotConfig, snap.Kind)
	}
	for k, v := range snap.Settings {
		if !config.IsOverlayKey(k) {
			return fmt.Errorf("import: unknown overlay key %q", k)
		}
		if err := settingsStore.Set(ctx, k, v, updatedBy); err != nil {
			return fmt.Errorf("set %s: %w", k, err)
		}
	}
	return nil
}

// HTTP handlers.

func (s *Server) handleExportConfig(w http.ResponseWriter, r *http.Request) {
	snap, err := ExportConfig(r.Context(), s.deps.Cfg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/toml")
	w.Header().Set("Content-Disposition",
		`attachment; filename="codereviewer-config-`+timestamp()+`.toml"`)
	enc := toml.NewEncoder(w)
	enc.SetIndentTables(true)
	_ = enc.Encode(snap)
}

func (s *Server) handleExportDb(w http.ResponseWriter, r *http.Request) {
	snap, err := ExportData(r.Context(), s.deps.Pool)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition",
		`attachment; filename="codereviewer-data-`+timestamp()+`.json"`)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(snap)
}

func (s *Server) handleImportConfigPOST(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		s.renderError(w, r, "import", err)
		return
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		s.renderError(w, r, "import", err)
		return
	}
	defer f.Close()
	body, err := io.ReadAll(f)
	if err != nil {
		s.renderError(w, r, "import", err)
		return
	}
	var snap ConfigSnapshot
	if err := toml.Unmarshal(body, &snap); err != nil {
		s.renderError(w, r, "import", fmt.Errorf("parse toml: %w", err))
		return
	}
	sess, _ := readSession(r, s.secret)
	if err := ImportConfig(r.Context(), snap, s.deps.Settings, sess.Subject); err != nil {
		s.renderError(w, r, "import", err)
		return
	}
	// Apply overlay immediately so the dashboard reflects new values.
	_ = config.ApplyOverlay(r.Context(), s.deps.Cfg, s.deps.Settings)
	s.renderOk(w, r, "import", fmt.Sprintf("Imported %d config setting(s).", len(snap.Settings)))
}

func (s *Server) handleImportDbPOST(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(256 << 20); err != nil {
		s.renderError(w, r, "import", err)
		return
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		s.renderError(w, r, "import", err)
		return
	}
	defer f.Close()
	body, err := io.ReadAll(f)
	if err != nil {
		s.renderError(w, r, "import", err)
		return
	}
	var snap DataSnapshot
	if err := json.Unmarshal(body, &snap); err != nil {
		s.renderError(w, r, "import", fmt.Errorf("parse json: %w", err))
		return
	}
	if err := ImportData(r.Context(), s.deps.Pool, snap); err != nil {
		s.renderError(w, r, "import", err)
		return
	}
	s.renderOk(w, r, "import", fmt.Sprintf(
		"Imported %d tenants, %d repos, %d code_chunks, %d rules, %d review_comments.",
		len(snap.Tenants), len(snap.Repos),
		len(snap.CodeChunks), len(snap.Rules), len(snap.Comments)))
}

func timestamp() string {
	return time.Now().UTC().Format("20060102-150405")
}

// pgvectorScanner pulls a pgvector column into []float32 via pgx's
// Scan interface. Implemented locally to avoid leaking pgvector-go
// types beyond the export path.
type pgvectorScanner struct{ values []float32 }

func (p *pgvectorScanner) Slice() []float32 { return p.values }

// Scan accepts the textual pgvector representation, e.g. "[0.1,0.2,...]"
// or a NULL byte slice from pgx for missing values.
func (p *pgvectorScanner) Scan(src any) error {
	if src == nil {
		p.values = nil
		return nil
	}
	var s string
	switch v := src.(type) {
	case []byte:
		s = string(v)
	case string:
		s = v
	default:
		return fmt.Errorf("pgvectorScanner: unsupported source %T", src)
	}
	if len(s) < 2 || s[0] != '[' || s[len(s)-1] != ']' {
		return fmt.Errorf("pgvectorScanner: bad text %q", s)
	}
	inner := s[1 : len(s)-1]
	if inner == "" {
		p.values = nil
		return nil
	}
	parts := splitCommas(inner)
	p.values = make([]float32, len(parts))
	for i, x := range parts {
		f, err := strconv.ParseFloat(x, 32)
		if err != nil {
			return fmt.Errorf("parse float at %d: %w", i, err)
		}
		p.values[i] = float32(f)
	}
	return nil
}

func splitCommas(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

// vectorLiteral encodes []float32 to the textual pgvector form that
// INSERTs accept directly: "[v1,v2,...]". Nil/empty produces "[]" which
// pgvector treats as a zero-length vector.
func vectorLiteral(v []float32) string {
	if len(v) == 0 {
		return "[]"
	}
	// Pre-size based on a reasonable per-element budget (~12 chars).
	buf := make([]byte, 0, len(v)*12+2)
	buf = append(buf, '[')
	for i, x := range v {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = strconv.AppendFloat(buf, float64(x), 'f', -1, 32)
	}
	buf = append(buf, ']')
	return string(buf)
}

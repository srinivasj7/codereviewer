package admin

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"time"

	"codereviewer/internal/ports/store"
	"codereviewer/internal/schemas"
)

// Janitor sweeps retention windows on a configurable interval. It's
// constructed by cmd/admin-ui and run in a goroutine; Stop is via
// canceling the context. The janitor is idempotent — running it more
// often than needed costs only the queries, not correctness.
//
// Each sweep does three things:
//  1. Delete pr_runs, feedback_events, pr_context_items older than the
//     configured days windows.
//  2. Evict embedding_cache down to the row cap (FIFO by created_at).
//  3. Rotate the auto-export directory, keeping the most-recent N files
//     across config + data pairs.
//
// Sweep failures are logged and the loop continues; one flaky table
// must not stall the others.
type Janitor struct {
	PrRuns         store.PrRunStore
	Feedback       store.FeedbackStore
	Context        store.ContextStore
	EmbeddingCache store.EmbeddingCache
	ExportDir      string
	// Static fallback values, used when Live is nil. cmd/admin-ui wires
	// Live to read the live overlay so admin-UI tunes take effect on the
	// next sweep without a restart; tests can supply the fields directly.
	PrRunsDays     int
	FeedbackDays   int
	PrContextDays  int
	CacheMaxRows   int
	ExportMaxFiles int
	Live           func() schemas.RetentionConfig
	Interval       time.Duration
	Obs            obsLogger
}

// retention returns the windows to use for the upcoming sweep. Live
// (when wired) wins over the static fields so admin-UI saves are
// observed on the next tick.
func (j *Janitor) retention() (prRuns, feedback, prContext, cacheMax, exportMax int) {
	if j.Live != nil {
		r := j.Live()
		return r.PrRunsDays, r.FeedbackEventsDays, r.PrContextItemsDays,
			r.EmbeddingCacheMaxRows, r.AutoExportMaxFiles
	}
	return j.PrRunsDays, j.FeedbackDays, j.PrContextDays, j.CacheMaxRows, j.ExportMaxFiles
}

// obsLogger is the subset of ports.Logger the janitor uses. Declared
// locally to keep the import surface small.
type obsLogger interface {
	Info(msg string, kv ...any)
	Warn(msg string, kv ...any)
	Error(msg string, kv ...any)
}

// Run blocks until ctx is canceled. The first sweep fires after Interval
// (not immediately) so a process restart doesn't always include a sweep.
func (j *Janitor) Run(ctx context.Context) {
	if j.Interval <= 0 {
		j.Interval = 6 * time.Hour
	}
	ticker := time.NewTicker(j.Interval)
	defer ticker.Stop()
	j.Obs.Info("janitor started", "interval_hours", j.Interval.Hours())
	for {
		select {
		case <-ctx.Done():
			j.Obs.Info("janitor shutting down")
			return
		case <-ticker.C:
			j.sweep(ctx)
		}
	}
}

func (j *Janitor) sweep(ctx context.Context) {
	now := time.Now()
	prRunsDays, feedbackDays, prContextDays, cacheMaxRows, exportMaxFiles := j.retention()

	if j.PrRuns != nil && prRunsDays > 0 {
		cutoff := now.Add(-time.Duration(prRunsDays) * 24 * time.Hour)
		if n, err := j.PrRuns.DeleteBefore(ctx, cutoff); err != nil {
			j.Obs.Error("janitor: pr_runs sweep failed", "err", err.Error())
		} else if n > 0 {
			j.Obs.Info("janitor: pr_runs swept", "rows_deleted", n, "cutoff", cutoff.Format(time.RFC3339))
		}
	}

	if j.Feedback != nil && feedbackDays > 0 {
		cutoff := now.Add(-time.Duration(feedbackDays) * 24 * time.Hour)
		if n, err := j.Feedback.DeleteBefore(ctx, cutoff); err != nil {
			j.Obs.Error("janitor: feedback sweep failed", "err", err.Error())
		} else if n > 0 {
			j.Obs.Info("janitor: feedback_events swept", "rows_deleted", n, "cutoff", cutoff.Format(time.RFC3339))
		}
	}

	if j.Context != nil && prContextDays > 0 {
		cutoff := now.Add(-time.Duration(prContextDays) * 24 * time.Hour)
		if n, err := j.Context.DeletePrContextBefore(ctx, cutoff); err != nil {
			j.Obs.Error("janitor: pr_context sweep failed", "err", err.Error())
		} else if n > 0 {
			j.Obs.Info("janitor: pr_context_items swept", "rows_deleted", n, "cutoff", cutoff.Format(time.RFC3339))
		}
	}

	if j.EmbeddingCache != nil && cacheMaxRows > 0 {
		if n, err := j.EmbeddingCache.EvictToMax(ctx, cacheMaxRows); err != nil {
			j.Obs.Error("janitor: embedding_cache evict failed", "err", err.Error())
		} else if n > 0 {
			j.Obs.Info("janitor: embedding_cache evicted", "rows_deleted", n, "max_rows", cacheMaxRows)
		}
	}

	if j.ExportDir != "" && exportMaxFiles > 0 {
		if deleted, err := rotateExportFiles(j.ExportDir, exportMaxFiles); err != nil {
			j.Obs.Error("janitor: export rotate failed", "err", err.Error())
		} else if deleted > 0 {
			j.Obs.Info("janitor: export files rotated", "files_deleted", deleted, "max_files", exportMaxFiles)
		}
	}
}

// rotateExportFiles keeps the most-recent maxFiles per kind (config-*
// and data-* are rotated independently so a pair stays together). Files
// are sorted by mtime descending and excess removed.
func rotateExportFiles(dir string, maxFiles int) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	byKind := map[string][]os.DirEntry{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		switch {
		case len(name) >= 7 && name[:7] == "config-":
			byKind["config"] = append(byKind["config"], e)
		case len(name) >= 5 && name[:5] == "data-":
			byKind["data"] = append(byKind["data"], e)
		}
	}
	deleted := 0
	for _, group := range byKind {
		if len(group) <= maxFiles {
			continue
		}
		// Sort by mtime descending; oldest at the tail.
		sort.Slice(group, func(i, j int) bool {
			fi, errI := group[i].Info()
			fj, errJ := group[j].Info()
			if errI != nil || errJ != nil {
				return group[i].Name() > group[j].Name()
			}
			return fi.ModTime().After(fj.ModTime())
		})
		for _, e := range group[maxFiles:] {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				return deleted, err
			}
			deleted++
		}
	}
	return deleted, nil
}

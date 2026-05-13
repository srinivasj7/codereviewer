package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// AutoExporter periodically writes config + data snapshots to disk.
// Construct via NewAutoExporter and start with Run; Run blocks until
// ctx is canceled. If outputDir is empty or ":memory:" the exporter
// returns immediately — useful for ephemeral dev compose runs where
// persisting backups doesn't make sense.
type AutoExporter struct {
	srv       *Server
	outputDir string
	interval  time.Duration
}

// NewAutoExporter returns an exporter that writes into outputDir every
// `interval`. The output directory is created lazily on first export.
func NewAutoExporter(srv *Server, outputDir string, interval time.Duration) *AutoExporter {
	return &AutoExporter{srv: srv, outputDir: outputDir, interval: interval}
}

// Run blocks. Each tick exports config + data into outputDir as
// timestamped files. Failures are logged and the loop continues — one
// flaky export must not crash the admin process.
func (a *AutoExporter) Run(ctx context.Context) {
	if a.outputDir == "" || a.outputDir == ":memory:" {
		a.srv.deps.Obs.Logger.Info("auto-export disabled (no export_dir set)")
		return
	}
	if a.interval <= 0 {
		a.interval = 24 * time.Hour
	}
	// First export shortly after boot so operators see a snapshot
	// quickly instead of waiting `interval` from a cold start.
	timer := time.NewTimer(time.Minute)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			a.srv.deps.Obs.Logger.Info("auto-export shutting down")
			return
		case <-timer.C:
			if err := a.exportOnce(ctx); err != nil {
				a.srv.deps.Obs.Logger.Error("auto-export failed", "err", err.Error())
			}
			timer.Reset(a.interval)
		}
	}
}

func (a *AutoExporter) exportOnce(ctx context.Context) error {
	if err := os.MkdirAll(a.outputDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", a.outputDir, err)
	}
	ts := timestamp()

	cfgPath := filepath.Join(a.outputDir, "config-"+ts+".toml")
	cfgSnap, err := ExportConfig(ctx, a.srv.deps.Cfg)
	if err != nil {
		return fmt.Errorf("config snapshot: %w", err)
	}
	if err := writeTOML(cfgPath, cfgSnap); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	dataPath := filepath.Join(a.outputDir, "data-"+ts+".json")
	dataSnap, err := ExportData(ctx, a.srv.deps.Pool)
	if err != nil {
		return fmt.Errorf("data snapshot: %w", err)
	}
	if err := writeJSON(dataPath, dataSnap); err != nil {
		return fmt.Errorf("write data: %w", err)
	}

	a.srv.deps.Obs.Logger.Info("auto-export written",
		"config", cfgPath,
		"data", dataPath,
		"tenants", len(dataSnap.Tenants),
		"repos", len(dataSnap.Repos),
		"code_chunks", len(dataSnap.CodeChunks),
		"rules", len(dataSnap.Rules),
		"review_comments", len(dataSnap.Comments),
	)
	return nil
}

func writeTOML(path string, v any) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := toml.NewEncoder(f)
	enc.SetIndentTables(true)
	return enc.Encode(v)
}

func writeJSON(path string, v any) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

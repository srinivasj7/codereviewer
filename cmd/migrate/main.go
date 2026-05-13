// migrate runs goose up/status against the configured Postgres database
// using the migrations embedded into the binary. Suitable as a one-shot
// init container in docker-compose; pre-deploy as a step in production.
package main

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"codereviewer/internal/db"
)

func main() {
	url := flag.String("url", "", "Postgres URL (or use POSTGRES_URL env)")
	dir := flag.String("dir", "up", "migration direction: up | status | version")
	flag.Parse()

	target := *url
	if target == "" {
		target = os.Getenv("POSTGRES_URL")
	}
	if target == "" {
		fmt.Fprintln(os.Stderr, "migrate: missing --url or POSTGRES_URL env")
		os.Exit(2)
	}

	if err := run(target, *dir); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
}

func run(url, direction string) error {
	sub, err := fs.Sub(db.Migrations, "migrations")
	if err != nil {
		return fmt.Errorf("locate embedded migrations: %w", err)
	}
	goose.SetBaseFS(sub)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set dialect: %w", err)
	}

	sqlDB, err := goose.OpenDBWithDriver("pgx", url)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer sqlDB.Close()

	ctx := context.Background()
	switch direction {
	case "up":
		return goose.UpContext(ctx, sqlDB, ".")
	case "status":
		return goose.StatusContext(ctx, sqlDB, ".")
	case "version":
		return goose.VersionContext(ctx, sqlDB, ".")
	}
	return fmt.Errorf("unknown direction %q (want up | status | version)", direction)
}

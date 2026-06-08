// Package migrations embeds the SQL migration files and runs them against
// Postgres at startup, so a fresh `docker compose up` yields a ready schema with
// no manual step. The same files can also be driven by the migrate CLI (see Makefile).
package migrations

import (
	"embed"
	"errors"
	"fmt"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // registers the pgx5:// driver
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed *.sql
var files embed.FS

// Run applies all up migrations. It is idempotent — already-applied migrations
// are skipped — so it is safe to call on every boot.
func Run(postgresDSN string) error {
	src, err := iofs.New(files, ".")
	if err != nil {
		return fmt.Errorf("open migration source: %w", err)
	}
	// golang-migrate's pgx/v5 driver is registered under the pgx5:// scheme.
	dbURL := postgresDSN
	for _, p := range []string{"postgres://", "postgresql://"} {
		if strings.HasPrefix(dbURL, p) {
			dbURL = "pgx5://" + strings.TrimPrefix(dbURL, p)
			break
		}
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, dbURL)
	if err != nil {
		return fmt.Errorf("init migrator: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

package store

import (
	"embed"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Migrate runs all pending up migrations against the database at dsn.
// dsn must be a pgx-compatible connection string, e.g.:
// "pgx5://user:pass@host:5432/dbname"
func Migrate(dsn string) error {
	src, err := iofs.New(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("create migration source: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, dsn)
	if err != nil {
		return fmt.Errorf("create migrator: %w", err)
	}
	defer func() {
		srcErr, dbErr := m.Close()
		if srcErr != nil {
			_ = srcErr // source cleanup error; non-fatal for startup migration
		}
		if dbErr != nil {
			_ = dbErr // db cleanup error; non-fatal for startup migration
		}
	}()
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("run migrations: %w", err)
	}
	return nil
}

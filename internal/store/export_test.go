//go:build integration

package store

import (
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// MigrateTo migrates the database at dsn to the given schema version, down or
// up as needed. Test-only: production code only ever migrates up via Migrate.
func MigrateTo(dsn string, version uint) error {
	src, err := iofs.New(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("create migration source: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, dsn)
	if err != nil {
		return fmt.Errorf("create migrator: %w", err)
	}
	defer m.Close()
	return m.Migrate(version)
}

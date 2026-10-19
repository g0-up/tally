package db

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/golang-migrate/migrate/v4"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// ErrNoMigrations is returned when the build carries no migration files.
var ErrNoMigrations = errors.New("no migrations are embedded in this build")

// Migrator applies the embedded *.up.sql / *.down.sql files with golang-migrate. It must connect
// as the schema owner (MIGRATE_DATABASE_URL), never as the application role.
type Migrator struct {
	m *migrate.Migrate
}

// NewMigrator opens url and reads migrations from the root of src.
func NewMigrator(url string, src fs.FS) (*Migrator, error) {
	if src == nil {
		return nil, ErrNoMigrations
	}
	if files, err := fs.Glob(src, "*.sql"); err != nil || len(files) == 0 {
		return nil, ErrNoMigrations
	}
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		return nil, errors.New("migrate: MIGRATE_DATABASE_URL is not a valid PostgreSQL connection string")
	}
	sqlDB := stdlib.OpenDB(*cfg)
	driver, err := pgxmigrate.WithInstance(sqlDB, &pgxmigrate.Config{})
	if err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("migrate: connect: %w", err)
	}
	source, err := iofs.New(src, ".")
	if err != nil {
		_ = driver.Close()
		return nil, fmt.Errorf("migrate: read migrations: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", source, "pgx5", driver)
	if err != nil {
		_ = source.Close()
		_ = driver.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Migrator{m: m}, nil
}

// Up applies every pending migration. Being up to date is not an error.
func (g *Migrator) Up() error {
	if err := g.m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}

// Down rolls back n migrations, or all of them when n <= 0.
func (g *Migrator) Down(n int) error {
	var err error
	if n <= 0 {
		err = g.m.Down()
	} else {
		err = g.m.Steps(-n)
	}
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate down: %w", err)
	}
	return nil
}

// Version reports the applied version (0 when none) and whether the last migration failed half way.
func (g *Migrator) Version() (version uint, dirty bool, err error) {
	version, dirty, err = g.m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("migrate version: %w", err)
	}
	return version, dirty, nil
}

// Force records version as applied and clean without running anything (repair after a failed
// migration was fixed by hand).
func (g *Migrator) Force(version int) error {
	if err := g.m.Force(version); err != nil {
		return fmt.Errorf("migrate force: %w", err)
	}
	return nil
}

// Close releases the connection.
func (g *Migrator) Close() error {
	srcErr, dbErr := g.m.Close()
	return errors.Join(srcErr, dbErr)
}

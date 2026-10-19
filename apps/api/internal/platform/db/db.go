// Package db opens the PostgreSQL pool and is the only place that starts transactions.
// Repositories receive a Querier from WithTenantTx, WithPlatformTx or WithReadOnlyTenantTx and
// never see the pool, so the unit of work and the RLS tenant context are explicit.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
)

// Querier runs statements inside the caller's transaction. sqlx.GetContext / SelectContext accept
// it directly.
type Querier interface {
	sqlx.ExtContext
	sqlx.PreparerContext
}

// Options tunes the pool.
type Options struct {
	// MaxConns caps open connections (default 20).
	MaxConns int
	// StatementTimeout aborts any single statement running longer (default 15 s).
	StatementTimeout time.Duration
	// ApplicationName shows in pg_stat_activity ("tally-api", "tally-worker").
	ApplicationName string
}

// DB is the application connection pool (role tally_app).
type DB struct {
	x *sqlx.DB
}

// Open connects with pgx through database/sql and verifies the connection with a ping.
func Open(ctx context.Context, url string, opts Options) (*DB, error) {
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		// pgx errors may echo the connection string; never include it.
		return nil, errors.New("open database: DATABASE_URL is not a valid PostgreSQL connection string")
	}
	if opts.MaxConns <= 0 {
		opts.MaxConns = 20
	}
	if opts.StatementTimeout <= 0 {
		opts.StatementTimeout = 15 * time.Second
	}
	if cfg.RuntimeParams == nil {
		cfg.RuntimeParams = map[string]string{}
	}
	cfg.RuntimeParams["statement_timeout"] = strconv.FormatInt(opts.StatementTimeout.Milliseconds(), 10)
	if opts.ApplicationName != "" {
		cfg.RuntimeParams["application_name"] = opts.ApplicationName
	}

	sqlDB := stdlib.OpenDB(*cfg)
	sqlDB.SetMaxOpenConns(opts.MaxConns)
	sqlDB.SetMaxIdleConns(opts.MaxConns)
	sqlDB.SetConnMaxIdleTime(5 * time.Minute)
	sqlDB.SetConnMaxLifetime(time.Hour)

	d := &DB{x: sqlx.NewDb(sqlDB, "pgx")}
	if err := d.Ping(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return d, nil
}

// Ping checks that the database answers.
func (d *DB) Ping(ctx context.Context) error {
	if err := d.x.PingContext(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	return nil
}

// Close closes the pool.
func (d *DB) Close() error { return d.x.Close() }

// Stats reports pool statistics.
func (d *DB) Stats() sql.DBStats { return d.x.Stats() }

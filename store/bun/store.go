package bun

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/driver/pgdriver"
	"github.com/uptrace/bun/driver/sqliteshim"

	ctrlplane "github.com/xraph/ctrlplane"
)

// Driver represents the database driver type.
type Driver string

const (
	// DriverPostgreSQL uses PostgreSQL driver.
	DriverPostgreSQL Driver = "postgres"

	// DriverSQLite uses SQLite driver.
	DriverSQLite Driver = "sqlite"
)

// Config holds the configuration for the Bun store.
type Config struct {
	// Driver specifies the database driver (postgres or sqlite).
	Driver Driver `default:"postgres" env:"CP_BUN_DRIVER" json:"driver"`

	// DSN is the database connection string.
	DSN string `env:"CP_BUN_DSN" json:"dsn"`

	// MaxOpenConns is the maximum number of open connections.
	MaxOpenConns int `default:"25" env:"CP_BUN_MAX_OPEN_CONNS" json:"max_open_conns"`

	// MaxIdleConns is the maximum number of idle connections.
	MaxIdleConns int `default:"5" env:"CP_BUN_MAX_IDLE_CONNS" json:"max_idle_conns"`

	// ConnMaxLifetime is the maximum lifetime of a connection.
	ConnMaxLifetime time.Duration `default:"5m" env:"CP_BUN_CONN_MAX_LIFETIME" json:"conn_max_lifetime"`

	// ConnMaxIdleTime is the maximum idle time of a connection.
	ConnMaxIdleTime time.Duration `default:"1m" env:"CP_BUN_CONN_MAX_IDLE_TIME" json:"conn_max_idle_time"`
}

// Store is the Bun SQL implementation of store.Store.
type Store struct {
	db  *bun.DB
	cfg Config
}

// New creates a new Bun store.
func New(cfg Config) (*Store, error) {
	if cfg.DSN == "" {
		return nil, fmt.Errorf("bun: %w: dsn is required", ctrlplane.ErrInvalidConfig)
	}

	var sqldb *sql.DB

	var db *bun.DB

	switch cfg.Driver {
	case DriverPostgreSQL:
		sqldb = sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(cfg.DSN)))
		db = bun.NewDB(sqldb, pgdialect.New())

	case DriverSQLite:
		sqldb, err := sql.Open(sqliteshim.ShimName, cfg.DSN)
		if err != nil {
			return nil, fmt.Errorf("bun: failed to open sqlite: %w", err)
		}

		db = bun.NewDB(sqldb, sqlitedialect.New())

	default:
		return nil, fmt.Errorf("bun: %w: unsupported driver %s", ctrlplane.ErrInvalidConfig, cfg.Driver)
	}

	sqldb.SetMaxOpenConns(cfg.MaxOpenConns)
	sqldb.SetMaxIdleConns(cfg.MaxIdleConns)
	sqldb.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	sqldb.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

	return &Store{
		db:  db,
		cfg: cfg,
	}, nil
}

// Migrate runs all schema migrations.
func (s *Store) Migrate(ctx context.Context) error {
	return runMigrations(ctx, s.db)
}

// Ping checks database connectivity.
func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// now returns the current UTC time.
func now() time.Time {
	return time.Now().UTC()
}

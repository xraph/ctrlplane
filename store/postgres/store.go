package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/xraph/grove"
	"github.com/xraph/grove/drivers/pgdriver"
	"github.com/xraph/grove/migrate"

	"github.com/xraph/ctrlplane/store"
)

// compile-time interface check.
var _ store.Store = (*Store)(nil)

// Store implements store.Store using grove ORM with pgdriver.
type Store struct {
	db *grove.DB
	pg *pgdriver.PgDB
}

// New creates a new PostgreSQL-backed store.
func New(db *grove.DB) *Store {
	return &Store{
		db: db,
		pg: pgdriver.Unwrap(db),
	}
}

// DB returns the underlying grove database for direct access.
func (s *Store) DB() *grove.DB { return s.db }

// Migrate creates the required tables and indexes using the grove orchestrator.
func (s *Store) Migrate(ctx context.Context) error {
	executor, err := migrate.NewExecutorFor(s.pg)
	if err != nil {
		return fmt.Errorf("ctrlplane/postgres: create migration executor: %w", err)
	}

	orch := migrate.NewOrchestrator(executor, Migrations)
	if _, err := orch.Migrate(ctx); err != nil {
		return fmt.Errorf("ctrlplane/postgres: migration failed: %w", err)
	}

	return nil
}

// Ping checks database connectivity.
func (s *Store) Ping(ctx context.Context) error {
	return s.db.Ping(ctx)
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// isNoRows checks for the standard sql.ErrNoRows sentinel.
func isNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}

// now returns the current UTC time.
func now() time.Time {
	return time.Now().UTC()
}

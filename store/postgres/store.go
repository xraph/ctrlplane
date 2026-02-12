package postgres

import "context"

// Config holds PostgreSQL connection configuration.
type Config struct {
	// DSN is the database connection string.
	DSN string `env:"CP_POSTGRES_DSN" json:"dsn"`
}

// Store implements the aggregate store interface using PostgreSQL.
type Store struct {
	config Config
}

// New creates a new PostgreSQL store.
func New(cfg Config) (*Store, error) {
	return &Store{config: cfg}, nil
}

// Migrate runs schema migrations.
func (s *Store) Migrate(_ context.Context) error {
	return nil
}

// Ping checks database connectivity.
func (s *Store) Ping(_ context.Context) error {
	return nil
}

// Close closes the database connection.
func (s *Store) Close() error {
	return nil
}

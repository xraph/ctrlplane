package sqlite

import "context"

// Config holds SQLite configuration.
type Config struct {
	// Path is the database file path.
	Path string `default:"ctrlplane.db" env:"CP_SQLITE_PATH" json:"path"`
}

// Store implements the aggregate store interface using SQLite.
type Store struct {
	config Config
}

// New creates a new SQLite store.
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

package badger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dgraph-io/badger/v4"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
)

// Key prefixes for different entity types.
const (
	prefixInstance       = "inst:"
	prefixDeployment     = "depl:"
	prefixRelease        = "rels:"
	prefixHealthCheck    = "hchk:"
	prefixHealthResult   = "hrsl:"
	prefixMetric         = "metr:"
	prefixLog            = "logs:"
	prefixTrace          = "trac:"
	prefixResourceSnap   = "rsna:"
	prefixDomain         = "doma:"
	prefixRoute          = "rout:"
	prefixCertificate    = "cert:"
	prefixSecret         = "secr:"
	prefixTenant         = "tent:"
	prefixAudit          = "audt:"
	prefixInstanceSlug   = "islg:"
	prefixTenantSlug     = "tslg:"
	prefixReleaseVersion = "rlvr:"
)

// Config holds the configuration for the Badger store.
type Config struct {
	// Path is the directory path where Badger will store data.
	Path string `default:"./data/badger" env:"CP_BADGER_PATH" json:"path"`

	// InMemory when true runs Badger in memory mode (for testing).
	InMemory bool `default:"false" env:"CP_BADGER_IN_MEMORY" json:"in_memory"`

	// SyncWrites enables synchronous writes (slower but safer).
	SyncWrites bool `default:"false" env:"CP_BADGER_SYNC_WRITES" json:"sync_writes"`
}

// Store is the Badger implementation of store.Store.
type Store struct {
	db  *badger.DB
	cfg Config
}

// New creates a new Badger store.
func New(cfg Config) (*Store, error) {
	if cfg.Path == "" && !cfg.InMemory {
		return nil, fmt.Errorf("badger: %w: path is required", ctrlplane.ErrInvalidConfig)
	}

	opts := badger.DefaultOptions(cfg.Path)
	opts.SyncWrites = cfg.SyncWrites

	if cfg.InMemory {
		opts = opts.WithInMemory(true)
	}

	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("badger: failed to open database: %w", err)
	}

	return &Store{
		db:  db,
		cfg: cfg,
	}, nil
}

// Migrate is a no-op for Badger (no schema management needed).
func (s *Store) Migrate(_ context.Context) error {
	return nil
}

// Ping checks if the database is accessible.
func (s *Store) Ping(_ context.Context) error {
	return s.db.View(func(_ *badger.Txn) error {
		return nil
	})
}

// Close closes the Badger database.
func (s *Store) Close() error {
	return s.db.Close()
}

// idStr converts an id.ID to its string representation for keys.
func idStr(i id.ID) string {
	return i.String()
}

// now returns the current UTC time.
func now() time.Time {
	return time.Now().UTC()
}

// set stores a value with the given key using JSON encoding.
func (s *Store) set(txn *badger.Txn, key string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("badger: json marshal failed: %w", err)
	}

	return txn.Set([]byte(key), data)
}

// get retrieves a value by key and decodes it from JSON.
func (s *Store) get(txn *badger.Txn, key string, dest any) error {
	item, err := txn.Get([]byte(key))
	if err != nil {
		if errors.Is(err, badger.ErrKeyNotFound) {
			return ctrlplane.ErrNotFound
		}

		return fmt.Errorf("badger: get failed: %w", err)
	}

	return item.Value(func(val []byte) error {
		if err := json.Unmarshal(val, dest); err != nil {
			return fmt.Errorf("badger: json unmarshal failed: %w", err)
		}

		return nil
	})
}

// delete removes a key from the database.
func (s *Store) delete(txn *badger.Txn, key string) error {
	return txn.Delete([]byte(key))
}

// exists checks if a key exists in the database.
func (s *Store) exists(txn *badger.Txn, key string) (bool, error) {
	_, err := txn.Get([]byte(key))
	if err != nil {
		if errors.Is(err, badger.ErrKeyNotFound) {
			return false, nil
		}

		return false, fmt.Errorf("badger: exists check failed: %w", err)
	}

	return true, nil
}

// iterate iterates over keys with a given prefix.
func (s *Store) iterate(txn *badger.Txn, prefix string, fn func(key string, val []byte) error) error {
	opts := badger.DefaultIteratorOptions
	opts.Prefix = []byte(prefix)

	it := txn.NewIterator(opts)
	defer it.Close()

	for it.Rewind(); it.Valid(); it.Next() {
		item := it.Item()
		key := string(item.Key())

		err := item.Value(func(val []byte) error {
			return fn(key, val)
		})
		if err != nil {
			return fmt.Errorf("badger: iteration failed: %w", err)
		}
	}

	return nil
}

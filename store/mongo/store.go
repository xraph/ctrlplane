package mongo

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/store"
)

// Collection name constants.
const (
	colInstances         = "instances"
	colDeployments       = "deployments"
	colReleases          = "releases"
	colHealthChecks      = "health_checks"
	colHealthResults     = "health_results"
	colMetrics           = "metrics"
	colLogs              = "logs"
	colTraces            = "traces"
	colResourceSnapshots = "resource_snapshots"
	colDomains           = "domains"
	colRoutes            = "routes"
	colCertificates      = "certificates"
	colSecrets           = "secrets"
	colTenants           = "tenants"
	colAuditEntries      = "audit_entries"
)

// Compile-time interface check.
var _ store.Store = (*Store)(nil)

// Config holds the configuration for the MongoDB store.
type Config struct {
	URI         string        `env:"CP_MONGO_URI"  json:"uri"`
	Database    string        `default:"ctrlplane" env:"CP_MONGO_DATABASE"      json:"database"`
	MaxPoolSize uint64        `default:"100"       env:"CP_MONGO_MAX_POOL_SIZE" json:"max_pool_size"`
	MinPoolSize uint64        `default:"10"        env:"CP_MONGO_MIN_POOL_SIZE" json:"min_pool_size"`
	Timeout     time.Duration `default:"10s"       env:"CP_MONGO_TIMEOUT"       json:"timeout"`
}

// Store is a MongoDB-backed implementation of store.Store.
type Store struct {
	client *mongo.Client
	db     *mongo.Database
	cfg    Config
}

// New creates a new MongoDB store and establishes a connection.
func New(cfg Config) (*Store, error) {
	if cfg.URI == "" {
		return nil, fmt.Errorf("mongo: %w: uri is required", ctrlplane.ErrInvalidConfig)
	}

	if cfg.Database == "" {
		cfg.Database = "ctrlplane"
	}

	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}

	opts := options.Client().
		ApplyURI(cfg.URI).
		SetMaxPoolSize(cfg.MaxPoolSize).
		SetMinPoolSize(cfg.MinPoolSize).
		SetTimeout(cfg.Timeout)

	client, err := mongo.Connect(opts)
	if err != nil {
		return nil, fmt.Errorf("mongo: connect: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	if err := client.Ping(ctx, nil); err != nil {
		return nil, fmt.Errorf("mongo: ping: %w", err)
	}

	return &Store{
		client: client,
		db:     client.Database(cfg.Database),
		cfg:    cfg,
	}, nil
}

// Migrate creates collections and indexes.
func (s *Store) Migrate(ctx context.Context) error {
	indexes := migrationIndexes()

	for col, models := range indexes {
		if len(models) == 0 {
			continue
		}

		_, err := s.col(col).Indexes().CreateMany(ctx, models)
		if err != nil {
			return fmt.Errorf("mongo: migrate %s indexes: %w", col, err)
		}
	}

	return nil
}

// Ping checks database connectivity.
func (s *Store) Ping(ctx context.Context) error {
	if err := s.client.Ping(ctx, nil); err != nil {
		return fmt.Errorf("mongo: ping: %w", err)
	}

	return nil
}

// Close disconnects from MongoDB.
func (s *Store) Close() error {
	if err := s.client.Disconnect(context.Background()); err != nil {
		return fmt.Errorf("mongo: close: %w", err)
	}

	return nil
}

// col returns a collection handle.
func (s *Store) col(name string) *mongo.Collection {
	return s.db.Collection(name)
}

// isDuplicateKeyError checks if an error is a MongoDB duplicate key error (code 11000).
func isDuplicateKeyError(err error) bool {
	return mongo.IsDuplicateKeyError(err)
}

// now returns the current UTC time.
func now() time.Time {
	return time.Now().UTC()
}

// idStr returns the string representation of an ID.
func idStr(i id.ID) string {
	return i.String()
}

// migrationIndexes returns the index definitions for all collections.
func migrationIndexes() map[string][]mongo.IndexModel {
	return map[string][]mongo.IndexModel{
		colInstances: {
			{
				Keys:    bson.D{{Key: "tenant_id", Value: 1}, {Key: "slug", Value: 1}},
				Options: options.Index().SetUnique(true),
			},
			{Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "created_at", Value: -1}}},
		},
		colTenants: {
			{
				Keys:    bson.D{{Key: "slug", Value: 1}},
				Options: options.Index().SetUnique(true),
			},
		},
		colDeployments: {
			{Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "instance_id", Value: 1}, {Key: "created_at", Value: -1}}},
		},
		colReleases: {
			{Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "instance_id", Value: 1}, {Key: "version", Value: -1}}},
		},
		colHealthChecks: {
			{Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "instance_id", Value: 1}}},
		},
		colHealthResults: {
			{Keys: bson.D{{Key: "check_id", Value: 1}, {Key: "checked_at", Value: -1}}},
			{Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "instance_id", Value: 1}}},
		},
		colMetrics: {
			{Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "instance_id", Value: 1}, {Key: "timestamp", Value: -1}}},
		},
		colLogs: {
			{Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "instance_id", Value: 1}, {Key: "timestamp", Value: -1}}},
		},
		colTraces: {
			{Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "instance_id", Value: 1}, {Key: "timestamp", Value: -1}}},
			{Keys: bson.D{{Key: "trace_id", Value: 1}}},
		},
		colResourceSnapshots: {
			{Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "instance_id", Value: 1}, {Key: "timestamp", Value: -1}}},
		},
		colDomains: {
			{
				Keys:    bson.D{{Key: "hostname", Value: 1}},
				Options: options.Index().SetUnique(true),
			},
			{Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "instance_id", Value: 1}}},
		},
		colRoutes: {
			{Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "instance_id", Value: 1}}},
		},
		colCertificates: {
			{Keys: bson.D{{Key: "tenant_id", Value: 1}}},
			{Keys: bson.D{{Key: "domain_id", Value: 1}}},
		},
		colSecrets: {
			{
				Keys:    bson.D{{Key: "tenant_id", Value: 1}, {Key: "instance_id", Value: 1}, {Key: "key", Value: 1}},
				Options: options.Index().SetUnique(true),
			},
		},
		colAuditEntries: {
			{Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "created_at", Value: -1}}},
		},
	}
}

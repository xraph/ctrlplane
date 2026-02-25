package mongo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/xraph/grove"
	"github.com/xraph/grove/drivers/mongodriver"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/store"
)

// Collection name constants.
const (
	colInstances         = "cp_instances"
	colDeployments       = "cp_deployments"
	colReleases          = "cp_releases"
	colHealthChecks      = "cp_health_checks"
	colHealthResults     = "cp_health_results"
	colMetrics           = "cp_metrics"
	colLogs              = "cp_logs"
	colTraces            = "cp_traces"
	colResourceSnapshots = "cp_resource_snapshots"
	colDomains           = "cp_domains"
	colRoutes            = "cp_routes"
	colCertificates      = "cp_certificates"
	colSecrets           = "cp_secrets"
	colTenants           = "cp_tenants"
	colAuditEntries      = "cp_audit_entries"
)

// Compile-time interface check.
var _ store.Store = (*Store)(nil)

// Store implements store.Store using MongoDB via Grove ORM.
type Store struct {
	db  *grove.DB
	mdb *mongodriver.MongoDB
}

// New creates a new MongoDB store backed by Grove ORM.
func New(db *grove.DB) *Store {
	return &Store{
		db:  db,
		mdb: mongodriver.Unwrap(db),
	}
}

// DB returns the underlying grove database for direct access.
func (s *Store) DB() *grove.DB { return s.db }

// Migrate creates indexes for all controlplane collections.
func (s *Store) Migrate(ctx context.Context) error {
	indexes := migrationIndexes()

	for col, models := range indexes {
		if len(models) == 0 {
			continue
		}

		_, err := s.mdb.Collection(col).Indexes().CreateMany(ctx, models)
		if err != nil {
			return fmt.Errorf("ctrlplane/mongo: migrate %s indexes: %w", col, err)
		}
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

// now returns the current UTC time.
func now() time.Time {
	return time.Now().UTC()
}

// isNoDocuments checks if an error wraps mongo.ErrNoDocuments.
func isNoDocuments(err error) bool {
	return errors.Is(err, mongo.ErrNoDocuments)
}

// idStr returns the string representation of an ID.
func idStr(i id.ID) string {
	return i.String()
}

// migrationIndexes returns the index definitions for all collections.
func migrationIndexes() map[string][]mongo.IndexModel {
	return map[string][]mongo.IndexModel{
		colTenants: {
			{
				Keys:    bson.D{{Key: "slug", Value: 1}},
				Options: options.Index().SetUnique(true),
			},
		},
		colInstances: {
			{
				Keys:    bson.D{{Key: "tenant_id", Value: 1}, {Key: "slug", Value: 1}},
				Options: options.Index().SetUnique(true),
			},
			{Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "created_at", Value: -1}}},
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

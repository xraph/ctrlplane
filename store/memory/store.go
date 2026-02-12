package memory

import (
	"context"
	"sync"
	"time"

	"github.com/xraph/ctrlplane/admin"
	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/health"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/network"
	"github.com/xraph/ctrlplane/secrets"
	"github.com/xraph/ctrlplane/telemetry"
)

// Store is the in-memory implementation of store.Store.
type Store struct {
	mu sync.RWMutex

	instances   map[string]*instance.Instance // keyed by ID string
	deployments map[string]*deploy.Deployment
	releases    map[string]*deploy.Release

	healthChecks  map[string]*health.HealthCheck
	healthResults map[string][]health.HealthResult // keyed by check ID string

	metrics           []telemetry.Metric
	logs              []telemetry.LogEntry
	traces            []telemetry.Trace
	resourceSnapshots []telemetry.ResourceSnapshot

	domains      map[string]*network.Domain
	routes       map[string]*network.Route
	certificates map[string]*network.Certificate

	secretStore map[string]*secrets.Secret // keyed by "instanceID:key"

	tenants      map[string]*admin.Tenant
	auditEntries []admin.AuditEntry
}

// New creates a new in-memory store.
func New() *Store {
	return &Store{
		instances:     make(map[string]*instance.Instance),
		deployments:   make(map[string]*deploy.Deployment),
		releases:      make(map[string]*deploy.Release),
		healthChecks:  make(map[string]*health.HealthCheck),
		healthResults: make(map[string][]health.HealthResult),
		domains:       make(map[string]*network.Domain),
		routes:        make(map[string]*network.Route),
		certificates:  make(map[string]*network.Certificate),
		secretStore:   make(map[string]*secrets.Secret),
		tenants:       make(map[string]*admin.Tenant),
	}
}

// Migrate is a no-op for the in-memory store.
func (s *Store) Migrate(_ context.Context) error {
	return nil
}

// Ping is a no-op for the in-memory store.
func (s *Store) Ping(_ context.Context) error {
	return nil
}

// Close is a no-op for the in-memory store.
func (s *Store) Close() error {
	return nil
}

// idStr converts an id.ID to its string representation for map keys.
func idStr(i id.ID) string {
	return i.String()
}

// now returns the current UTC time.
func now() time.Time {
	return time.Now().UTC()
}

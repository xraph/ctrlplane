package event

import (
	"time"

	"github.com/xraph/ctrlplane/id"
)

// Type identifies the kind of event.
type Type string

// Instance events.
const (
	InstanceCreated     Type = "instance.created"
	InstanceStarted     Type = "instance.started"
	InstanceStopped     Type = "instance.stopped"
	InstanceFailed      Type = "instance.failed"
	InstanceDeleted     Type = "instance.deleted"
	InstanceScaled      Type = "instance.scaled"
	InstanceSuspended   Type = "instance.suspended"
	InstanceUnsuspended Type = "instance.unsuspended"
)

// Deploy events.
const (
	DeployStarted    Type = "deploy.started"
	DeploySucceeded  Type = "deploy.succeeded"
	DeployFailed     Type = "deploy.failed"
	DeployRolledBack Type = "deploy.rolled_back"
)

// Health events.
const (
	HealthCheckPassed Type = "health.passed"
	HealthCheckFailed Type = "health.failed"
	HealthDegraded    Type = "health.degraded"
	HealthRecovered   Type = "health.recovered"
)

// Network events.
const (
	DomainAdded     Type = "domain.added"
	DomainVerified  Type = "domain.verified"
	DomainRemoved   Type = "domain.removed"
	CertProvisioned Type = "cert.provisioned"
	CertExpiring    Type = "cert.expiring"
)

// Admin events.
const (
	TenantCreated   Type = "tenant.created"
	TenantSuspended Type = "tenant.suspended"
	TenantDeleted   Type = "tenant.deleted"
	QuotaExceeded   Type = "quota.exceeded"
)

// Datacenter events.
const (
	DatacenterCreated       Type = "datacenter.created"
	DatacenterUpdated       Type = "datacenter.updated"
	DatacenterDeleted       Type = "datacenter.deleted"
	DatacenterStatusChanged Type = "datacenter.status_changed"
)

// Route events for gateway hooks.
const (
	RouteAdded   Type = "route.added"
	RouteUpdated Type = "route.updated"
	RouteRemoved Type = "route.removed"
)

// Event is the envelope for all ctrlplane events.
type Event struct {
	ID         id.ID          `json:"id"`
	Type       Type           `json:"type"`
	TenantID   string         `json:"tenant_id"`
	InstanceID id.ID          `json:"instance_id,omitzero"`
	ActorID    string         `json:"actor_id,omitempty"`
	Payload    map[string]any `json:"payload,omitempty"`
	Timestamp  time.Time      `json:"timestamp"`
}

// NewEvent creates an event with a fresh ID and timestamp.
func NewEvent(t Type, tenantID string) *Event {
	return &Event{
		ID:        id.New(id.PrefixEvent),
		Type:      t,
		TenantID:  tenantID,
		Timestamp: time.Now().UTC(),
	}
}

// WithInstance sets the instance ID on the event.
func (e *Event) WithInstance(instanceID id.ID) *Event {
	e.InstanceID = instanceID

	return e
}

// WithActor sets the actor ID on the event.
func (e *Event) WithActor(actorID string) *Event {
	e.ActorID = actorID

	return e
}

// WithPayload sets the payload on the event.
func (e *Event) WithPayload(payload map[string]any) *Event {
	e.Payload = payload

	return e
}

// WithDatacenter sets the datacenter ID on the event payload.
func (e *Event) WithDatacenter(datacenterID id.ID) *Event {
	if e.Payload == nil {
		e.Payload = make(map[string]any)
	}

	e.Payload["datacenter_id"] = datacenterID.String()

	return e
}

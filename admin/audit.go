package admin

import ctrlplane "github.com/xraph/ctrlplane"

// AuditEntry records a single auditable action in the system.
type AuditEntry struct {
	ctrlplane.Entity

	TenantID   string         `db:"tenant_id"   json:"tenant_id"`
	ActorID    string         `db:"actor_id"    json:"actor_id"`
	ActorType  string         `db:"actor_type"  json:"actor_type"`
	Resource   string         `db:"resource"    json:"resource"`
	ResourceID string         `db:"resource_id" json:"resource_id"`
	Action     string         `db:"action"      json:"action"`
	Details    map[string]any `db:"details"     json:"details,omitempty"`
	IPAddress  string         `db:"ip_address"  json:"ip_address,omitempty"`
}

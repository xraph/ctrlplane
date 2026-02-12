package admin

import (
	"time"

	ctrlplane "github.com/xraph/ctrlplane"
)

// TenantStatus represents the lifecycle state of a tenant.
type TenantStatus string

const (
	// TenantActive indicates the tenant is active.
	TenantActive TenantStatus = "active"

	// TenantSuspended indicates the tenant is suspended.
	TenantSuspended TenantStatus = "suspended"

	// TenantDeleted indicates the tenant is deleted.
	TenantDeleted TenantStatus = "deleted"
)

// Tenant represents a tenant or organization in the control plane.
type Tenant struct {
	ctrlplane.Entity

	ExternalID  string            `db:"external_id"  json:"external_id,omitempty"`
	Name        string            `db:"name"         json:"name"`
	Slug        string            `db:"slug"         json:"slug"`
	Status      TenantStatus      `db:"status"       json:"status"`
	Plan        string            `db:"plan"         json:"plan"`
	Quota       Quota             `db:"quota"        json:"quota"`
	SuspendedAt *time.Time        `db:"suspended_at" json:"suspended_at,omitempty"`
	Metadata    map[string]string `db:"metadata"     json:"metadata,omitempty"`
}

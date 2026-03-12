package datacenter

import (
	"time"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
)

// Status represents the operational state of a datacenter.
type Status string

const (
	// StatusActive indicates the datacenter is available for new workloads.
	StatusActive Status = "active"

	// StatusMaintenance indicates the datacenter is undergoing maintenance.
	StatusMaintenance Status = "maintenance"

	// StatusDraining indicates the datacenter is being drained of workloads.
	StatusDraining Status = "draining"

	// StatusOffline indicates the datacenter is offline.
	StatusOffline Status = "offline"
)

// Location holds geographic coordinates and metadata for a datacenter.
type Location struct {
	Latitude  float64 `db:"latitude"  json:"latitude"`
	Longitude float64 `db:"longitude" json:"longitude"`
	Country   string  `db:"country"   json:"country"`
	City      string  `db:"city"      json:"city"`
}

// Capacity holds optional resource quotas for a datacenter.
type Capacity struct {
	MaxInstances int `db:"max_instances"  json:"max_instances"`
	MaxCPUMillis int `db:"max_cpu_millis" json:"max_cpu_millis"`
	MaxMemoryMB  int `db:"max_memory_mb"  json:"max_memory_mb"`
}

// Datacenter represents a deployment location backed by a provider.
type Datacenter struct {
	ctrlplane.Entity

	TenantID      string            `db:"tenant_id"       json:"tenant_id"`
	Name          string            `db:"name"            json:"name"`
	Slug          string            `db:"slug"            json:"slug"`
	ProviderName  string            `db:"provider_name"   json:"provider_name"`
	Region        string            `db:"region"          json:"region"`
	Zone          string            `db:"zone"            json:"zone"`
	Status        Status            `db:"status"          json:"status"`
	Location      Location          `db:"location"        json:"location"`
	Capacity      Capacity          `db:"capacity"        json:"capacity"`
	Labels        map[string]string `db:"labels"          json:"labels,omitempty"`
	Metadata      map[string]string `db:"metadata"        json:"metadata,omitempty"`
	LastCheckedAt *time.Time        `db:"last_checked_at" json:"last_checked_at,omitempty"`
}

// NewDatacenter creates a datacenter entity with a fresh ID and timestamps.
func NewDatacenter() *Datacenter {
	return &Datacenter{
		Entity: ctrlplane.NewEntity(id.PrefixDatacenter),
		Status: StatusActive,
	}
}

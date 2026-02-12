package instance

import (
	"time"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

// Instance represents a managed application instance belonging to a tenant.
type Instance struct {
	ctrlplane.Entity

	TenantID       string                 `db:"tenant_id"       json:"tenant_id"`
	Name           string                 `db:"name"            json:"name"`
	Slug           string                 `db:"slug"            json:"slug"`
	ProviderName   string                 `db:"provider_name"   json:"provider_name"`
	ProviderRef    string                 `db:"provider_ref"    json:"provider_ref"`
	Region         string                 `db:"region"          json:"region"`
	State          provider.InstanceState `db:"state"           json:"state"`
	Image          string                 `db:"image"           json:"image"`
	Resources      provider.ResourceSpec  `db:"resources"       json:"resources"`
	Env            map[string]string      `db:"env"             json:"env,omitempty"`
	Ports          []provider.PortSpec    `db:"ports"           json:"ports,omitempty"`
	Endpoints      []provider.Endpoint    `db:"endpoints"       json:"endpoints,omitempty"`
	Labels         map[string]string      `db:"labels"          json:"labels,omitempty"`
	CurrentRelease id.ID                  `db:"current_release" json:"current_release,omitzero"`
	SuspendedAt    *time.Time             `db:"suspended_at"    json:"suspended_at,omitempty"`
}

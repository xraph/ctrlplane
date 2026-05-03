package workload

import (
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

// CreateRequest holds the parameters for creating a Workload. The
// service runs Workload.Create followed by N Instance.Create calls
// (one per replica) so the caller's experience is single-shot:
// "give me a workload running 3 replicas of [main + sidecar]" → one
// API call.
//
// FromTemplateID, when non-zero, instructs the service to read the
// referenced template and use its fields as defaults; any field also
// non-zero on this request overrides the template's value.
type CreateRequest struct {
	Name           string                 `json:"name"                      validate:"required"`
	DatacenterID   id.ID                  `json:"datacenter_id,omitzero"`
	Region         string                 `json:"region,omitempty"`
	ProviderName   string                 `json:"provider_name,omitempty"`
	FromTemplateID id.ID                  `json:"from_template_id,omitzero"`
	Kind           provider.WorkloadKind  `json:"kind,omitempty"`
	Services       []provider.ServiceSpec `json:"services,omitempty"`
	Labels         map[string]string      `json:"labels,omitempty"`

	// Replicas is the desired replica count. Defaults to 1 when zero
	// or negative.
	Replicas int `json:"replicas,omitempty"`
}

// UpdateRequest mutates a Workload's spec. Replicas live on Scale,
// not here. Kind is immutable post-creation and must be set via
// recreate, not Update.
type UpdateRequest struct {
	Name     *string                `json:"name,omitempty"`
	Services []provider.ServiceSpec `json:"services,omitempty"`
	Labels   map[string]string      `json:"labels,omitempty"`
}

// ListOptions filters the Workload list endpoint.
type ListOptions struct {
	State        State  `json:"state,omitempty"`
	ProviderName string `json:"provider_name,omitempty"`
	Region       string `json:"region,omitempty"`
	Limit        int    `json:"limit,omitempty"`
}

// ListResult holds a page of Workloads.
type ListResult struct {
	Items []*Workload `json:"items"`
	Total int         `json:"total"`
}

// DeployRequest kicks off a new release rollout. Services lists only
// the services being changed in this rollout — services not listed
// inherit their snapshot from the prior Release.
type DeployRequest struct {
	Services []provider.ServiceDeploySpec `json:"services"           validate:"required,min=1"`
	Strategy string                       `json:"strategy,omitempty"` // "rolling" (default), "recreate", "blue_green", "canary"
	Notes    string                       `json:"notes,omitempty"`
}

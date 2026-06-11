package template

import (
	"context"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/vars"
)

// CreateRequest holds the parameters for creating a workload template.
//
// A template describes what to deploy via Source. For backward
// compatibility, callers may instead populate Services alone — Create
// projects them onto a services Source.
type CreateRequest struct {
	Name            string                    `json:"name"                       validate:"required"`
	Description     string                    `json:"description,omitempty"`
	DefaultKind     provider.WorkloadKind     `json:"default_kind,omitempty"`
	DefaultStrategy string                    `json:"default_strategy,omitempty"`
	Services        []provider.ServiceSpec    `json:"services,omitempty"`
	Labels          map[string]string         `json:"labels,omitempty"`
	Notes           string                    `json:"notes,omitempty"`
	Variables       []vars.Definition         `json:"variables,omitempty"`
	Source          provider.DeploymentSource `json:"source,omitzero"`
}

// UpdateRequest holds the parameters for updating a workload template.
// Pointer fields enable partial updates — only non-nil fields are applied.
type UpdateRequest struct {
	Name            *string                    `json:"name,omitempty"`
	Description     *string                    `json:"description,omitempty"`
	DefaultKind     *provider.WorkloadKind     `json:"default_kind,omitempty"`
	DefaultStrategy *string                    `json:"default_strategy,omitempty"`
	Services        []provider.ServiceSpec     `json:"services,omitempty"`
	Labels          map[string]string          `json:"labels,omitempty"`
	Notes           *string                    `json:"notes,omitempty"`
	Variables       []vars.Definition          `json:"variables,omitempty"`
	Source          *provider.DeploymentSource `json:"source,omitempty"`
}

// CreateFromWorkloadRequest forks a template from an existing workload's
// spec. Only Name and Description are user-supplied — every other field
// is read from the workload via the WorkloadSpecReader.
type CreateFromWorkloadRequest struct {
	Name        string `json:"name"                  validate:"required"`
	Description string `json:"description,omitempty"`
	Notes       string `json:"notes,omitempty"`
}

// ListOptions configures template listing.
type ListOptions struct {
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// ListResult holds a page of templates with a total count.
type ListResult struct {
	Items      []*Template `json:"items"`
	NextCursor string      `json:"next_cursor,omitempty"`
	Total      int         `json:"total"`
}

// WorkloadSpec is the projection of a Workload's blueprint-relevant
// fields the template service needs in order to fork a template from a
// workload. The workload package adapts its concrete Workload onto this
// shape via WorkloadSpecReader so this package never imports workload.
type WorkloadSpec struct {
	Kind     provider.WorkloadKind
	Services []provider.ServiceSpec
	Labels   map[string]string
}

// WorkloadSpecReader is the dependency template uses to read a
// workload's spec when forking a template.
type WorkloadSpecReader interface {
	ReadWorkloadSpec(ctx context.Context, tenantID string, workloadID id.ID) (*WorkloadSpec, error)
}

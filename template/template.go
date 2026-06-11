package template

import (
	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/vars"
)

// SecretRef is an alias for provider.SecretRef. The canonical type lives
// in the provider package so it can sit alongside ServiceSpec without
// introducing a template ↔ provider import cycle. Kept here as a thin
// alias so existing callers that referenced template.SecretRef do not
// need to chase the rename.
type SecretRef = provider.SecretRef

// ConfigFile is an alias for provider.ConfigFile. See SecretRef for the
// rationale.
type ConfigFile = provider.ConfigFile

// Template is a reusable workload blueprint that can be instantiated to
// create a Workload. The Services slice mirrors a Workload's Services —
// instantiation copies them onto the new Workload verbatim. Workload-
// level fields (DefaultKind, DefaultStrategy, Labels) seed the workload
// at creation time and may be overridden by the CreateRequest.
type Template struct {
	ctrlplane.Entity

	TenantID        string                 `db:"tenant_id"        json:"tenant_id"`
	Name            string                 `db:"name"             json:"name"`
	Description     string                 `db:"description"      json:"description,omitempty"`
	DefaultKind     provider.WorkloadKind  `db:"default_kind"     json:"default_kind,omitempty"`
	DefaultStrategy string                 `db:"default_strategy" json:"default_strategy,omitempty"`
	Services        []provider.ServiceSpec `db:"services"         json:"services"`
	Labels          map[string]string      `db:"labels"           json:"labels,omitempty"`
	Notes           string                 `db:"notes"            json:"notes,omitempty"`

	// Variables declares the typed template variables resolved at
	// instantiation time and injected into the deployment source.
	Variables []vars.Definition `db:"variables" json:"variables,omitempty"`

	// Source describes what the template deploys (services | helm |
	// manifests | argocd). Legacy templates carry only Services; call
	// NormalizeSource to project them onto a services Source.
	Source provider.DeploymentSource `db:"source" json:"source,omitzero"`
}

// NormalizeSource projects a legacy services-only template onto a typed
// services Source when no Source.Type is set. Idempotent — an explicit
// Source is left untouched.
func (t *Template) NormalizeSource() {
	if t.Source.Type == "" && len(t.Services) > 0 {
		t.Source = provider.DeploymentSource{
			Type:     provider.SourceServices,
			Services: t.Services,
		}
	}
}

// MainService returns the template's Main service, or nil when none is
// configured. Used by the dashboard to show "the template's image"
// when a single representative is needed.
func (t *Template) MainService() *provider.ServiceSpec {
	if t == nil {
		return nil
	}

	for i := range t.Services {
		if t.Services[i].Role == provider.RoleMain || t.Services[i].Role == "" {
			return &t.Services[i]
		}
	}

	return nil
}

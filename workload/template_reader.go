package workload

import (
	"context"
	"fmt"
	"maps"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/template"
)

// SpecReader adapts a workload service onto template.WorkloadSpecReader
// so the template service can fork a Template from an existing
// Workload's spec without importing the workload package directly
// (which would create an import cycle).
//
// Wire it at composition time:
//
//	wlSvc := workload.NewService(...)
//	tplSvc := template.NewService(store, events)
//	tplSvc.SetWorkloadReader(workload.NewSpecReader(wlSvc))
type SpecReader struct {
	source workloadGetter
}

// workloadGetter is the minimal surface SpecReader needs from
// workload.Service.
type workloadGetter interface {
	Get(ctx context.Context, workloadID id.ID) (*Workload, error)
}

// NewSpecReader returns a reader backed by the given workload service.
func NewSpecReader(source workloadGetter) *SpecReader {
	return &SpecReader{source: source}
}

// ReadWorkloadSpec implements template.WorkloadSpecReader. It loads
// the workload via the embedded service and projects its blueprint-
// relevant fields onto a template.WorkloadSpec.
func (r *SpecReader) ReadWorkloadSpec(ctx context.Context, tenantID string, workloadID id.ID) (*template.WorkloadSpec, error) {
	w, err := r.source.Get(ctx, workloadID)
	if err != nil {
		return nil, err
	}

	if tenantID != "" && w.TenantID != tenantID {
		return nil, fmt.Errorf("read workload spec: tenant mismatch for %s", workloadID)
	}

	return &template.WorkloadSpec{
		Kind:     w.Kind,
		Services: cloneServices(w.Services),
		Labels:   cloneStringMap(w.Labels),
	}, nil
}

// cloneServices returns a shallow copy of a ServiceSpec slice with an
// independent backing array. Nested map/slice fields share storage —
// callers that need a fully detached deep copy should round-trip through
// JSON.
func cloneServices(in []provider.ServiceSpec) []provider.ServiceSpec {
	if in == nil {
		return nil
	}

	out := make([]provider.ServiceSpec, len(in))
	copy(out, in)

	return out
}

// applyTemplateDefaults fills empty fields on a workload create
// request from the source template. Fields already set on the request
// win; the template is the baseline.
func applyTemplateDefaults(req CreateRequest, tmpl *template.Template) CreateRequest {
	if tmpl == nil {
		return req
	}

	if len(req.Services) == 0 && len(tmpl.Services) > 0 {
		req.Services = cloneServices(tmpl.Services)
	}

	if req.Kind == "" {
		req.Kind = tmpl.DefaultKind
	}

	if req.Labels == nil && len(tmpl.Labels) > 0 {
		req.Labels = cloneStringMap(tmpl.Labels)
	}

	return req
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}

	out := make(map[string]string, len(in))
	maps.Copy(out, in)

	return out
}

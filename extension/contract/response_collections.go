package contract

import (
	"errors"
	"fmt"
	"time"

	"github.com/xraph/ctrlplane/admin"
	"github.com/xraph/ctrlplane/datacenter"
	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/template"
)

type listDTO[T any] struct {
	Items      []T    `json:"items"`
	Total      int    `json:"total"`
	NextCursor string `json:"next_cursor,omitempty"`
}
type datacenterRowDTO struct {
	datacenterDTO

	InstanceCount int `json:"instance_count"`
}
type healthRowDTO struct {
	Instance *instanceDTO       `json:"instance"`
	Health   *instanceHealthDTO `json:"health"`
	Error    string             `json:"error,omitempty"`
}
type healthSummaryDTO struct {
	Items    []healthRowDTO `json:"items"`
	Total    int            `json:"total"`
	Complete bool           `json:"complete"`
	Counts   map[string]int `json:"counts"`
}
type sessionDTO struct {
	Subject  string `json:"subject"`
	TenantID string `json:"tenant_id"`
	Admin    bool   `json:"admin"`
}
type ackDTO struct {
	OK bool `json:"ok"`
}
type statsDTO struct {
	TotalTenants   int `json:"total_tenants"`
	ActiveTenants  int `json:"active_tenants"`
	TotalProviders int `json:"total_providers"`
}
type configDTO struct {
	DefaultProvider    string `json:"default_provider"`
	HealthInterval     string `json:"health_interval"`
	TelemetryInterval  string `json:"telemetry_interval"`
	MaxInstances       int    `json:"max_instances_per_tenant"`
	AuditEnabled       bool   `json:"audit_enabled"`
	DatabaseConfigured bool   `json:"database_url_configured"`
}

func projectCollection(v any) (any, error) {
	switch v := v.(type) {
	case *instance.ListResult:
		if v == nil {
			return nil, errors.New("ctrlplane contract: missing list result")
		}

		return listDTO[*instanceDTO]{viewSlice(v.Items, func(v *instance.Instance) *instanceDTO { return viewPointer(v, projectInstance) }), v.Total, v.NextCursor}, nil
	case *template.ListResult:
		if v == nil {
			return nil, errors.New("ctrlplane contract: missing list result")
		}

		return listDTO[*templateDTO]{viewSlice(v.Items, func(v *template.Template) *templateDTO { return viewPointer(v, projectTemplate) }), v.Total, v.NextCursor}, nil
	case *deploy.DeployListResult:
		if v == nil {
			return nil, errors.New("ctrlplane contract: missing list result")
		}

		return listDTO[*deploymentDTO]{viewSlice(v.Items, func(v *deploy.Deployment) *deploymentDTO { return viewPointer(v, projectDeployment) }), v.Total, v.NextCursor}, nil
	case *deploy.ReleaseListResult:
		if v == nil {
			return nil, errors.New("ctrlplane contract: missing list result")
		}

		return listDTO[*releaseDTO]{viewSlice(v.Items, func(v *deploy.Release) *releaseDTO { return viewPointer(v, projectRelease) }), v.Total, v.NextCursor}, nil
	case *datacenter.ListResult:
		if v == nil {
			return nil, errors.New("ctrlplane contract: missing list result")
		}

		return listDTO[*datacenterDTO]{viewSlice(v.Items, func(v *datacenter.Datacenter) *datacenterDTO { return viewPointer(v, projectDatacenter) }), v.Total, v.NextCursor}, nil
	case *admin.TenantListResult:
		if v == nil {
			return nil, errors.New("ctrlplane contract: missing list result")
		}

		return listDTO[*tenantDTO]{viewSlice(v.Items, func(v *admin.Tenant) *tenantDTO { return viewPointer(v, projectTenant) }), v.Total, v.NextCursor}, nil
	case *admin.AuditResult:
		if v == nil {
			return nil, errors.New("ctrlplane contract: missing list result")
		}

		return listDTO[auditEntryDTO]{viewSlice(v.Items, projectAuditEntry), v.Total, v.NextCursor}, nil
	case healthResult:
		return healthSummaryDTO{Items: viewSlice(v.Items, func(v healthRow) healthRowDTO {
			return healthRowDTO{viewPointer(v.Instance, projectInstance), viewPointer(v.Health, projectInstanceHealth), safeDiagnostic(v.Error)}
		}), Total: v.Total, Complete: v.Complete, Counts: v.Counts}, nil
	case []providerStatus:
		return viewSlice(v, func(v providerStatus) providerStatusDTO {
			return providerStatusDTO{v.Name, v.Region, v.Healthy, viewPointer(v.Location, projectProviderLocation), v.Capabilities, v.CheckedAt, v.Message}
		}), nil
	case page:
		items, err := projectResponse(v.Items)
		if err != nil {
			return nil, err
		}

		return page{Items: items, Total: v.Total, Complete: v.Complete}, nil
	case []any:
		out := make([]any, 0, len(v))
		for _, row := range v {
			item, err := projectResponse(row)
			if err != nil {
				return nil, err
			}

			out = append(out, item)
		}

		return out, nil
	case sessionDTO, ackDTO, statsDTO, configDTO, purgeSummary, listDTO[datacenterRowDTO]:
		return v, nil
	default:
		return nil, fmt.Errorf("ctrlplane contract: response projection missing for %T", v)
	}
}

func safeDiagnostic(message string) string {
	if message == "" {
		return ""
	}

	return "Operation failed. See server diagnostics for details."
}
func safeObservation(message string) string {
	if message == "" {
		return ""
	}

	return "Observation recorded. See server diagnostics for details."
}

// safeMetadata permits known event and audit fields, never arbitrary objects or backend diagnostics.
func safeMetadata(in map[string]any) map[string]any {
	out := make(map[string]any)

	for key, value := range in {
		switch key {
		case "error", "warning":
			out[key] = "See server diagnostics for details."
		case "workload_id", "instance_id", "template_id", "datacenter_id", "deployment_id", "release_id", "domain_id", "route_id", "certificate_id", "tenant_name", "plan", "reason", "services_deployed", "strategy", "version", "replicas", "replica_count", "service_count", "kind", "cpu_millis", "memory_mb", "hostname", "path", "port", "protocol", "rollback":
			switch value := value.(type) {
			case string, bool, int, int32, int64, uint, uint64, float64, []string:
				out[key] = value
			case id.ID:
				out[key] = value.String()
			}
		}
	}

	return out
}

type providerStatusDTO struct {
	Name         string               `json:"name"`
	Region       string               `json:"region"`
	Healthy      *bool                `json:"healthy"`
	Location     *providerLocationDTO `json:"location"`
	Capabilities []string             `json:"capabilities"`
	CheckedAt    *time.Time           `json:"checked_at"`
	Message      string               `json:"message"`
}

func observedTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}

	t = t.UTC()

	return &t
}

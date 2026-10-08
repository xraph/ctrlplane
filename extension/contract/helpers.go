package contract

import (
	"context"
	"fmt"
	"strings"
	"time"

	dash "github.com/xraph/forge/extensions/dashboard/contract"

	"github.com/xraph/ctrlplane/admin"
	"github.com/xraph/ctrlplane/app"
	"github.com/xraph/ctrlplane/bootstrap"
	"github.com/xraph/ctrlplane/datacenter"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/health"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/workload"
)

type reasonInput struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}
type scaleInput struct {
	ID       string `json:"id"`
	Replicas int    `json:"replicas"`
}
type rollbackInput struct {
	InstanceID string `json:"instance_id"`
	ReleaseID  string `json:"release_id"`
}
type secretInput struct {
	InstanceID string `json:"instance_id"`
	Key        string `json:"key"`
}
type statusInput struct {
	ID     string            `json:"id"`
	Status datacenter.Status `json:"status"`
}
type eventInput struct {
	Type  string `json:"type"`
	Limit int    `json:"limit"`
}

func badRequest(message string) error {
	return &dash.Error{Code: dash.CodeBadRequest, Message: message}
}
func unavailable(message string) error {
	return &dash.Error{Code: dash.CodeUnavailable, Message: message, Retryable: true}
}

func recentEvents(cp *app.CtrlPlane, in eventInput) page {
	all := cp.Events().RecentEvents(200)
	filtered := make([]*event.Event, 0)

	for _, e := range all {
		if in.Type == "" || strings.HasPrefix(string(e.Type), in.Type+".") {
			filtered = append(filtered, e)
		}
	}

	count := min(limit(in.Limit), len(filtered))

	return page{Items: filtered[:count], Total: len(filtered), Complete: false}
}

func configDetail(cp *app.CtrlPlane) any {
	cfg := cp.Config()

	return struct {
		DefaultProvider    string `json:"default_provider"`
		HealthInterval     string `json:"health_interval"`
		TelemetryInterval  string `json:"telemetry_interval"`
		MaxInstances       int    `json:"max_instances_per_tenant"`
		AuditEnabled       bool   `json:"audit_enabled"`
		DatabaseConfigured bool   `json:"database_configured"`
	}{cfg.DefaultProvider, cfg.HealthInterval.String(), cfg.TelemetryFlushInterval.String(), cfg.MaxInstancesPerTenant, cfg.AuditEnabled, cfg.DatabaseURL != ""}
}

type healthRow struct {
	Instance *instance.Instance     `json:"instance"`
	Health   *health.InstanceHealth `json:"health"`
	Error    string                 `json:"error,omitempty"`
}

type healthResult struct {
	Items    []healthRow    `json:"items"`
	Total    int            `json:"total"`
	Complete bool           `json:"complete"`
	Counts   map[string]int `json:"counts"`
}

func healthSummary(ctx context.Context, cp *app.CtrlPlane, in instance.ListOptions) (any, error) {
	in.Limit = limit(in.Limit)

	result, err := cp.Instances.List(ctx, in)
	if err != nil {
		return nil, err
	}

	out := healthResult{Items: make([]healthRow, 0, len(result.Items)), Total: result.Total, Complete: result.NextCursor == "" && len(result.Items) >= result.Total, Counts: map[string]int{"healthy": 0, "degraded": 0, "unhealthy": 0, "unknown": 0}}
	for _, inst := range result.Items {
		h, err := cp.Health.GetHealth(ctx, inst.ID)

		row := healthRow{Instance: inst, Health: h}
		if err != nil {
			row.Error = err.Error()
			row.Health = &health.InstanceHealth{InstanceID: inst.ID, Status: health.StatusUnknown}
			out.Complete = false
		}

		out.Counts[string(row.Health.Status)]++
		out.Items = append(out.Items, row)
	}

	return out, nil
}

func retryBootstrap(ctx context.Context, cp *app.CtrlPlane, value string) (any, error) {
	target, err := parseID(value, id.PrefixBootstrap)
	if err != nil {
		return nil, err
	}

	if cp.Bootstraps == nil {
		return nil, unavailable("Bootstrap service is not configured.")
	}

	row, err := cp.Bootstraps.Get(ctx, target)
	if err != nil {
		return nil, err
	}

	if _, err := cp.Datacenters.Get(ctx, row.DatacenterID); err != nil {
		return nil, err
	}

	if row.State != bootstrap.StateFailed {
		return nil, &dash.Error{Code: dash.CodeConflict, Message: "Only a failed bootstrap can be retried."}
	}

	row.State = bootstrap.StatePending
	row.LastError = ""
	row.UpdatedAt = time.Now().UTC()

	return ack(cp.Store().UpdateBootstrap(ctx, row))
}

type purgeSummary struct {
	WorkloadsDeleted int      `json:"workloads_deleted"`
	InstancesDeleted int      `json:"instances_deleted"`
	Failures         []string `json:"failures"`
}

func purgeProvider(ctx context.Context, cp *app.CtrlPlane, name string) (any, error) {
	if name == "" {
		return nil, badRequest("Provider name is required.")
	}

	if _, err := cp.Providers().Get(name); err != nil {
		return nil, err
	}

	summary := purgeSummary{Failures: []string{}}
	// Re-list after each deletion batch because workload lists have no cursor.
	for {
		rows, err := cp.Workloads.List(ctx, workload.ListOptions{ProviderName: name, Limit: 200})
		if err != nil {
			return nil, err
		}

		if len(rows.Items) == 0 {
			break
		}

		for _, row := range rows.Items {
			if err := cp.Workloads.Delete(ctx, row.ID); err != nil {
				summary.Failures = append(summary.Failures, fmt.Sprintf("workload %s: %v", row.ID, err))
			} else {
				summary.WorkloadsDeleted++
			}
		}

		if len(summary.Failures) > 0 {
			return nil, &dash.Error{Code: dash.CodeConflict, Message: "Provider purge stopped after partial workload failures.", Details: map[string]any{"summary": summary}}
		}
	}

	for {
		rows, err := cp.Instances.List(ctx, instance.ListOptions{Provider: name, Limit: 200})
		if err != nil {
			return nil, err
		}

		if len(rows.Items) == 0 {
			break
		}

		for _, row := range rows.Items {
			if err := cp.Instances.Delete(ctx, row.ID); err != nil {
				summary.Failures = append(summary.Failures, fmt.Sprintf("instance %s: %v", row.ID, err))
			} else {
				summary.InstancesDeleted++
			}
		}

		if len(summary.Failures) > 0 {
			return nil, &dash.Error{Code: dash.CodeConflict, Message: "Provider purge stopped after partial instance failures.", Details: map[string]any{"summary": summary}}
		}
	}

	return summary, nil
}

func systemStats(ctx context.Context, cp *app.CtrlPlane) (any, error) {
	stats, err := cp.Admin.SystemStats(ctx)
	if err != nil {
		return nil, err
	}
	// These three counters are populated by the service. The remaining fields
	// on SystemStats are currently zero-value placeholders, not measurements.
	return struct {
		TotalTenants   int `json:"total_tenants"`
		ActiveTenants  int `json:"active_tenants"`
		TotalProviders int `json:"total_providers"`
	}{stats.TotalTenants, stats.ActiveTenants, stats.TotalProviders}, nil
}

type providerStatus struct {
	Name         string                  `json:"name"`
	Region       string                  `json:"region"`
	Healthy      *bool                   `json:"healthy"`
	Location     *admin.ProviderLocation `json:"location"`
	Capabilities []string                `json:"capabilities"`
	CheckedAt    *time.Time              `json:"checked_at"`
	Message      string                  `json:"message"`
}

func providerStatuses(ctx context.Context, cp *app.CtrlPlane) (any, error) {
	statuses, err := cp.Admin.ListProviders(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]providerStatus, 0, len(statuses))
	for _, row := range statuses {
		item := providerStatus{Name: row.Name, Region: row.Region, Location: row.Location, Capabilities: row.Capabilities, Message: "No health observation yet."}
		if cp.ProviderHealth != nil {
			if cached, ok := cp.ProviderHealth.Get(row.Name); ok {
				item.Healthy = &cached.Healthy
				item.CheckedAt = &cached.CheckedAt
				item.Message = cached.Message
			}
		}

		out = append(out, item)
	}

	return out, nil
}

package contract

import (
	"context"
	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/admin"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/datacenter"
	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/template"
	"github.com/xraph/ctrlplane/workload"
)

func registerQueries(b *bindings) {
	query(b, "deployments.recent", func(ctx context.Context, in instance.ListOptions) (any, error) {
		return recentDeployments(ctx, b.cp, in)
	})
	cp := b.cp
	query(b, "session.detail", func(ctx context.Context, _ struct{}) (any, error) {
		claims := auth.ClaimsFrom(ctx)

		return struct {
			Subject  string `json:"subject"`
			TenantID string `json:"tenant_id"`
			Admin    bool   `json:"admin"`
		}{claims.SubjectID, claims.TenantID, claims.IsSystemAdmin()}, nil
	})
	query(b, "instances.list", func(ctx context.Context, in instance.ListOptions) (any, error) {
		in.Limit = limit(in.Limit)

		return cp.Instances.List(ctx, in)
	})
	query(b, "instances.detail", func(ctx context.Context, in entityInput) (any, error) {
		target, err := parseID(in.ID, id.PrefixInstance)
		if err != nil {
			return nil, err
		}

		return cp.Instances.Get(ctx, target)
	})
	query(b, "workloads.list", func(ctx context.Context, in workload.ListOptions) (any, error) {
		in.Limit = limit(in.Limit)

		result, err := cp.Workloads.List(ctx, in)
		if err != nil {
			return nil, err
		}

		return page{Items: result.Items, Total: result.Total, Complete: len(result.Items) >= result.Total && len(result.Items) < in.Limit}, nil
	})
	query(b, "workloads.detail", func(ctx context.Context, in entityInput) (any, error) {
		target, err := parseID(in.ID, id.PrefixWorkload)
		if err != nil {
			return nil, err
		}

		return cp.Workloads.Get(ctx, target)
	})
	query(b, "templates.list", func(ctx context.Context, in template.ListOptions) (any, error) {
		in.Limit = limit(in.Limit)

		return cp.Templates.List(ctx, in)
	})
	query(b, "templates.detail", func(ctx context.Context, in entityInput) (any, error) {
		target, err := parseID(in.ID, id.PrefixTemplate)
		if err != nil {
			return nil, err
		}

		return cp.Templates.Get(ctx, target)
	})
	query(b, "datacenters.list", func(ctx context.Context, in datacenter.ListOptions) (any, error) {
		in.Limit = limit(in.Limit)

		result, err := cp.Datacenters.List(ctx, in)
		if err != nil {
			return nil, err
		}

		type row struct {
			*datacenter.Datacenter

			InstanceCount int `json:"instance_count"`
		}

		items := make([]row, 0, len(result.Items))
		for _, dc := range result.Items {
			count, err := cp.Store().CountInstancesByDatacenter(ctx, auth.ClaimsFrom(ctx).TenantID, dc.ID)
			if err != nil {
				return nil, err
			}

			items = append(items, row{dc, count})
		}

		return struct {
			Items      []row  `json:"items"`
			Total      int    `json:"total"`
			NextCursor string `json:"next_cursor,omitempty"`
		}{items, result.Total, result.NextCursor}, nil
	})
	query(b, "datacenters.detail", func(ctx context.Context, in entityInput) (any, error) {
		target, err := parseID(in.ID, id.PrefixDatacenter)
		if err != nil {
			return nil, err
		}

		return cp.Datacenters.Get(ctx, target)
	})
	query(b, "workloads.instances", func(ctx context.Context, in entityInput) (any, error) {
		target, err := parseID(in.ID, id.PrefixWorkload)
		if err != nil {
			return nil, err
		}

		if _, err := cp.Workloads.Get(ctx, target); err != nil {
			return nil, err
		}

		return cp.Workloads.ListInstances(ctx, target)
	})
	query(b, "workloads.health", func(ctx context.Context, in entityInput) (any, error) {
		target, err := parseID(in.ID, id.PrefixWorkload)
		if err != nil {
			return nil, err
		}

		if _, err := cp.Workloads.Get(ctx, target); err != nil {
			return nil, err
		}

		replicas, err := cp.Workloads.ListInstances(ctx, target)
		if err != nil {
			return nil, err
		}

		for _, replica := range replicas {
			if _, err := cp.Health.GetHealth(ctx, replica.ID); err != nil {
				return nil, err
			}
		}

		return cp.Workloads.GetHealth(ctx, target)
	})
	query(b, "deployments.list", func(ctx context.Context, in targetInput) (any, error) {
		opts := deploy.ListOptions{Cursor: in.Cursor, Limit: limit(in.Limit)}
		if in.WorkloadID != "" {
			target, err := parseID(in.WorkloadID, id.PrefixWorkload)
			if err != nil {
				return nil, err
			}

			return workloadCollection(ctx, cp, target, "deployments", opts)
		}

		target, err := ownedInstance(ctx, cp, in.InstanceID)
		if err != nil {
			return nil, err
		}

		return cp.Deploys.ListDeployments(ctx, target, opts)
	})
	query(b, "deployments.detail", func(ctx context.Context, in entityInput) (any, error) {
		target, err := parseID(in.ID, id.PrefixDeployment)
		if err != nil {
			return nil, err
		}

		return cp.Deploys.GetDeployment(ctx, target)
	})
	query(b, "releases.list", func(ctx context.Context, in targetInput) (any, error) {
		opts := deploy.ListOptions{Cursor: in.Cursor, Limit: limit(in.Limit)}
		if in.WorkloadID != "" {
			target, err := parseID(in.WorkloadID, id.PrefixWorkload)
			if err != nil {
				return nil, err
			}

			return workloadCollection(ctx, cp, target, "releases", opts)
		}

		target, err := ownedInstance(ctx, cp, in.InstanceID)
		if err != nil {
			return nil, err
		}

		return cp.Deploys.ListReleases(ctx, target, opts)
	})
	query(b, "releases.detail", func(ctx context.Context, in entityInput) (any, error) {
		target, err := parseID(in.ID, id.PrefixRelease)
		if err != nil {
			return nil, err
		}

		return cp.Deploys.GetRelease(ctx, target)
	})
	query(b, "health.detail", func(ctx context.Context, in targetInput) (any, error) {
		target, err := ownedInstance(ctx, cp, in.InstanceID)
		if err != nil {
			return nil, err
		}

		return cp.Health.GetHealth(ctx, target)
	})
	query(b, "health.checks", func(ctx context.Context, in targetInput) (any, error) {
		target, err := ownedInstance(ctx, cp, in.InstanceID)
		if err != nil {
			return nil, err
		}

		return cp.Health.ListChecks(ctx, target)
	})
	query(b, "secrets.list", func(ctx context.Context, in targetInput) (any, error) {
		target, err := ownedInstance(ctx, cp, in.InstanceID)
		if err != nil {
			return nil, err
		}

		return cp.Secrets.List(ctx, target)
	})
	query(b, "telemetry.detail", func(ctx context.Context, in targetInput) (any, error) {
		target, err := ownedInstance(ctx, cp, in.InstanceID)
		if err != nil {
			return nil, err
		}

		return cp.Telemetry.GetDashboard(ctx, target)
	})
	query(b, "domains.list", func(ctx context.Context, in targetInput) (any, error) {
		if in.WorkloadID != "" {
			target, err := parseID(in.WorkloadID, id.PrefixWorkload)
			if err != nil {
				return nil, err
			}

			return workloadCollection(ctx, cp, target, "domains", deploy.ListOptions{})
		}

		target, err := ownedInstance(ctx, cp, in.InstanceID)
		if err != nil {
			return nil, err
		}

		return cp.Network.ListDomains(ctx, target)
	})
	query(b, "routes.list", func(ctx context.Context, in targetInput) (any, error) {
		if in.WorkloadID != "" {
			target, err := parseID(in.WorkloadID, id.PrefixWorkload)
			if err != nil {
				return nil, err
			}

			return workloadCollection(ctx, cp, target, "routes", deploy.ListOptions{})
		}

		target, err := ownedInstance(ctx, cp, in.InstanceID)
		if err != nil {
			return nil, err
		}

		return cp.Network.ListRoutes(ctx, target)
	})
	query(b, "certificates.list", func(ctx context.Context, in targetInput) (any, error) {
		if in.WorkloadID != "" {
			return nil, badRequest("Choose an instance for certificates.")
		}

		target, err := ownedInstance(ctx, cp, in.InstanceID)
		if err != nil {
			return nil, err
		}

		return cp.Network.ListCerts(ctx, target)
	})
	query(b, "system.stats", func(ctx context.Context, in struct{}) (any, error) { return systemStats(ctx, cp) })
	query(b, "providers.list", func(ctx context.Context, in struct{}) (any, error) { return providerStatuses(ctx, cp) })
	query(b, "workers.list", func(ctx context.Context, in struct{}) (any, error) {
		if cp.Scheduler() == nil {
			return nil, unavailable("Scheduler is not configured.")
		}

		return cp.Scheduler().Workers(), nil
	})
	query(b, "workers.detail", func(ctx context.Context, in namedInput) (any, error) {
		if cp.Scheduler() == nil {
			return nil, unavailable("Scheduler is not configured.")
		}

		result, ok := cp.Scheduler().WorkerByName(in.Name)
		if !ok {
			return nil, ctrlplane.ErrNotFound
		}

		return result, nil
	})
	query(b, "events.list", func(ctx context.Context, in eventInput) (any, error) { return recentEvents(cp, in), nil })
	query(b, "config.detail", func(ctx context.Context, in struct{}) (any, error) { return configDetail(cp), nil })
	query(b, "audit.list", func(ctx context.Context, in admin.AuditQuery) (any, error) {
		in.Limit = limit(in.Limit)

		claims := auth.ClaimsFrom(ctx)
		if !claims.IsSystemAdmin() {
			in.TenantID = claims.TenantID
		}

		return cp.Admin.QueryAuditLog(ctx, in)
	})
	query(b, "tenants.list", func(ctx context.Context, in admin.ListTenantsOptions) (any, error) {
		in.Limit = limit(in.Limit)

		return cp.Admin.ListTenants(ctx, in)
	})
	query(b, "tenants.detail", func(ctx context.Context, in entityInput) (any, error) {
		if _, err := parseID(in.ID, id.PrefixTenant); err != nil {
			return nil, err
		}

		return cp.Admin.GetTenant(ctx, in.ID)
	})
	query(b, "tenants.quota", func(ctx context.Context, in entityInput) (any, error) {
		if _, err := parseID(in.ID, id.PrefixTenant); err != nil {
			return nil, err
		}

		return cp.Admin.GetQuota(ctx, in.ID)
	})
	query(b, "datacenters.instances", func(ctx context.Context, in entityInput) (any, error) {
		target, err := parseID(in.ID, id.PrefixDatacenter)
		if err != nil {
			return nil, err
		}

		if _, err := cp.Datacenters.Get(ctx, target); err != nil {
			return nil, err
		}

		return cp.Instances.List(ctx, instance.ListOptions{Datacenter: target.String(), Limit: 200})
	})
	query(b, "bootstrap.list", func(ctx context.Context, in entityInput) (any, error) {
		target, err := parseID(in.ID, id.PrefixDatacenter)
		if err != nil {
			return nil, err
		}

		if _, err := cp.Datacenters.Get(ctx, target); err != nil {
			return nil, err
		}

		if cp.Bootstraps == nil {
			return nil, unavailable("Bootstrap service is not configured.")
		}

		return cp.Bootstraps.ListByDatacenter(ctx, target)
	})
	query(b, "health.summary", func(ctx context.Context, in instance.ListOptions) (any, error) { return healthSummary(ctx, cp, in) })
}

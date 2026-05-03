package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/a-h/templ"
	"github.com/xraph/forge/extensions/dashboard/contributor"

	"github.com/xraph/ctrlplane/admin"
	"github.com/xraph/ctrlplane/app"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/bootstrap"
	"github.com/xraph/ctrlplane/dashboard/components"
	"github.com/xraph/ctrlplane/dashboard/pages"
	"github.com/xraph/ctrlplane/dashboard/settings"
	"github.com/xraph/ctrlplane/dashboard/widgets"
	"github.com/xraph/ctrlplane/datacenter"
	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/health"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/network"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/template"
	"github.com/xraph/ctrlplane/workload"
)

// Compile-time interface assertions.
var _ contributor.LocalContributor = (*Contributor)(nil)

// Contributor implements the dashboard LocalContributor for CtrlPlane.
type Contributor struct {
	manifest *contributor.Manifest
	cp       *app.CtrlPlane
}

// New creates a new ctrlplane dashboard contributor.
func New(manifest *contributor.Manifest, cp *app.CtrlPlane) *Contributor {
	return &Contributor{
		manifest: manifest,
		cp:       cp,
	}
}

// Manifest returns the contributor manifest.
func (c *Contributor) Manifest() *contributor.Manifest {
	return c.manifest
}

// dashboardClaims are injected into every dashboard request context so
// ctrlplane service calls (which require auth.RequireClaims) succeed.
// Dashboard access is already gated by the Forge dashboard auth layer,
// so granting system:admin here is safe.
//
// TenantID is intentionally empty: the dashboard is the operator
// console, so List/Get queries should span every tenant rather than
// filter to one. Stores treat tenantID=="" as "no tenant filter" (see
// store/mongo/instance.go::List). Without this the dashboard would
// only show platform-shared rows, or — if upstream auth middleware
// populated tenant-scoped claims — only the viewer's personal
// tenant's data, which is wrong for a cross-tenant admin view.
var dashboardClaims = &auth.Claims{
	SubjectID: "dashboard",
	TenantID:  "",
	Roles:     []string{"system:admin"},
}

// dashboardContext returns ctx with platform-admin auth claims
// injected. The dashboard is the cross-tenant admin view, so we
// always elevate — even when the request already carries
// tenant-scoped claims from upstream auth middleware. Per-row
// access stays safe because the dashboard only reads (List / Get);
// mutations (Suspend / Scale / etc.) live behind separate admin
// handlers that re-check authorization themselves.
func (c *Contributor) dashboardContext(ctx context.Context) context.Context {
	return auth.WithClaims(ctx, dashboardClaims)
}

// RenderPage renders a page for the given route.
func (c *Contributor) RenderPage(ctx context.Context, route string, params contributor.Params) (templ.Component, error) {
	ctx = c.dashboardContext(ctx)

	switch route {
	case "/", "":
		return c.renderOverview(ctx)
	case "/instances":
		return c.renderInstances(ctx, params)
	case "/instances/detail":
		return c.renderInstanceDetail(ctx, params)
	case "/workloads":
		return c.renderWorkloads(ctx, params)
	case "/workloads/detail":
		return c.renderWorkloadDetail(ctx, params)
	case "/deployments":
		return c.renderDeployments(ctx, params)
	case "/deployments/detail":
		return c.renderDeploymentDetail(ctx, params)
	case "/health":
		return c.renderHealth(ctx, params)
	case "/network":
		return c.renderNetwork(ctx, params)
	case "/secrets":
		return c.renderSecrets(ctx, params)
	case "/tenants":
		return c.renderTenants(ctx, params)
	case "/tenants/detail":
		return c.renderTenantDetail(ctx, params)
	case "/audit":
		return c.renderAuditLog(ctx, params)
	case "/deployments/create":
		return c.renderDeployCreate(ctx, params)
	case "/deployments/rollback":
		return c.renderDeployRollback(ctx, params)
	case "/providers":
		return c.renderProviders(ctx, params)
	case "/providers/detail":
		return c.renderProviderDetail(ctx, params)
	case "/workers":
		return c.renderWorkers(ctx, params)
	case "/workers/detail":
		return c.renderWorkerDetail(ctx, params)
	case "/events":
		return c.renderEvents(ctx, params)
	case "/templates":
		return c.renderTemplates(ctx, params)
	case "/templates/detail":
		return c.renderTemplateDetail(ctx, params)
	case "/templates/create":
		return c.renderTemplateCreate(ctx, params)
	case "/templates/edit":
		return c.renderTemplateEdit(ctx, params)
	case "/datacenters":
		return c.renderDatacenters(ctx, params)
	case "/datacenters/detail":
		return c.renderDatacenterDetail(ctx, params)
	case "/datacenters/create":
		return c.renderDatacenterCreate(ctx, params)
	default:
		return nil, fmt.Errorf("unknown route %q: %w", route, contributor.ErrPageNotFound)
	}
}

// RenderWidget renders a widget by ID.
func (c *Contributor) RenderWidget(ctx context.Context, widgetID string) (templ.Component, error) {
	ctx = c.dashboardContext(ctx)

	switch widgetID {
	case "ctrlplane-system-stats":
		return c.renderSystemStatsWidget(ctx)
	case "ctrlplane-recent-deploys":
		return c.renderRecentDeploysWidget(ctx)
	case "ctrlplane-health-summary":
		return c.renderHealthSummaryWidget(ctx)
	case "ctrlplane-workers":
		return c.renderWorkersWidget(ctx)
	default:
		return nil, fmt.Errorf("unknown widget %q: %w", widgetID, contributor.ErrWidgetNotFound)
	}
}

// RenderSettings renders a settings panel by ID.
func (c *Contributor) RenderSettings(ctx context.Context, settingID string) (templ.Component, error) {
	ctx = c.dashboardContext(ctx)

	switch settingID {
	case "ctrlplane-config":
		return c.renderConfigSettings(ctx)
	default:
		return nil, fmt.Errorf("unknown setting %q: %w", settingID, contributor.ErrSettingNotFound)
	}
}

// --- Page Renderers ---

func (c *Contributor) renderOverview(ctx context.Context) (templ.Component, error) {
	stats, err := c.cp.Admin.SystemStats(ctx)
	if err != nil {
		return nil, fmt.Errorf("dashboard: fetch system stats: %w", err)
	}

	providers, err := c.cp.Admin.ListProviders(ctx)
	if err != nil {
		return nil, fmt.Errorf("dashboard: fetch providers: %w", err)
	}

	recent, err := c.cp.Instances.List(ctx, instance.ListOptions{Limit: 5})
	if err != nil {
		return nil, fmt.Errorf("dashboard: fetch recent instances: %w", err)
	}

	return pages.OverviewPage(stats, providers, recent.Items), nil
}

func (c *Contributor) renderInstances(ctx context.Context, params contributor.Params) (templ.Component, error) {
	limit := 20

	if v := params.QueryParams["limit"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	opts := instance.ListOptions{
		State:    params.QueryParams["state"],
		Label:    params.QueryParams["label"],
		Provider: params.QueryParams["provider"],
		Cursor:   params.QueryParams["cursor"],
		Limit:    limit,
	}

	result, err := c.cp.Instances.List(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("dashboard: list instances: %w", err)
	}

	return pages.InstancesPage(result), nil
}

func (c *Contributor) renderInstanceDetail(ctx context.Context, params contributor.Params) (templ.Component, error) {
	instanceIDStr := params.QueryParams["instance_id"]
	if instanceIDStr == "" {
		return nil, fmt.Errorf("dashboard: missing instance_id: %w", contributor.ErrPageNotFound)
	}

	instanceID, err := id.Parse(instanceIDStr)
	if err != nil {
		return nil, fmt.Errorf("dashboard: parse instance_id: %w", err)
	}

	// Handle actions before rendering.
	if action := params.QueryParams["action"]; action != "" {
		if err := c.handleInstanceAction(ctx, instanceID, action, params); err != nil {
			return nil, fmt.Errorf("dashboard: instance action %q: %w", action, err)
		}
	}

	inst, err := c.cp.Instances.Get(ctx, instanceID)
	if err != nil {
		return nil, fmt.Errorf("dashboard: get instance: %w", err)
	}

	tab := params.QueryParams["tab"]
	if tab == "" {
		tab = "info"
	}

	data := pages.InstanceDetailData{
		Instance: inst,
		Tab:      tab,
	}

	// Best-effort: surface the parent workload when this instance
	// was created as a replica. Failures collapse silently — the
	// header just doesn't show the link.
	if widLabel, ok := inst.Labels["ctrlplane.workload"]; ok && widLabel != "" {
		if wid, parseErr := id.Parse(widLabel); parseErr == nil {
			if w, getErr := c.cp.Workloads.Get(ctx, wid); getErr == nil {
				data.ParentWorkload = w
			}
		}
	}

	// Load tab-specific data.
	switch tab {
	case "deploys":
		deploys, listErr := c.cp.Deploys.ListDeployments(ctx, instanceID, deploy.ListOptions{Limit: 20})
		if listErr != nil {
			return nil, fmt.Errorf("dashboard: list deployments: %w", listErr)
		}

		data.Deployments = deploys

	case "releases":
		releases, listErr := c.cp.Deploys.ListReleases(ctx, instanceID, deploy.ListOptions{Limit: 20})
		if listErr != nil {
			return nil, fmt.Errorf("dashboard: list releases: %w", listErr)
		}

		data.Releases = releases

	case "health":
		ih, healthErr := c.cp.Health.GetHealth(ctx, instanceID)
		if healthErr != nil {
			return nil, fmt.Errorf("dashboard: get health: %w", healthErr)
		}

		data.Health = ih

		checks, checksErr := c.cp.Health.ListChecks(ctx, instanceID)
		if checksErr != nil {
			return nil, fmt.Errorf("dashboard: list health checks: %w", checksErr)
		}

		data.HealthChecks = checks

	case "network":
		domains, domErr := c.cp.Network.ListDomains(ctx, instanceID)
		if domErr != nil {
			return nil, fmt.Errorf("dashboard: list domains: %w", domErr)
		}

		data.Domains = domains

		routes, routeErr := c.cp.Network.ListRoutes(ctx, instanceID)
		if routeErr != nil {
			return nil, fmt.Errorf("dashboard: list routes: %w", routeErr)
		}

		data.Routes = routes

	case "secrets":
		secs, secErr := c.cp.Secrets.List(ctx, instanceID)
		if secErr != nil {
			return nil, fmt.Errorf("dashboard: list secrets: %w", secErr)
		}

		data.Secrets = secs

	case "telemetry":
		dash, telErr := c.cp.Telemetry.GetDashboard(ctx, instanceID)
		if telErr != nil {
			return nil, fmt.Errorf("dashboard: get telemetry dashboard: %w", telErr)
		}

		data.TelemetryDashboard = dash
	}

	return pages.InstanceDetailPage(data), nil
}

func (c *Contributor) handleInstanceAction(ctx context.Context, instanceID id.ID, action string, params contributor.Params) error {
	switch action {
	case "start":
		return c.cp.Instances.Start(ctx, instanceID)
	case "stop":
		return c.cp.Instances.Stop(ctx, instanceID)
	case "restart":
		return c.cp.Instances.Restart(ctx, instanceID)
	case "suspend":
		return c.cp.Instances.Suspend(ctx, instanceID, "suspended via dashboard")
	case "unsuspend":
		return c.cp.Instances.Unsuspend(ctx, instanceID)
	case "delete":
		return c.cp.Instances.Delete(ctx, instanceID)
	default:
		return nil
	}
}

// renderDeployments serves the /deployments page. Three shapes:
//
//  1. ?workload_id=... — aggregate deployments across the
//     workload's replicas (the workload-centric default).
//  2. ?instance_id=... — legacy per-instance view, kept for
//     deep-links from the instance detail page.
//  3. neither — render the workload-selection landing page so
//     users see something useful instead of "select an instance"
//     filled with replica names that look opaque.
func (c *Contributor) renderDeployments(ctx context.Context, params contributor.Params) (templ.Component, error) {
	limit := 20

	if v := params.QueryParams["limit"]; v != "" {
		if n, parseErr := strconv.Atoi(v); parseErr == nil && n > 0 {
			limit = n
		}
	}

	// Workload-centric path: aggregate deployments across the
	// workload's replicas. This is the default the user sees
	// when they click a workload row in the selection landing.
	if widStr := params.QueryParams["workload_id"]; widStr != "" {
		wid, err := id.Parse(widStr)
		if err != nil {
			return nil, fmt.Errorf("dashboard: parse workload_id: %w", err)
		}

		w, err := c.cp.Workloads.Get(ctx, wid)
		if err != nil {
			return nil, fmt.Errorf("dashboard: get workload for deployments: %w", err)
		}

		replicas, err := c.cp.Workloads.ListInstances(ctx, wid)
		if err != nil {
			return nil, fmt.Errorf("dashboard: list workload replicas: %w", err)
		}

		var all []*deploy.Deployment

		for _, r := range replicas {
			res, dErr := c.cp.Deploys.ListDeployments(ctx, r.ID, deploy.ListOptions{Limit: limit})
			if dErr != nil {
				continue
			}

			all = append(all, res.Items...)
		}

		result := &deploy.DeployListResult{Items: all, Total: len(all)}

		return pages.DeploymentsForWorkloadPage(w, result), nil
	}

	// Legacy per-instance path: kept so links from instance
	// detail keep working.
	if instanceIDStr := params.QueryParams["instance_id"]; instanceIDStr != "" {
		instanceID, err := id.Parse(instanceIDStr)
		if err != nil {
			return nil, fmt.Errorf("dashboard: parse instance_id: %w", err)
		}

		result, err := c.cp.Deploys.ListDeployments(ctx, instanceID, deploy.ListOptions{
			Cursor: params.QueryParams["cursor"],
			Limit:  limit,
		})
		if err != nil {
			return nil, fmt.Errorf("dashboard: list deployments: %w", err)
		}

		inst, err := c.cp.Instances.Get(ctx, instanceID)
		if err != nil {
			return nil, fmt.Errorf("dashboard: get instance for deployments: %w", err)
		}

		return pages.DeploymentsPage(inst, result), nil
	}

	// No selection yet — show the workload-selection landing.
	wResult, err := c.cp.Workloads.List(ctx, workload.ListOptions{Limit: 100})
	if err != nil {
		return nil, fmt.Errorf("dashboard: list workloads for deployments: %w", err)
	}

	return pages.DeploymentsSelectWorkloadPage(wResult.Items), nil
}

func (c *Contributor) renderDeploymentDetail(ctx context.Context, params contributor.Params) (templ.Component, error) {
	deployIDStr := params.QueryParams["deployment_id"]
	if deployIDStr == "" {
		return nil, fmt.Errorf("dashboard: missing deployment_id: %w", contributor.ErrPageNotFound)
	}

	deployID, err := id.Parse(deployIDStr)
	if err != nil {
		return nil, fmt.Errorf("dashboard: parse deployment_id: %w", err)
	}

	// Handle cancel action.
	if action := params.QueryParams["action"]; action == "cancel" {
		if cancelErr := c.cp.Deploys.Cancel(ctx, deployID); cancelErr != nil {
			return nil, fmt.Errorf("dashboard: cancel deployment: %w", cancelErr)
		}
	}

	dep, err := c.cp.Deploys.GetDeployment(ctx, deployID)
	if err != nil {
		return nil, fmt.Errorf("dashboard: get deployment: %w", err)
	}

	var rel *deploy.Release

	if !dep.ReleaseID.IsNil() {
		rel, err = c.cp.Deploys.GetRelease(ctx, dep.ReleaseID)
		if err != nil {
			return nil, fmt.Errorf("dashboard: get release: %w", err)
		}
	}

	return pages.DeploymentDetailPage(dep, rel), nil
}

func (c *Contributor) renderHealth(ctx context.Context, params contributor.Params) (templ.Component, error) {
	_ = params

	// Workload rollup leads — primary unit operators reason about
	// in the new model.
	wResult, err := c.cp.Workloads.List(ctx, workload.ListOptions{Limit: 100})
	if err != nil {
		return nil, fmt.Errorf("dashboard: list workloads for health: %w", err)
	}

	workloadRows := make([]pages.WorkloadHealthRow, 0, len(wResult.Items))
	for _, w := range wResult.Items {
		wh, hErr := c.cp.Workloads.GetHealth(ctx, w.ID)
		if hErr != nil {
			workloadRows = append(workloadRows, pages.WorkloadHealthRow{Workload: w})

			continue
		}

		workloadRows = append(workloadRows, pages.WorkloadHealthRow{Workload: w, Health: wh})
	}

	// Per-instance drill-down underneath.
	result, err := c.cp.Instances.List(ctx, instance.ListOptions{Limit: 100})
	if err != nil {
		return nil, fmt.Errorf("dashboard: list instances for health: %w", err)
	}

	instanceRows := make([]pages.InstanceHealthRow, 0, len(result.Items))
	for _, inst := range result.Items {
		ih, healthErr := c.cp.Health.GetHealth(ctx, inst.ID)
		if healthErr != nil {
			instanceRows = append(instanceRows, pages.InstanceHealthRow{
				Instance: inst,
				Health:   &health.InstanceHealth{Status: health.StatusUnknown},
			})

			continue
		}

		instanceRows = append(instanceRows, pages.InstanceHealthRow{
			Instance: inst,
			Health:   ih,
		})
	}

	return pages.HealthPage(workloadRows, instanceRows), nil
}

func (c *Contributor) renderNetwork(ctx context.Context, params contributor.Params) (templ.Component, error) {
	instanceIDStr := params.QueryParams["instance_id"]
	tab := params.QueryParams["tab"]

	if tab == "" {
		tab = "domains"
	}

	if instanceIDStr == "" {
		// Show instance selection.
		result, err := c.cp.Instances.List(ctx, instance.ListOptions{Limit: 100})
		if err != nil {
			return nil, fmt.Errorf("dashboard: list instances for network: %w", err)
		}

		return pages.NetworkSelectInstancePage(result.Items), nil
	}

	instanceID, err := id.Parse(instanceIDStr)
	if err != nil {
		return nil, fmt.Errorf("dashboard: parse instance_id: %w", err)
	}

	// Handle network actions.
	if action := params.QueryParams["action"]; action != "" {
		if handleErr := c.handleNetworkAction(ctx, action, params); handleErr != nil {
			return nil, fmt.Errorf("dashboard: network action %q: %w", action, handleErr)
		}
	}

	domains, err := c.cp.Network.ListDomains(ctx, instanceID)
	if err != nil {
		return nil, fmt.Errorf("dashboard: list domains: %w", err)
	}

	routes, err := c.cp.Network.ListRoutes(ctx, instanceID)
	if err != nil {
		return nil, fmt.Errorf("dashboard: list routes: %w", err)
	}

	inst, err := c.cp.Instances.Get(ctx, instanceID)
	if err != nil {
		return nil, fmt.Errorf("dashboard: get instance for network: %w", err)
	}

	return pages.NetworkPage(inst, domains, routes, tab), nil
}

func (c *Contributor) handleNetworkAction(ctx context.Context, action string, params contributor.Params) error {
	switch action {
	case "add_domain":
		hostname := params.QueryParams["hostname"]
		if hostname == "" {
			return nil
		}

		instanceIDStr := params.QueryParams["instance_id"]
		if instanceIDStr == "" {
			return nil
		}

		instanceID, err := id.Parse(instanceIDStr)
		if err != nil {
			return fmt.Errorf("parse instance_id: %w", err)
		}

		tlsEnabled := params.QueryParams["tls_enabled"] == "true"

		_, err = c.cp.Network.AddDomain(ctx, network.AddDomainRequest{
			InstanceID: instanceID,
			Hostname:   hostname,
			TLSEnabled: tlsEnabled,
		})

		return err

	case "verify_domain":
		domainIDStr := params.QueryParams["domain_id"]
		if domainIDStr == "" {
			return nil
		}

		domainID, err := id.Parse(domainIDStr)
		if err != nil {
			return fmt.Errorf("parse domain_id: %w", err)
		}

		_, err = c.cp.Network.VerifyDomain(ctx, domainID)

		return err

	case "remove_domain":
		domainIDStr := params.QueryParams["domain_id"]
		if domainIDStr == "" {
			return nil
		}

		domainID, err := id.Parse(domainIDStr)
		if err != nil {
			return fmt.Errorf("parse domain_id: %w", err)
		}

		return c.cp.Network.RemoveDomain(ctx, domainID)

	case "provision_cert":
		domainIDStr := params.QueryParams["domain_id"]
		if domainIDStr == "" {
			return nil
		}

		domainID, err := id.Parse(domainIDStr)
		if err != nil {
			return fmt.Errorf("parse domain_id: %w", err)
		}

		_, err = c.cp.Network.ProvisionCert(ctx, domainID)

		return err

	case "add_route":
		instanceIDStr := params.QueryParams["instance_id"]
		if instanceIDStr == "" {
			return nil
		}

		instanceID, err := id.Parse(instanceIDStr)
		if err != nil {
			return fmt.Errorf("parse instance_id: %w", err)
		}

		path := params.QueryParams["path"]
		if path == "" {
			return nil
		}

		port, portErr := strconv.Atoi(params.QueryParams["port"])
		if portErr != nil {
			return fmt.Errorf("parse port: %w", portErr)
		}

		protocol := params.QueryParams["protocol"]

		_, err = c.cp.Network.AddRoute(ctx, network.AddRouteRequest{
			InstanceID: instanceID,
			Path:       path,
			Port:       port,
			Protocol:   protocol,
		})

		return err

	case "remove_route":
		routeIDStr := params.QueryParams["route_id"]
		if routeIDStr == "" {
			return nil
		}

		routeID, err := id.Parse(routeIDStr)
		if err != nil {
			return fmt.Errorf("parse route_id: %w", err)
		}

		return c.cp.Network.RemoveRoute(ctx, routeID)

	default:
		return nil
	}
}

func (c *Contributor) renderSecrets(ctx context.Context, params contributor.Params) (templ.Component, error) {
	instanceIDStr := params.QueryParams["instance_id"]
	if instanceIDStr == "" {
		result, err := c.cp.Instances.List(ctx, instance.ListOptions{Limit: 100})
		if err != nil {
			return nil, fmt.Errorf("dashboard: list instances for secrets: %w", err)
		}

		return pages.SecretsSelectInstancePage(result.Items), nil
	}

	instanceID, err := id.Parse(instanceIDStr)
	if err != nil {
		return nil, fmt.Errorf("dashboard: parse instance_id: %w", err)
	}

	// Handle delete action.
	if action := params.QueryParams["action"]; action == "delete" {
		key := params.QueryParams["key"]
		if key != "" {
			if delErr := c.cp.Secrets.Delete(ctx, instanceID, key); delErr != nil {
				return nil, fmt.Errorf("dashboard: delete secret: %w", delErr)
			}
		}
	}

	secs, err := c.cp.Secrets.List(ctx, instanceID)
	if err != nil {
		return nil, fmt.Errorf("dashboard: list secrets: %w", err)
	}

	inst, err := c.cp.Instances.Get(ctx, instanceID)
	if err != nil {
		return nil, fmt.Errorf("dashboard: get instance for secrets: %w", err)
	}

	return pages.SecretsPage(inst, secs), nil
}

func (c *Contributor) renderTenants(ctx context.Context, params contributor.Params) (templ.Component, error) {
	limit := 20

	if v := params.QueryParams["limit"]; v != "" {
		if n, parseErr := strconv.Atoi(v); parseErr == nil && n > 0 {
			limit = n
		}
	}

	opts := admin.ListTenantsOptions{
		Status: params.QueryParams["status"],
		Cursor: params.QueryParams["cursor"],
		Limit:  limit,
	}

	result, err := c.cp.Admin.ListTenants(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("dashboard: list tenants: %w", err)
	}

	return pages.TenantsPage(result), nil
}

func (c *Contributor) renderTenantDetail(ctx context.Context, params contributor.Params) (templ.Component, error) {
	tenantID := params.QueryParams["tenant_id"]
	if tenantID == "" {
		return nil, fmt.Errorf("dashboard: missing tenant_id: %w", contributor.ErrPageNotFound)
	}

	// Handle actions.
	if action := params.QueryParams["action"]; action != "" {
		if err := c.handleTenantAction(ctx, tenantID, action); err != nil {
			return nil, fmt.Errorf("dashboard: tenant action %q: %w", action, err)
		}
	}

	tenant, err := c.cp.Admin.GetTenant(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("dashboard: get tenant: %w", err)
	}

	quota, err := c.cp.Admin.GetQuota(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("dashboard: get quota: %w", err)
	}

	return pages.TenantDetailPage(tenant, quota), nil
}

func (c *Contributor) handleTenantAction(ctx context.Context, tenantID string, action string) error {
	switch action {
	case "suspend":
		return c.cp.Admin.SuspendTenant(ctx, tenantID, "suspended via dashboard")
	case "unsuspend":
		return c.cp.Admin.UnsuspendTenant(ctx, tenantID)
	case "delete":
		return c.cp.Admin.DeleteTenant(ctx, tenantID)
	default:
		return nil
	}
}

func (c *Contributor) renderAuditLog(ctx context.Context, params contributor.Params) (templ.Component, error) {
	limit := 50

	if v := params.QueryParams["limit"]; v != "" {
		if n, parseErr := strconv.Atoi(v); parseErr == nil && n > 0 {
			limit = n
		}
	}

	query := admin.AuditQuery{
		TenantID: params.QueryParams["tenant_id"],
		ActorID:  params.QueryParams["actor_id"],
		Resource: params.QueryParams["resource"],
		Action:   params.QueryParams["action_type"],
		Limit:    limit,
	}

	if since := params.QueryParams["since"]; since != "" {
		if t, parseErr := time.Parse("2006-01-02", since); parseErr == nil {
			query.Since = t
		}
	}

	if until := params.QueryParams["until"]; until != "" {
		if t, parseErr := time.Parse("2006-01-02", until); parseErr == nil {
			query.Until = t
		}
	}

	result, err := c.cp.Admin.QueryAuditLog(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("dashboard: query audit log: %w", err)
	}

	return pages.AuditPage(result), nil
}

// --- Deploy Create & Rollback ---

func (c *Contributor) renderDeployCreate(ctx context.Context, params contributor.Params) (templ.Component, error) {
	data := pages.DeployFormData{}

	// If an instance_id is provided, pre-select that instance.
	if instIDStr := params.QueryParams["instance_id"]; instIDStr != "" {
		instID, err := id.Parse(instIDStr)
		if err == nil {
			inst, getErr := c.cp.Instances.Get(ctx, instID)
			if getErr == nil {
				data.Instance = inst
			}
		}
	}

	// Handle form submission.
	if params.FormData != nil && params.FormData["image"] != "" {
		instIDStr := params.FormData["instance_id"]

		instID, parseErr := id.Parse(instIDStr)
		if parseErr != nil {
			data.Error = "Invalid instance ID"
		}

		if data.Error == "" {
			image := params.FormData["image"]
			strategy := params.FormData["strategy"]

			if strategy == "" {
				strategy = "rolling"
			}

			env := make(map[string]string)

			req := deploy.DeployRequest{
				InstanceID: instID,
				Strategy:   strategy,
				Services: []provider.ServiceDeploySpec{{
					Name:  "main",
					Image: image,
					Env:   env,
				}},
				CommitSHA: params.FormData["commit_sha"],
				Notes:     params.FormData["notes"],
			}

			_, deployErr := c.cp.Deploys.Deploy(ctx, req)
			if deployErr != nil {
				data.Error = deployErr.Error()
			} else {
				data.Success = "Deployment created successfully"
				data.RedirectURL = "./deployments"
			}
		}

		return pages.DeployCreatePage(data), nil
	}

	// Load instances for the selector.
	if data.Instance == nil {
		result, err := c.cp.Instances.List(ctx, instance.ListOptions{Limit: 100})
		if err != nil {
			return nil, fmt.Errorf("dashboard: list instances for deploy create: %w", err)
		}

		data.Instances = result.Items
	}

	return pages.DeployCreatePage(data), nil
}

func (c *Contributor) renderDeployRollback(ctx context.Context, params contributor.Params) (templ.Component, error) {
	instanceIDStr := params.QueryParams["instance_id"]
	if instanceIDStr == "" {
		return nil, fmt.Errorf("dashboard: missing instance_id: %w", contributor.ErrPageNotFound)
	}

	instID, err := id.Parse(instanceIDStr)
	if err != nil {
		return nil, fmt.Errorf("dashboard: parse instance_id for rollback: %w", err)
	}

	inst, err := c.cp.Instances.Get(ctx, instID)
	if err != nil {
		return nil, fmt.Errorf("dashboard: get instance for rollback: %w", err)
	}

	data := pages.RollbackFormData{
		Instance: inst,
	}

	// Handle rollback action.
	if action := params.QueryParams["action"]; action == "rollback" {
		releaseIDStr := params.QueryParams["release_id"]
		if releaseIDStr != "" {
			releaseID, parseErr := id.Parse(releaseIDStr)
			if parseErr != nil {
				data.Error = "Invalid release ID"
			} else {
				if _, rollbackErr := c.cp.Deploys.Rollback(ctx, instID, releaseID); rollbackErr != nil {
					data.Error = rollbackErr.Error()
				} else {
					data.Success = "Rollback initiated successfully"
				}
			}
		}
	}

	// Load releases.
	releases, err := c.cp.Deploys.ListReleases(ctx, instID, deploy.ListOptions{Limit: 20})
	if err != nil {
		return nil, fmt.Errorf("dashboard: list releases for rollback: %w", err)
	}

	data.Releases = releases.Items

	return pages.DeployRollbackPage(data), nil
}

// --- Provider Pages ---

func (c *Contributor) renderProviders(ctx context.Context, params contributor.Params) (templ.Component, error) {
	_ = params

	providers, err := c.cp.Admin.ListProviders(ctx)
	if err != nil {
		return nil, fmt.Errorf("dashboard: list providers: %w", err)
	}

	return pages.ProvidersPage(pages.ProviderListData{
		Providers: providers,
	}), nil
}

func (c *Contributor) renderProviderDetail(ctx context.Context, params contributor.Params) (templ.Component, error) {
	providerName := params.QueryParams["provider"]
	if providerName == "" {
		return nil, fmt.Errorf("dashboard: missing provider name: %w", contributor.ErrPageNotFound)
	}

	providers, err := c.cp.Admin.ListProviders(ctx)
	if err != nil {
		return nil, fmt.Errorf("dashboard: list providers for detail: %w", err)
	}

	var found *admin.ProviderStatus

	for i := range providers {
		if providers[i].Name == providerName {
			found = &providers[i]

			break
		}
	}

	if found == nil {
		return nil, fmt.Errorf("dashboard: provider %q not found: %w", providerName, contributor.ErrPageNotFound)
	}

	data := pages.ProviderDetailData{
		Provider: *found,
	}

	// Handle actions.
	switch params.QueryParams["action"] {
	case "test_health":
		result, testErr := c.cp.Admin.TestProviderHealth(ctx, providerName)
		if testErr != nil {
			return nil, fmt.Errorf("dashboard: test provider health: %w", testErr)
		}

		data.HealthTest = result
	case "purge":
		summary, purgeErr := c.purgeProvider(ctx, providerName)

		data.PurgeSummary = summary
		if purgeErr != nil {
			data.PurgeError = purgeErr.Error()
		}
	}

	// Load instances for this provider.
	instResult, err := c.cp.Instances.List(ctx, instance.ListOptions{
		Provider: providerName,
		Limit:    50,
	})
	if err != nil {
		return nil, fmt.Errorf("dashboard: list instances for provider: %w", err)
	}

	data.Instances = instResult.Items

	return pages.ProviderDetailPage(data), nil
}

// purgeProvider tears down every workload (and orphan instance) on
// the named provider. Workloads are deleted first so their cascade
// covers most replicas; remaining instances (workload-less, e.g.
// older rows or partial provisioning) are swept directly.
//
// Returns a summary regardless of error: when one component fails
// to delete, the rest still get a chance, and the caller surfaces
// the partial result + the wrapped error so operators can retry.
func (c *Contributor) purgeProvider(ctx context.Context, providerName string) (*pages.PurgeSummary, error) {
	summary := &pages.PurgeSummary{Provider: providerName}

	// Phase 1: delete workloads (cascades to their replicas).
	workloads, err := c.cp.Workloads.List(ctx, workload.ListOptions{
		ProviderName: providerName,
		Limit:        1000,
	})
	if err != nil {
		return summary, fmt.Errorf("list workloads on provider %s: %w", providerName, err)
	}

	var workloadFailures []string

	for _, w := range workloads.Items {
		if delErr := c.cp.Workloads.Delete(ctx, w.ID); delErr != nil {
			workloadFailures = append(workloadFailures, fmt.Sprintf("%s: %v", w.Name, delErr))

			continue
		}

		summary.WorkloadsDeleted++
	}

	summary.WorkloadFailures = workloadFailures

	// Phase 2: sweep orphan instances (any that weren't owned by a
	// deleted workload — older rows, partial provisioning, etc).
	instResult, err := c.cp.Instances.List(ctx, instance.ListOptions{
		Provider: providerName,
		Limit:    1000,
	})
	if err != nil {
		return summary, fmt.Errorf("list instances on provider %s: %w", providerName, err)
	}

	var instanceFailures []string

	for _, inst := range instResult.Items {
		if delErr := c.cp.Instances.Delete(ctx, inst.ID); delErr != nil {
			instanceFailures = append(instanceFailures, fmt.Sprintf("%s: %v", inst.Name, delErr))

			continue
		}

		summary.InstancesDeleted++
	}

	summary.InstanceFailures = instanceFailures

	if len(workloadFailures) > 0 || len(instanceFailures) > 0 {
		return summary, fmt.Errorf("purge %s: %d workload + %d instance failure(s)",
			providerName, len(workloadFailures), len(instanceFailures))
	}

	return summary, nil
}

// --- Worker & Events Pages ---

func (c *Contributor) renderWorkers(ctx context.Context, params contributor.Params) (templ.Component, error) {
	_ = params

	scheduler := c.cp.Scheduler()
	if scheduler == nil {
		return pages.WorkersPage(pages.WorkersPageData{}), nil
	}

	workers := scheduler.Workers()

	return pages.WorkersPage(pages.WorkersPageData{
		Workers: workers,
	}), nil
}

func (c *Contributor) renderWorkerDetail(ctx context.Context, params contributor.Params) (templ.Component, error) {
	_ = ctx
	workerName := params.QueryParams["worker"]

	if workerName == "" {
		return nil, fmt.Errorf("dashboard: missing worker name: %w", contributor.ErrPageNotFound)
	}

	scheduler := c.cp.Scheduler()
	if scheduler == nil {
		return nil, fmt.Errorf("dashboard: scheduler not available: %w", contributor.ErrPageNotFound)
	}

	info, found := scheduler.WorkerByName(workerName)
	if !found {
		return nil, fmt.Errorf("dashboard: worker %q not found: %w", workerName, contributor.ErrPageNotFound)
	}

	return pages.WorkerDetailPage(pages.WorkerDetailData{
		Worker: info,
	}), nil
}

func (c *Contributor) renderEvents(ctx context.Context, params contributor.Params) (templ.Component, error) {
	_ = ctx
	filterType := params.QueryParams["type"]

	var eventTypes []event.Type

	switch filterType {
	case "instance":
		eventTypes = []event.Type{
			event.InstanceCreated, event.InstanceStarted, event.InstanceStopped,
			event.InstanceFailed, event.InstanceDeleted, event.InstanceScaled,
			event.InstanceSuspended, event.InstanceUnsuspended,
		}
	case "deploy":
		eventTypes = []event.Type{
			event.DeployStarted, event.DeploySucceeded,
			event.DeployFailed, event.DeployRolledBack,
		}
	case "health":
		eventTypes = []event.Type{
			event.HealthCheckPassed, event.HealthCheckFailed,
			event.HealthDegraded, event.HealthRecovered,
		}
	case "domain":
		eventTypes = []event.Type{
			event.DomainAdded, event.DomainVerified, event.DomainRemoved,
			event.CertProvisioned, event.CertExpiring,
		}
	case "tenant":
		eventTypes = []event.Type{
			event.TenantCreated, event.TenantSuspended,
			event.TenantDeleted, event.QuotaExceeded,
		}
	case "datacenter":
		eventTypes = []event.Type{
			event.DatacenterCreated, event.DatacenterUpdated,
			event.DatacenterDeleted, event.DatacenterStatusChanged,
		}
	case "route":
		eventTypes = []event.Type{
			event.RouteAdded, event.RouteUpdated, event.RouteRemoved,
		}
	}

	events := c.cp.Events().RecentEvents(100, eventTypes...)

	return pages.EventsPage(pages.EventsPageData{
		Events:     events,
		FilterType: filterType,
	}), nil
}

// --- Widget Renderers ---

func (c *Contributor) renderSystemStatsWidget(ctx context.Context) (templ.Component, error) {
	stats, err := c.cp.Admin.SystemStats(ctx)
	if err != nil {
		return nil, fmt.Errorf("dashboard: fetch system stats: %w", err)
	}

	return widgets.SystemStatsWidget(stats), nil
}

func (c *Contributor) renderRecentDeploysWidget(ctx context.Context) (templ.Component, error) {
	result, err := c.cp.Instances.List(ctx, instance.ListOptions{Limit: 5})
	if err != nil {
		return nil, fmt.Errorf("dashboard: list instances for recent deploys: %w", err)
	}

	allDeploys := make([]*deploy.Deployment, 0)

	for _, inst := range result.Items {
		deploys, listErr := c.cp.Deploys.ListDeployments(ctx, inst.ID, deploy.ListOptions{Limit: 2})
		if listErr != nil {
			continue
		}

		allDeploys = append(allDeploys, deploys.Items...)
	}

	// Limit to 5 most recent.
	if len(allDeploys) > 5 {
		allDeploys = allDeploys[:5]
	}

	return widgets.RecentDeploysWidget(allDeploys), nil
}

func (c *Contributor) renderHealthSummaryWidget(ctx context.Context) (templ.Component, error) {
	result, err := c.cp.Instances.List(ctx, instance.ListOptions{Limit: 100})
	if err != nil {
		return nil, fmt.Errorf("dashboard: list instances for health summary: %w", err)
	}

	var healthy, degraded, unhealthy, unknown int

	for _, inst := range result.Items {
		ih, healthErr := c.cp.Health.GetHealth(ctx, inst.ID)
		if healthErr != nil {
			unknown++

			continue
		}

		switch ih.Status {
		case health.StatusHealthy:
			healthy++
		case health.StatusDegraded:
			degraded++
		case health.StatusUnhealthy:
			unhealthy++
		default:
			unknown++
		}
	}

	return widgets.HealthSummaryWidget(
		components.HealthCounts{
			Healthy:   healthy,
			Degraded:  degraded,
			Unhealthy: unhealthy,
			Unknown:   unknown,
		},
	), nil
}

func (c *Contributor) renderWorkersWidget(ctx context.Context) (templ.Component, error) {
	_ = ctx

	scheduler := c.cp.Scheduler()
	if scheduler == nil {
		return widgets.WorkersWidget(nil), nil
	}

	return widgets.WorkersWidget(scheduler.Workers()), nil
}

// --- Template Pages ---

func (c *Contributor) renderTemplates(ctx context.Context, params contributor.Params) (templ.Component, error) {
	_ = params

	result, err := c.cp.Templates.List(ctx, template.ListOptions{Limit: 100})
	if err != nil {
		return nil, fmt.Errorf("dashboard: list templates: %w", err)
	}

	return pages.TemplatesPage(pages.TemplateListPageData{
		Templates: result,
	}), nil
}

func (c *Contributor) renderTemplateDetail(ctx context.Context, params contributor.Params) (templ.Component, error) {
	templateIDStr := params.QueryParams["template_id"]
	if templateIDStr == "" {
		return nil, fmt.Errorf("dashboard: missing template_id: %w", contributor.ErrPageNotFound)
	}

	templateID, err := id.Parse(templateIDStr)
	if err != nil {
		return nil, fmt.Errorf("dashboard: parse template_id: %w", err)
	}

	data := pages.TemplateDetailPageData{}

	// Handle delete action.
	if action := params.QueryParams["action"]; action == "delete" {
		if delErr := c.cp.Templates.Delete(ctx, templateID); delErr != nil {
			// Template not found or delete failed — show the templates list with error.
			result, listErr := c.cp.Templates.List(ctx, template.ListOptions{Limit: 100})
			if listErr != nil {
				return nil, fmt.Errorf("dashboard: list templates after failed delete: %w", listErr)
			}

			return pages.TemplatesPage(pages.TemplateListPageData{
				Templates: result,
			}), nil
		}

		// Redirect to templates list after successful deletion.
		result, listErr := c.cp.Templates.List(ctx, template.ListOptions{Limit: 100})
		if listErr != nil {
			return nil, fmt.Errorf("dashboard: list templates after delete: %w", listErr)
		}

		return pages.TemplatesPage(pages.TemplateListPageData{
			Templates: result,
		}), nil
	}

	tmpl, err := c.cp.Templates.Get(ctx, templateID)
	if err != nil {
		return nil, fmt.Errorf("dashboard: get template: %w", err)
	}

	data.Template = tmpl

	return pages.TemplateDetailPage(data), nil
}

func (c *Contributor) renderTemplateCreate(ctx context.Context, params contributor.Params) (templ.Component, error) {
	data := pages.TemplateFormData{}

	// Handle form submission. The form covers a single Main service
	// (image + cpu/mem + env/secrets/config-files). Sidecars / init
	// containers land in a future dashboard refresh.
	if params.FormData != nil && params.FormData["name"] != "" {
		labels := parseEnvJSON(params.FormData["labels_json"])

		main := provider.ServiceSpec{
			Name:  "main",
			Image: params.FormData["service_image"],
			Role:  provider.RoleMain,
			Resources: provider.ResourceSpec{
				CPUMillis: parseIntFormField(params.FormData["cpu_millis"]),
				MemoryMB:  parseIntFormField(params.FormData["memory_mb"]),
			},
			Env:         parseEnvJSON(params.FormData["env_json"]),
			Secrets:     parseSecretsJSON(params.FormData["secrets_json"]),
			ConfigFiles: parseConfigFilesJSON(params.FormData["config_files_json"]),
		}

		kind := provider.WorkloadKind(params.FormData["default_kind"])
		if kind == "" {
			kind = provider.KindDeployment
		}

		tmpl, createErr := c.cp.Templates.Create(ctx, template.CreateRequest{
			Name:            params.FormData["name"],
			Description:     params.FormData["description"],
			DefaultKind:     kind,
			DefaultStrategy: params.FormData["strategy"],
			Services:        []provider.ServiceSpec{main},
			Labels:          labels,
			Notes:           params.FormData["notes"],
		})
		if createErr != nil {
			data.Error = createErr.Error()

			return pages.TemplateFormPage(data), nil //nolint:nilerr // render form with validation error
		}

		return pages.TemplateDetailPage(pages.TemplateDetailPageData{
			Template:    tmpl,
			Success:     "Template created successfully",
			RedirectURL: "./templates/detail?template_id=" + tmpl.ID.String(),
		}), nil
	}

	return pages.TemplateFormPage(data), nil
}

func (c *Contributor) renderTemplateEdit(ctx context.Context, params contributor.Params) (templ.Component, error) {
	templateIDStr := params.QueryParams["template_id"]
	if templateIDStr == "" {
		return nil, fmt.Errorf("dashboard: missing template_id for edit: %w", contributor.ErrPageNotFound)
	}

	templateID, err := id.Parse(templateIDStr)
	if err != nil {
		return nil, fmt.Errorf("dashboard: parse template_id for edit: %w", err)
	}

	tmpl, err := c.cp.Templates.Get(ctx, templateID)
	if err != nil {
		return nil, fmt.Errorf("dashboard: get template for edit: %w", err)
	}

	data := pages.TemplateFormData{
		Template: tmpl,
	}

	// Handle form submission. Phase 1 edit covers Main service spec
	// only — non-Main services are preserved as-is on Update.
	if params.FormData != nil && params.FormData["name"] != "" {
		name := params.FormData["name"]
		description := params.FormData["description"]
		strategy := params.FormData["strategy"]
		notes := params.FormData["notes"]
		kind := provider.WorkloadKind(params.FormData["default_kind"])

		// Preserve existing services, but mutate the Main service's
		// image, resources, env, secrets, and config files from the
		// form. Non-Main services pass through untouched.
		services := append([]provider.ServiceSpec(nil), tmpl.Services...)

		for i := range services {
			if services[i].Role == provider.RoleMain || services[i].Role == "" {
				services[i].Image = params.FormData["service_image"]
				services[i].Resources = provider.ResourceSpec{
					CPUMillis: parseIntFormField(params.FormData["cpu_millis"]),
					MemoryMB:  parseIntFormField(params.FormData["memory_mb"]),
				}
				services[i].Env = parseEnvJSON(params.FormData["env_json"])
				services[i].Secrets = parseSecretsJSON(params.FormData["secrets_json"])
				services[i].ConfigFiles = parseConfigFilesJSON(params.FormData["config_files_json"])

				break
			}
		}

		updated, updateErr := c.cp.Templates.Update(ctx, templateID, template.UpdateRequest{
			Name:            &name,
			Description:     &description,
			DefaultKind:     &kind,
			DefaultStrategy: &strategy,
			Services:        services,
			Notes:           &notes,
		})
		if updateErr != nil {
			data.Error = updateErr.Error()

			return pages.TemplateFormPage(data), nil //nolint:nilerr // render form with validation error
		}

		// Show the updated template detail page with PRG redirect.
		return pages.TemplateDetailPage(pages.TemplateDetailPageData{
			Template:    updated,
			Success:     "Template updated successfully",
			RedirectURL: "./templates/detail?template_id=" + updated.ID.String(),
		}), nil
	}

	return pages.TemplateFormPage(data), nil
}

// parseEnvJSON deserializes the env_json form field into a map.
func parseEnvJSON(envJSON string) map[string]string {
	if envJSON == "" || envJSON == "{}" {
		return make(map[string]string)
	}

	env := make(map[string]string)

	if err := json.Unmarshal([]byte(envJSON), &env); err != nil {
		return make(map[string]string)
	}

	return env
}

// parsePortsJSON deserializes the ports_json form field into a slice of PortSpec.
func parsePortsJSON(portsJSON string) []provider.PortSpec {
	if portsJSON == "" || portsJSON == "[]" {
		return nil
	}

	var ports []provider.PortSpec

	if err := json.Unmarshal([]byte(portsJSON), &ports); err != nil {
		return nil
	}

	return ports
}

// parseVolumesJSON deserializes the volumes_json form field into a slice of VolumeSpec.
func parseVolumesJSON(volumesJSON string) []provider.VolumeSpec {
	if volumesJSON == "" || volumesJSON == "[]" {
		return nil
	}

	var volumes []provider.VolumeSpec

	if err := json.Unmarshal([]byte(volumesJSON), &volumes); err != nil {
		return nil
	}

	return volumes
}

// parseSecretsJSON deserializes the secrets_json form field into a slice of SecretRef.
func parseSecretsJSON(secretsJSON string) []template.SecretRef {
	if secretsJSON == "" || secretsJSON == "[]" {
		return nil
	}

	var refs []template.SecretRef

	if err := json.Unmarshal([]byte(secretsJSON), &refs); err != nil {
		return nil
	}

	return refs
}

// parseConfigFilesJSON deserializes the config_files_json form field into a slice of ConfigFile.
func parseConfigFilesJSON(configFilesJSON string) []template.ConfigFile {
	if configFilesJSON == "" || configFilesJSON == "[]" {
		return nil
	}

	var files []template.ConfigFile

	if err := json.Unmarshal([]byte(configFilesJSON), &files); err != nil {
		return nil
	}

	return files
}

// parseHealthCheckJSON deserializes the health_check_json form field into a HealthCheckSpec.
func parseHealthCheckJSON(healthCheckJSON string) *provider.HealthCheckSpec {
	if healthCheckJSON == "" || healthCheckJSON == "{}" || healthCheckJSON == "null" {
		return nil
	}

	var hc provider.HealthCheckSpec

	if err := json.Unmarshal([]byte(healthCheckJSON), &hc); err != nil {
		return nil
	}

	// Only return if at least port is set (meaningful health check).
	if hc.Port == 0 {
		return nil
	}

	return &hc
}

// parseIntFormField parses an integer from a form field, returning 0 on empty or error.
func parseIntFormField(value string) int {
	if value == "" {
		return 0
	}

	n, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}

	return n
}

// --- Datacenter Pages ---

func (c *Contributor) renderDatacenters(ctx context.Context, params contributor.Params) (templ.Component, error) {
	opts := datacenter.ListOptions{
		Status: params.QueryParams["status"],
		Limit:  100,
	}

	result, err := c.cp.Datacenters.List(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("dashboard: list datacenters: %w", err)
	}

	// Build instance counts per datacenter.
	instanceCounts := make(map[string]int)

	for _, dc := range result.Items {
		count, countErr := c.cp.Store().CountInstancesByDatacenter(ctx, dashboardClaims.TenantID, dc.ID)
		if countErr == nil {
			instanceCounts[dc.ID.String()] = count
		}
	}

	return pages.DatacentersPage(pages.DatacentersPageData{
		Datacenters:    result.Items,
		InstanceCounts: instanceCounts,
		Total:          result.Total,
		StatusFilter:   params.QueryParams["status"],
	}), nil
}

func (c *Contributor) renderDatacenterDetail(ctx context.Context, params contributor.Params) (templ.Component, error) {
	dcIDStr := params.QueryParams["datacenter_id"]
	if dcIDStr == "" {
		return nil, fmt.Errorf("dashboard: missing datacenter_id: %w", contributor.ErrPageNotFound)
	}

	dcID, err := id.Parse(dcIDStr)
	if err != nil {
		return nil, fmt.Errorf("dashboard: parse datacenter_id: %w", err)
	}

	// Handle actions before rendering.
	if action := params.QueryParams["action"]; action != "" {
		if handleErr := c.handleDatacenterAction(ctx, dcID, action, params.QueryParams); handleErr != nil {
			// If delete succeeded, redirect to list.
			if action == "delete" {
				return c.renderDatacenters(ctx, params)
			}

			return nil, fmt.Errorf("dashboard: datacenter action %q: %w", action, handleErr)
		}

		// After successful delete, redirect to list page.
		if action == "delete" {
			return c.renderDatacenters(ctx, params)
		}
	}

	dc, err := c.cp.Datacenters.Get(ctx, dcID)
	if err != nil {
		return nil, fmt.Errorf("dashboard: get datacenter: %w", err)
	}

	tab := params.QueryParams["tab"]
	if tab == "" {
		tab = "info"
	}

	data := pages.DatacenterDetailData{
		Datacenter: dc,
		Tab:        tab,
	}

	// Load instance count.
	count, countErr := c.cp.Store().CountInstancesByDatacenter(ctx, dashboardClaims.TenantID, dcID)
	if countErr == nil {
		data.InstanceCount = count
	}

	// Load instances for the instances tab.
	if tab == "instances" {
		instResult, instErr := c.cp.Instances.List(ctx, instance.ListOptions{
			Datacenter: dcID.String(),
			Limit:      50,
		})
		if instErr == nil {
			data.Instances = instResult.Items
		}
	}

	// Load bootstrap workloads for the services tab. Read-only —
	// the bootstrap reconciler is the only writer of these rows in
	// production; operators interact via the Redeploy action which
	// flips a Failed row back to Pending so the next reconcile tick
	// retries (handled by handleDatacenterAction).
	if tab == "services" && c.cp.Bootstraps != nil {
		bootstraps, bsErr := c.cp.Bootstraps.ListByDatacenter(ctx, dcID)
		if bsErr == nil {
			data.BootstrapServices = bootstraps
		}
	}

	return pages.DatacenterDetailPage(data), nil
}

func (c *Contributor) handleDatacenterAction(ctx context.Context, dcID id.ID, action string, qp map[string]string) error {
	switch action {
	case "set_active":
		return c.cp.Datacenters.SetStatus(ctx, dcID, datacenter.StatusActive)
	case "set_maintenance":
		return c.cp.Datacenters.SetStatus(ctx, dcID, datacenter.StatusMaintenance)
	case "set_draining":
		return c.cp.Datacenters.SetStatus(ctx, dcID, datacenter.StatusDraining)
	case "set_offline":
		return c.cp.Datacenters.SetStatus(ctx, dcID, datacenter.StatusOffline)
	case "delete":
		return c.cp.Datacenters.Delete(ctx, dcID)
	case "redeploy_bootstrap":
		return c.redeployBootstrap(ctx, qp["bootstrap_id"])
	default:
		return nil
	}
}

// redeployBootstrap flips a single bootstrap workload back into the
// Pending state so the reconciler retries Provision on its next
// tick. Operator escape hatch for rows stuck in StateFailed after
// transient provider errors — without this they'd still recover
// automatically, but the operator may not want to wait the full
// reconcile interval (default 60s).
//
// The action is a no-op when the bootstrap subsystem isn't wired
// (cp.Bootstraps == nil). Errors propagate up to the page so the
// dashboard surfaces them; on success the page re-renders the
// services tab via the same handler chain and the row appears in
// StatePending → StateProvisioning → StateRunning over the next few
// ticks.
func (c *Contributor) redeployBootstrap(ctx context.Context, bootstrapIDStr string) error {
	if c.cp.Bootstraps == nil {
		return nil
	}

	if bootstrapIDStr == "" {
		return errors.New("redeploy_bootstrap: missing bootstrap_id")
	}

	bootstrapID, err := id.Parse(bootstrapIDStr)
	if err != nil {
		return fmt.Errorf("redeploy_bootstrap: parse bootstrap_id: %w", err)
	}

	bw, err := c.cp.Bootstraps.Get(ctx, bootstrapID)
	if err != nil {
		return fmt.Errorf("redeploy_bootstrap: get %s: %w", bootstrapID, err)
	}

	// Only retry rows that have actually failed — flipping a
	// running row back to Pending would tear down a healthy
	// service. Quietly succeed on non-Failed rows so accidental
	// double-clicks are harmless.
	if bw.State != bootstrap.StateFailed {
		return nil
	}

	bw.State = bootstrap.StatePending
	bw.LastError = ""

	return c.cp.Store().UpdateBootstrap(ctx, bw)
}

func (c *Contributor) renderDatacenterCreate(ctx context.Context, params contributor.Params) (templ.Component, error) {
	data := pages.DatacenterFormData{}

	// Handle form submission.
	if params.FormData != nil && params.FormData["name"] != "" {
		var loc *datacenter.Location

		country := params.FormData["country"]
		city := params.FormData["city"]

		if country != "" || city != "" {
			loc = &datacenter.Location{
				Country: country,
				City:    city,
			}
		}

		var capSpec *datacenter.Capacity

		maxInst := parseIntFormField(params.FormData["max_instances"])
		maxCPU := parseIntFormField(params.FormData["max_cpu_millis"])
		maxMem := parseIntFormField(params.FormData["max_memory_mb"])

		if maxInst > 0 || maxCPU > 0 || maxMem > 0 {
			capSpec = &datacenter.Capacity{
				MaxInstances: maxInst,
				MaxCPUMillis: maxCPU,
				MaxMemoryMB:  maxMem,
			}
		}

		dc, createErr := c.cp.Datacenters.Create(ctx, datacenter.CreateRequest{
			Name:         params.FormData["name"],
			ProviderName: params.FormData["provider_name"],
			Region:       params.FormData["region"],
			Zone:         params.FormData["zone"],
			Location:     loc,
			Capacity:     capSpec,
		})
		if createErr != nil {
			data.Error = createErr.Error()
		} else {
			data.Success = "Datacenter created successfully"
			data.RedirectURL = "./datacenters/detail?datacenter_id=" + dc.ID.String()
		}

		// Reload providers for the form.
		providers, provErr := c.cp.Admin.ListProviders(ctx)
		if provErr == nil {
			data.Providers = providers
		}

		return pages.DatacenterFormPage(data), nil
	}

	// Load providers for the form.
	providers, err := c.cp.Admin.ListProviders(ctx)
	if err != nil {
		return nil, fmt.Errorf("dashboard: list providers for datacenter form: %w", err)
	}

	data.Providers = providers

	return pages.DatacenterFormPage(data), nil
}

// --- Settings Renderer ---

func (c *Contributor) renderConfigSettings(ctx context.Context) (templ.Component, error) {
	cfg := c.cp.Config()

	providers, err := c.cp.Admin.ListProviders(ctx)
	if err != nil {
		return nil, fmt.Errorf("dashboard: fetch providers for settings: %w", err)
	}

	return settings.ConfigPanel(cfg, providers), nil
}

// --- Workloads (list + detail) ---

// renderWorkloads is the /workloads list page. Cross-tenant via
// the dashboard's elevated context (set by dashboardContext).
// Filter by ?state= to narrow to e.g. only failed workloads.
//
// Supports an inline ?action=delete&workload_id=<id> handler so the
// per-row Delete button on the workloads list works without
// bouncing through the detail page first. After the delete the list
// is re-rendered showing the deletion took effect; failures bubble
// up so the user sees them rather than a silently stale list.
func (c *Contributor) renderWorkloads(ctx context.Context, params contributor.Params) (templ.Component, error) {
	if action := params.QueryParams["action"]; action == "delete" {
		widStr := params.QueryParams["workload_id"]
		if widStr == "" {
			return nil, errors.New("dashboard: workload delete: workload_id required")
		}

		wid, err := id.Parse(widStr)
		if err != nil {
			return nil, fmt.Errorf("dashboard: workload delete: parse workload_id: %w", err)
		}

		if delErr := c.cp.Workloads.Delete(ctx, wid); delErr != nil {
			// Surface the error so the user knows the row didn't go
			// away. The list still renders below with the workload
			// in StateFailed (Delete leaves it in place on partial
			// teardown failure), so the operator can retry.
			return nil, fmt.Errorf("dashboard: delete workload %s: %w", wid, delErr)
		}
	}

	limit := 50

	if v := params.QueryParams["limit"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	opts := workload.ListOptions{
		ProviderName: params.QueryParams["provider"],
		Region:       params.QueryParams["region"],
		Limit:        limit,
	}
	if state := params.QueryParams["state"]; state != "" {
		opts.State = workload.State(state)
	}

	result, err := c.cp.Workloads.List(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("dashboard: list workloads: %w", err)
	}

	return pages.WorkloadsPage(result), nil
}

// renderWorkloadDetail is the /workloads/detail page. Loads
// everything the tabbed view shows in one shot — cheap because
// each tab's underlying call is sub-millisecond.
//
// Supports an optional ?action=<verb> query param for the
// lifecycle buttons (restart / pause / resume / delete). Errors
// from the action are wrapped into the page error and surfaced
// to the user; success re-renders the detail view.
func (c *Contributor) renderWorkloadDetail(ctx context.Context, params contributor.Params) (templ.Component, error) {
	idStr := params.QueryParams["workload_id"]
	if idStr == "" {
		return nil, fmt.Errorf("dashboard: missing workload_id: %w", contributor.ErrPageNotFound)
	}

	wid, err := id.Parse(idStr)
	if err != nil {
		return nil, fmt.Errorf("dashboard: parse workload_id: %w", err)
	}

	if action := params.QueryParams["action"]; action != "" {
		if actErr := c.handleWorkloadAction(ctx, wid, action, params); actErr != nil {
			// Delete-failed-after-row-removed is the expected
			// "workload not found" race; treat it as a successful
			// teardown and bounce to the list.
			if action == "delete" {
				return c.renderWorkloads(ctx, params)
			}

			return nil, fmt.Errorf("dashboard: workload action %q: %w", action, actErr)
		}
		// After a successful delete, the workload row is gone.
		// Re-rendering the detail page would 404 — bounce to the
		// list instead so the user lands somewhere useful.
		if action == "delete" {
			return c.renderWorkloads(ctx, params)
		}
		// Fall through to re-render the detail view with the
		// post-action state.
	}

	w, err := c.cp.Workloads.Get(ctx, wid)
	if err != nil {
		return nil, fmt.Errorf("dashboard: get workload: %w", err)
	}

	tab := params.QueryParams["tab"]
	if tab == "" {
		tab = "replicas"
	}

	data := pages.WorkloadDetailData{Workload: w, Tab: tab}

	// Replicas — always loaded so the header replica-count
	// reads true even when the user is on a non-replicas tab.
	replicas, err := c.cp.Workloads.ListInstances(ctx, wid)
	if err != nil {
		return nil, fmt.Errorf("dashboard: list workload replicas: %w", err)
	}

	data.Replicas = replicas

	switch tab {
	case "deploys":
		if res, derr := c.cp.Workloads.ListDeployments(ctx, wid, deploy.ListOptions{Limit: 20}); derr == nil {
			data.Deployments = res
		}
	case "releases":
		if res, rerr := c.cp.Workloads.ListReleases(ctx, wid, deploy.ListOptions{Limit: 20}); rerr == nil {
			data.Releases = res
		}
	case "health":
		if wh, herr := c.cp.Workloads.GetHealth(ctx, wid); herr == nil {
			data.Health = wh
		}
	case "network":
		if ds, derr := c.cp.Workloads.ListDomains(ctx, wid); derr == nil {
			data.Domains = ds
		}

		if rs, rerr := c.cp.Workloads.ListRoutes(ctx, wid); rerr == nil {
			data.Routes = rs
		}
	}

	return pages.WorkloadDetailPage(data), nil
}

// handleWorkloadAction dispatches workload lifecycle verbs from
// the detail page's action buttons to the workload service.
//
// Scale takes a `replicas` query param; everything else is verb-
// only. Errors propagate so the caller can decide whether to
// re-render the detail page or bounce elsewhere (delete bounces
// to the list).
func (c *Contributor) handleWorkloadAction(ctx context.Context, wid id.ID, action string, params contributor.Params) error {
	switch action {
	case "restart":
		return c.cp.Workloads.Restart(ctx, wid)
	case "pause":
		return c.cp.Workloads.Pause(ctx, wid)
	case "resume":
		return c.cp.Workloads.Resume(ctx, wid)
	case "delete":
		return c.cp.Workloads.Delete(ctx, wid)
	case "scale":
		raw := params.QueryParams["replicas"]
		if raw == "" {
			return errors.New("scale: replicas query param required")
		}

		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return fmt.Errorf("scale: invalid replicas value %q", raw)
		}

		_, err = c.cp.Workloads.Scale(ctx, wid, n)

		return err
	default:
		return fmt.Errorf("unknown workload action %q", action)
	}
}

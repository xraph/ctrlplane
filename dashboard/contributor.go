package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/a-h/templ"
	"github.com/xraph/forge/extensions/dashboard/contributor"

	"github.com/xraph/ctrlplane/admin"
	"github.com/xraph/ctrlplane/app"
	"github.com/xraph/ctrlplane/auth"
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

// dashboardClaims are injected into every dashboard request context so that
// ctrlplane service calls (which require auth.RequireClaims) succeed.
// Dashboard access is already gated by the Forge dashboard auth layer.
var dashboardClaims = &auth.Claims{
	SubjectID: "dashboard",
	TenantID:  "default",
	Roles:     []string{"system:admin"},
}

// dashboardContext returns a context with dashboard-level admin claims
// injected. All ctrlplane service methods require auth claims, but the
// dashboard rendering context does not carry them by default.
func (c *Contributor) dashboardContext(ctx context.Context) context.Context {
	if auth.ClaimsFrom(ctx) != nil {
		return ctx
	}

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

func (c *Contributor) renderDeployments(ctx context.Context, params contributor.Params) (templ.Component, error) {
	instanceIDStr := params.QueryParams["instance_id"]
	if instanceIDStr == "" {
		// Show all instances and prompt user to select.
		result, err := c.cp.Instances.List(ctx, instance.ListOptions{Limit: 100})
		if err != nil {
			return nil, fmt.Errorf("dashboard: list instances for deployments: %w", err)
		}

		return pages.DeploymentsSelectInstancePage(result.Items), nil
	}

	instanceID, err := id.Parse(instanceIDStr)
	if err != nil {
		return nil, fmt.Errorf("dashboard: parse instance_id: %w", err)
	}

	limit := 20

	if v := params.QueryParams["limit"]; v != "" {
		if n, parseErr := strconv.Atoi(v); parseErr == nil && n > 0 {
			limit = n
		}
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

	result, err := c.cp.Instances.List(ctx, instance.ListOptions{Limit: 100})
	if err != nil {
		return nil, fmt.Errorf("dashboard: list instances for health: %w", err)
	}

	healthData := make([]pages.InstanceHealthRow, 0, len(result.Items))

	for _, inst := range result.Items {
		ih, healthErr := c.cp.Health.GetHealth(ctx, inst.ID)
		if healthErr != nil {
			healthData = append(healthData, pages.InstanceHealthRow{
				Instance: inst,
				Health:   &health.InstanceHealth{Status: health.StatusUnknown},
			})

			continue
		}

		healthData = append(healthData, pages.InstanceHealthRow{
			Instance: inst,
			Health:   ih,
		})
	}

	return pages.HealthPage(healthData), nil
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
				Image:      image,
				Strategy:   strategy,
				Env:        env,
				CommitSHA:  params.FormData["commit_sha"],
				Notes:      params.FormData["notes"],
			}

			// If deploying from a template, include config files and secrets.
			if tmplIDStr := params.FormData["template_id"]; tmplIDStr != "" {
				tmplID, tmplParseErr := id.Parse(tmplIDStr)
				if tmplParseErr == nil {
					selectedTemplate, tmplGetErr := c.cp.Deploys.GetTemplate(ctx, tmplID)
					if tmplGetErr == nil {
						req.ConfigFiles = selectedTemplate.ConfigFiles
						req.Secrets = selectedTemplate.Secrets
					}
				}
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

	// Load available templates.
	templates, err := c.cp.Deploys.ListTemplates(ctx, deploy.ListOptions{Limit: 100})
	if err == nil && templates.Total > 0 {
		data.Templates = templates.Items
	}

	// If a template_id is provided, pre-fill the form from that template.
	if tmplIDStr := params.QueryParams["template_id"]; tmplIDStr != "" {
		tmplID, parseErr := id.Parse(tmplIDStr)
		if parseErr == nil {
			tmpl, getErr := c.cp.Deploys.GetTemplate(ctx, tmplID)
			if getErr == nil {
				data.SelectedTemplate = tmpl
			}
		}
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

	// Handle test health action.
	if action := params.QueryParams["action"]; action == "test_health" {
		result, testErr := c.cp.Admin.TestProviderHealth(ctx, providerName)
		if testErr != nil {
			return nil, fmt.Errorf("dashboard: test provider health: %w", testErr)
		}

		data.HealthTest = result
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

	result, err := c.cp.Deploys.ListTemplates(ctx, deploy.ListOptions{Limit: 100})
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
		if delErr := c.cp.Deploys.DeleteTemplate(ctx, templateID); delErr != nil {
			// Template not found or delete failed — show the templates list with error.
			result, listErr := c.cp.Deploys.ListTemplates(ctx, deploy.ListOptions{Limit: 100})
			if listErr != nil {
				return nil, fmt.Errorf("dashboard: list templates after failed delete: %w", listErr)
			}

			return pages.TemplatesPage(pages.TemplateListPageData{
				Templates: result,
			}), nil
		}

		// Redirect to templates list after successful deletion.
		result, listErr := c.cp.Deploys.ListTemplates(ctx, deploy.ListOptions{Limit: 100})
		if listErr != nil {
			return nil, fmt.Errorf("dashboard: list templates after delete: %w", listErr)
		}

		return pages.TemplatesPage(pages.TemplateListPageData{
			Templates: result,
		}), nil
	}

	tmpl, err := c.cp.Deploys.GetTemplate(ctx, templateID)
	if err != nil {
		return nil, fmt.Errorf("dashboard: get template: %w", err)
	}

	data.Template = tmpl

	return pages.TemplateDetailPage(data), nil
}

func (c *Contributor) renderTemplateCreate(ctx context.Context, params contributor.Params) (templ.Component, error) {
	data := pages.TemplateFormData{}

	// Handle form submission.
	if params.FormData != nil && params.FormData["name"] != "" {
		env := parseEnvJSON(params.FormData["env_json"])
		labels := parseEnvJSON(params.FormData["labels_json"])
		annotations := parseEnvJSON(params.FormData["annotations_json"])
		ports := parsePortsJSON(params.FormData["ports_json"])
		volumes := parseVolumesJSON(params.FormData["volumes_json"])
		secretRefs := parseSecretsJSON(params.FormData["secrets_json"])
		configFiles := parseConfigFilesJSON(params.FormData["config_files_json"])
		healthCheck := parseHealthCheckJSON(params.FormData["health_check_json"])

		resources := provider.ResourceSpec{
			CPUMillis: parseIntFormField(params.FormData["cpu_millis"]),
			MemoryMB:  parseIntFormField(params.FormData["memory_mb"]),
			DiskMB:    parseIntFormField(params.FormData["disk_mb"]),
			Replicas:  parseIntFormField(params.FormData["replicas"]),
			GPU:       params.FormData["gpu"],
		}

		strategy := params.FormData["strategy"]
		if strategy == "" {
			strategy = "rolling"
		}

		tmpl, createErr := c.cp.Deploys.CreateTemplate(ctx, deploy.CreateTemplateRequest{
			Name:        params.FormData["name"],
			Description: params.FormData["description"],
			Image:       params.FormData["image"],
			Strategy:    strategy,
			Resources:   resources,
			Ports:       ports,
			Volumes:     volumes,
			HealthCheck: healthCheck,
			Env:         env,
			Secrets:     secretRefs,
			ConfigFiles: configFiles,
			Labels:      labels,
			Annotations: annotations,
			CommitSHA:   params.FormData["commit_sha"],
			Notes:       params.FormData["notes"],
		})
		if createErr != nil {
			data.Error = createErr.Error()

			return pages.TemplateFormPage(data), nil //nolint:nilerr // render form with validation error
		}

		// Show the created template detail page with PRG redirect.
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

	tmpl, err := c.cp.Deploys.GetTemplate(ctx, templateID)
	if err != nil {
		return nil, fmt.Errorf("dashboard: get template for edit: %w", err)
	}

	data := pages.TemplateFormData{
		Template: tmpl,
	}

	// Handle form submission.
	if params.FormData != nil && params.FormData["name"] != "" {
		env := parseEnvJSON(params.FormData["env_json"])
		labels := parseEnvJSON(params.FormData["labels_json"])
		annotations := parseEnvJSON(params.FormData["annotations_json"])
		ports := parsePortsJSON(params.FormData["ports_json"])
		volumes := parseVolumesJSON(params.FormData["volumes_json"])
		secretRefs := parseSecretsJSON(params.FormData["secrets_json"])
		configFiles := parseConfigFilesJSON(params.FormData["config_files_json"])
		healthCheck := parseHealthCheckJSON(params.FormData["health_check_json"])

		resources := provider.ResourceSpec{
			CPUMillis: parseIntFormField(params.FormData["cpu_millis"]),
			MemoryMB:  parseIntFormField(params.FormData["memory_mb"]),
			DiskMB:    parseIntFormField(params.FormData["disk_mb"]),
			Replicas:  parseIntFormField(params.FormData["replicas"]),
			GPU:       params.FormData["gpu"],
		}

		name := params.FormData["name"]
		description := params.FormData["description"]
		image := params.FormData["image"]
		strategy := params.FormData["strategy"]
		commitSHA := params.FormData["commit_sha"]
		notes := params.FormData["notes"]

		updated, updateErr := c.cp.Deploys.UpdateTemplate(ctx, templateID, deploy.UpdateTemplateRequest{
			Name:        &name,
			Description: &description,
			Image:       &image,
			Strategy:    &strategy,
			Resources:   &resources,
			Ports:       ports,
			Volumes:     volumes,
			HealthCheck: healthCheck,
			Env:         env,
			Secrets:     secretRefs,
			ConfigFiles: configFiles,
			Labels:      labels,
			Annotations: annotations,
			CommitSHA:   &commitSHA,
			Notes:       &notes,
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
func parseSecretsJSON(secretsJSON string) []deploy.SecretRef {
	if secretsJSON == "" || secretsJSON == "[]" {
		return nil
	}

	var refs []deploy.SecretRef

	if err := json.Unmarshal([]byte(secretsJSON), &refs); err != nil {
		return nil
	}

	return refs
}

// parseConfigFilesJSON deserializes the config_files_json form field into a slice of ConfigFile.
func parseConfigFilesJSON(configFilesJSON string) []deploy.ConfigFile {
	if configFilesJSON == "" || configFilesJSON == "[]" {
		return nil
	}

	var files []deploy.ConfigFile

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
		if handleErr := c.handleDatacenterAction(ctx, dcID, action); handleErr != nil {
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

	return pages.DatacenterDetailPage(data), nil
}

func (c *Contributor) handleDatacenterAction(ctx context.Context, dcID id.ID, action string) error {
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
	default:
		return nil
	}
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

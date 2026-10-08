# Ctrlplane dashboard migration

You will manage workloads first, then inspect their replicas, releases, health and network configuration in context. The React plugin joins the Go contributor as `ctrlplane` and mounts under `/@ctrlplane`.

## Source and Git audit

On 2026-10-08 both primary checkouts were on main. Ctrlplane's local 6a1b3cf detaches the templ dashboard from the extension. Fetched origin/main was 35a0dd1; its route persistence and API changes were applied as 5eede2f without a merge or branch switch. Focused network, API, SQLite, PostgreSQL and Mongo package tests passed. The clean route-proxy-fields worktree matched origin/main across the entire tracked tree and was removed after integration; its branch remains. pr32 is an earlier ancestor of that route branch and must not be replayed. The old fix/gosec-findings branch carries security annotations and dependency updates already present or superseded on main; its health/provider files match main and its CI advisory setting is already present. Preserve all branches. Forge Dashboard had concurrent changes in host, kit, Authsome, Core and Weave; those are outside this migration's ownership.

The numbered architecture documents listed in AGENTS.md are absent. Existing source is authoritative: app/controlplane.go wires services, audit recording, workers and provider health; auth/ defines claims and authorization; each domain's service interface defines operations and JSON tags. Existing _project_files designs describe multi-service and deployment sources. packages/plugin/PLAYBOOK.md was read in full before implementation.

## Authorization design

Remove the old dashboardClaims elevation. Every intent requires an authenticated dashboard subject and either its exact ctrlplane:read/ctrlplane:write scope or the existing system:admin role from the trusted principal. Map the subject, email, name, tenant claim and roles into fresh Ctrlplane claims. Do not manufacture roles or accept them from request payloads. Call the configured Ctrlplane authorization provider for each intent, including reads.

An absent tenant claim is allowed only for an explicitly authenticated system administrator. A present empty, nil or non-string claim refuses, including for administrators. A normal tenant principal always needs a nonempty tenant_id claim. There is no default tenant and no client-selected tenant override. Creates need a tenant even for administrators so new resources never land under an empty identity. Admin queries, tenant operations, workers, provider control, bootstrap retries and the global event buffer require system:admin. Tenant reads never use these global paths. Parent instance/workload ownership is checked before child reads and writes, since several services accept an instance ID without checking the parent. Every TypeID is checked against its entity prefix.

The demo provides an explicit development principal and a real authorization provider. Production must populate dashboard principal claims. NoopProvider does not make an anonymous contract request authorized. Test denial before data access, malformed claims, provider refusal, tenant identity isolation and foreign-parent writes.

## Contract and data design

Use typed Go inputs with domain DTOs and their snake_case JSON tags. Bind queries and commands separately in an embedded, validated manifest. Every command declares invalidations for all affected queries. Keep command errors inside confirmation dialogs, disable pending submission and reset errors on open. List inputs clamp limits. Existing cursor results retain next_cursor. Workload lists have no cursor in the service and must disclose a bounded result when total exceeds returned rows. Audit and recent events are bounded windows; never invent pagination or imply full history. Child collections have bounded parent selection and explicit refresh.

Do not expose DatabaseURL, secret values, vault internals or provider credentials. Config shows intervals, default provider, quota and configured flags only. Preserve multi-service arrays and typed deployment sources when editing templates. Missing health remains unknown. Propagate failed component reads. Mark bounded aggregations incomplete. Network verification and certificate issuance currently record domain state without proving external DNS/ACME integration; label those operations by the service behavior and do not imply live verification.

## Product design

Use the shared kit's typography, colors, compact PageHeader, ResourceTable, QueryBoundary, ConfirmDialog and illustrated ZeroState. No new palette or global density changes. Workload rows show state, replica count, provider, region and the Main image, with detail links to replicas and deployment activity. The table and its next action stay visible near the top. Detail pages use compact horizontal sections and related actions on the same row, wrapping at narrow widths. Failed and unhealthy states use destructive badges, healthy/running/active use outline, transitional states use secondary. Identifiers use small monospace text; names use medium weight. Every table caption includes the current row count. Absence uses NoneCell or Timestamp.

## Implementation and review plan

1. Reconcile the Git history and record the complete legacy inventory before removing anything.
2. Add the authorized Go contributor, typed service handlers, manifest and extension discovery hook. Verify dispatcher registration, read/write isolation and a real persistent backend.
3. Add packages/plugin-ctrlplane with domain routes, compact tables, details, commands and forms. Add minimal shell registration. Preserve concurrently edited shared files.
4. Register the actual Ctrlplane extension in forge-dashboard/demo, using a persistent local store, a deterministic provider and representative multi-tenant seed data. Start the Go server and React shell for review.
5. Check desktop and narrow layouts, writes followed by reads, refresh, denied reads/writes, failed commands and retry, filtered/empty/missing states and browser console errors. Run package checks, workspace tests and Go build/test/lint. Record any environment or pre-existing failures separately.
6. Retire dashboard/ in its own commit only after every legacy item is accounted for and live review passes. Until then it stays as the source reference and is not registered.

## Legacy inventory and current coverage

Every legacy renderer below has a replacement or an explicit behavior change. Migration means the route, fields and command binding exist; it does not mean every destructive action has been exercised against a cloud provider. The source paths are relative to this repository. Render-only helpers change to shared React components. Query-parameter mutations deliberately change to command intents. Redirect scripts change to host navigation.

- `dashboard/components/audit_table.templ`: migrated to ResourceTable with row counts, actual values, detail links and a shared empty state. Columns: Time, Actor, Action, Resource, Resource ID, IP Address; Renderers: AuditTable.
- `dashboard/components/bootstrap_state_badge.templ`: changed to shared Badge rendering with consistent state semantics. Renderers: BootstrapStateBadge.
- `dashboard/components/datacenter_status_badge.templ`: changed to shared Badge rendering with consistent state semantics. Renderers: DatacenterStatusBadge.
- `dashboard/components/datacenter_table.templ`: migrated to ResourceTable with row counts, actual values, detail links and a shared empty state. Columns: Name, Provider, Region, Zone, Status, Instances; Renderers: DatacenterTable.
- `dashboard/components/deploy_table.templ`: migrated to ResourceTable with row counts, actual values, detail links and a shared empty state. Columns: Instance, Image, Strategy, State, Initiator, Started; Renderers: DeploymentTable.
- `dashboard/components/domain_table.templ`: migrated to ResourceTable with row counts, actual values, detail links and a shared empty state. Columns: Hostname, Verified, TLS, DNS Target, Cert Expiry, Actions; Actions: verify_domain, provision_cert, remove_domain; Tabs: domains; Renderers: DomainTable.
- `dashboard/components/empty_state.templ`: changed to the shared kit ZeroState through its thin EmptyState adapter, with recovery actions inside the result. Renderers: EmptyState.
- `dashboard/components/env_var_editor.templ`: changed to a lazy CodeMirror JSON editor that preserves environment, ports, secret references and config files for every service. Columns: Key, Value; Renderers: EnvVarEditor.
- `dashboard/components/event_table.templ`: migrated to ResourceTable with row counts, actual values, detail links and a shared empty state. Columns: Time, Type, Tenant, Instance, Actor; Renderers: EventTable.
- `dashboard/components/event_type_badge.templ`: changed to shared Badge rendering with consistent state semantics. Renderers: EventTypeBadge.
- `dashboard/components/health_table.templ`: migrated to ResourceTable with row counts, actual values, detail links and a shared empty state. Columns: Name, Type, Target, Interval, Enabled, Actions; Actions: run_check, remove_check; Tabs: health; Renderers: HealthCheckTable, HealthSummaryRow.
- `dashboard/components/instance_table.templ`: migrated to ResourceTable with row counts, actual values, detail links and a shared empty state. Columns: Name, State, Provider, Region, Image, Created; Renderers: InstanceTable.
- `dashboard/components/prg_redirect.templ`: dropped; command results invalidate queries and use host navigation instead of an injected redirect script. Renderers: PRGRedirect.
- `dashboard/components/provider_card.templ`: changed to compact provider rows and details with observed health, nullable before the first check; the uncomputed instance placeholder is dropped. Renderers: ProviderCard.
- `dashboard/components/release_table.templ`: migrated to ResourceTable with row counts, actual values, detail links and a shared empty state. Columns: Version, Image, Status, Commit, Notes, Created; Renderers: ReleaseTable.
- `dashboard/components/resource_gauge.templ`: changed to observed resource snapshot details; uncomputed telemetry aggregate zeroes are dropped. Renderers: ResourceGauge.
- `dashboard/components/route_table.templ`: migrated to ResourceTable with row counts, actual values, detail links and a shared empty state. Columns: Path, Port, Protocol, Weight, Strip Prefix, Actions; Actions: remove_route; Tabs: routes; Renderers: RouteTable.
- `dashboard/components/secret_table.templ`: migrated to ResourceTable with row counts, actual values, detail links and a shared empty state. Columns: Key, Type, Version, Created, Actions; Actions: delete; Renderers: SecretTable.
- `dashboard/components/stat_card.templ`: changed to the shared StatGrid component. Renderers: StatCard.
- `dashboard/components/state_badge.templ`: changed to shared Badge rendering with consistent state semantics. Renderers: InstanceStateBadge, DeployStateBadge, HealthStatusBadge, TenantStatusBadge, WorkerStatusBadge, WorkloadStateBadge, BoolBadge.
- `dashboard/components/strategy_select.templ`: changed to a native select using the registered strategy names, including blue-green. Fields: strategy; Renderers: StrategySelect.
- `dashboard/components/template_card.templ`: changed to template list rows, service tables and full configuration details. Renderers: TemplateCard.
- `dashboard/components/tenant_table.templ`: migrated to ResourceTable with row counts, actual values, detail links and a shared empty state. Columns: Name, Slug, Plan, Status, Created; Renderers: TenantTable.
- `dashboard/components/worker_table.templ`: migrated to ResourceTable with row counts, actual values, detail links and a shared empty state. Columns: Name, Interval, Status, Last Run, Run Count, Last Error; Renderers: WorkerTable.
- `dashboard/components/workload_table.templ`: migrated to ResourceTable with row counts, actual values, detail links and a shared empty state. Columns: Name, State, Replicas, Provider, Region, Image, Created, Actions; Actions: delete; Renderers: WorkloadTable, workloadDeleteBtn.
- `dashboard/pages/audit.templ`: migrated to the corresponding React resource page and typed contract intents. Renderers: AuditPage.
- `dashboard/pages/datacenter_detail.templ`: changed to Info, Instances and Bootstrap services sections; location and capacity retain all fields in expandable JSON, with status and failed-only bootstrap retry commands. Columns: Name, State, Provider, Region, Kind, Attempts, Last Error, Actions; Actions: set_maintenance, set_active, set_offline, delete, redeploy_bootstrap; Tabs: info, instances, capacity, services; Renderers: DatacenterDetailPage, datacenterInfoCard, datacenterInstancesCard, bootstrapServicesCard, datacenterCapacityCard, capacityCard.
- `dashboard/pages/datacenter_form.templ`: migrated to the corresponding React resource page and typed contract intents. Fields: name, provider_name, region, zone, country, city, max_instances, max_cpu_millis, max_memory_mb; Renderers: DatacenterFormPage.
- `dashboard/pages/datacenters.templ`: migrated to the corresponding React resource page and typed contract intents. Renderers: DatacentersPage, statusFilterButton.
- `dashboard/pages/deploy_create.templ`: changed to an instance Deploy dialog with per-service image, environment and health-check overrides, strategy, commit SHA and notes. Fields: instance_id, image, commit_sha, notes; Renderers: DeployCreatePage.
- `dashboard/pages/deploy_rollback.templ`: changed to per-instance Releases and release detail commands targeting a saved release. Columns: Version, Image, Status, Created, Action; Actions: rollback; Renderers: DeployRollbackPage.
- `dashboard/pages/deployment_detail.templ`: migrated to the corresponding React resource page and typed contract intents. Actions: cancel; Renderers: DeploymentDetailPage.
- `dashboard/pages/deployments.templ`: migrated to the corresponding React resource page and typed contract intents. Columns: Name, State, Provider, Region; Renderers: DeploymentsSelectWorkloadPage, DeploymentsForWorkloadPage, DeploymentsSelectInstancePage, DeploymentsPage.
- `dashboard/pages/events.templ`: migrated to the corresponding React resource page and typed contract intents. Columns: Timestamp, Type, Tenant, Instance, Actor; Renderers: EventsPage, eventTypeBadge.
- `dashboard/pages/health.templ`: changed to bounded workload rollups and an instance health table; missing evidence stays unknown and failed component reads remain errors. Columns: Name, Status, Replicas, Healthy, Degraded, Unhealthy, Unknown, Instance, Uptime, Checks, Last Checked; Tabs: health; Renderers: HealthPage.
- `dashboard/pages/instance_detail.templ`: changed to Info, Deployments, Releases, Health, Domains, Routes, Checks, Secrets and Telemetry sections; endpoints use a table and nested service ports remain in their configuration editor. Columns: Container, Host, Protocol, URL, Port, Public; Renderers: InstanceDetailPage, instanceTab, instanceActions, actionButton, instanceInfoTab, instanceDeploysTab, instanceReleasesTab, instanceHealthTab, instanceNetworkTab, instanceSecretsTab, instanceTelemetryTab.
- `dashboard/pages/instances.templ`: migrated to the corresponding React resource page and typed contract intents. Renderers: InstancesPage.
- `dashboard/pages/network.templ`: changed to instance Domains and Routes sections with add/remove commands and full route proxy-field editing. Columns: Name, State, Provider, Region; Fields: hostname, tls_enabled, path, port, protocol; Actions: add_domain, add_route; Tabs: domains, routes; Renderers: NetworkSelectInstancePage, NetworkPage.
- `dashboard/pages/overview.templ`: migrated to the corresponding React resource page and typed contract intents. Renderers: OverviewPage.
- `dashboard/pages/provider_detail.templ`: changed to provider details and scoped instance selection with health-test output and confirmed purge; partial failures stay visible. Columns: Name, State, Provider, Created; Actions: test_health, purge; Renderers: ProviderDetailPage.
- `dashboard/pages/providers.templ`: migrated to the corresponding React resource page and typed contract intents. Actions: test_health; Renderers: ProvidersPage.
- `dashboard/pages/secrets.templ`: migrated to the corresponding React resource page and typed contract intents. Columns: Name, State, Provider, Region; Renderers: SecretsSelectInstancePage, SecretsPage.
- `dashboard/pages/template_detail.templ`: migrated to the corresponding React resource page and typed contract intents. Actions: delete; Renderers: TemplateDetailPage, templateServiceCard, templateServiceEnvSection, templateServiceSecretsSection, templateServiceConfigFilesSection.
- `dashboard/pages/template_form.templ`: migrated to the corresponding React resource page and typed contract intents. Fields: name, description, default_kind, service_image, cpu_millis, memory_mb, env_json, secrets_json, config_files_json, notes; Renderers: TemplateFormPage.
- `dashboard/pages/templates.templ`: migrated to the corresponding React resource page and typed contract intents. Columns: Name, Image, Strategy, Resources, Details, Created; Renderers: TemplatesPage.
- `dashboard/pages/tenant_detail.templ`: migrated to the corresponding React resource page and typed contract intents. Actions: suspend, unsuspend, delete; Renderers: TenantDetailPage, tenantActions, quotaLimit.
- `dashboard/pages/tenants.templ`: migrated to the corresponding React resource page and typed contract intents. Renderers: TenantsPage.
- `dashboard/pages/worker_detail.templ`: migrated to the corresponding React resource page and typed contract intents. Renderers: WorkerDetailPage.
- `dashboard/pages/workers.templ`: migrated to the corresponding React resource page and typed contract intents. Columns: Name, Interval, Status, Last Run, Run Count, Last Error; Renderers: WorkersPage.
- `dashboard/pages/workload_detail.templ`: migrated to the corresponding React resource page and typed contract intents. Columns: Image, Notes, Created; Fields: replicas; Actions: scale; Tabs: replicas; Renderers: WorkloadDetailPage, workloadTab, workloadActions, workloadActionBtn, workloadReplicasTab, workloadServicesTab, workloadServiceCard, workloadReleasesTab, workloadDeploysTab, workloadHealthTab, workloadNetworkTab.
- `dashboard/pages/workloads.templ`: migrated to the corresponding React resource page and typed contract intents. Renderers: WorkloadsPage.
- `dashboard/settings/config.templ`: changed to a curated read-only config page; DatabaseURL and credentials are omitted, and database_url_configured describes only the URL setting. Columns: Name, Region, Health, Instances, Capabilities; Renderers: ConfigPanel.
- `dashboard/widgets/health_summary.templ`: changed to workload health summaries and instance results, with bounded coverage disclosed. Renderers: HealthSummaryWidget.
- `dashboard/widgets/recent_deploys.templ`: changed to a sorted bounded collection on Overview, with incomplete results disclosed. Columns: Image, State, Strategy; Renderers: RecentDeploysWidget.
- `dashboard/widgets/system_stats.templ`: changed to three computed tenant/provider counters; uncomputed instance, deployment and health counters are dropped with a visible explanation. Renderers: SystemStatsWidget.
- `dashboard/widgets/workers_widget.templ`: changed to compact worker rows on Overview; intervals and run durations use readable units. Renderers: WorkersWidget.

## Service actions behind the legacy surfaces

- Instances: list state/label/provider/cursor/limit; info, deploys, releases, health checks, domains/routes, secret metadata, telemetry; start, stop, restart, suspend with reason, unsuspend, delete. Parent workload links come from ctrlplane.workload labels.
- Workloads: state/provider/region/limit, replica count and image; replicas, deployments, releases, health, domains/routes; restart, pause, resume, scale to a nonnegative replica count, delete. Preserve failed teardown errors rather than redirecting them away.
- Deployments: workload or instance selection, strategy/state/initiator/start time, release detail, errors and timing; create with image, strategy, commit SHA and notes; cancel; rollback to a saved release. Replace the legacy per-replica bounded unsorted rollup with the workload service's aggregate and disclose limits.
- Health: workload worst-of-replicas summary and per-instance health; healthy/degraded/unhealthy/unknown counts. Recent summary covers a bounded sample, not the whole installation.
- Network: choose instance, domains/routes tabs; hostname/TLS/token/verified and path/port/protocol/weight; add/remove domain, record verification, provision certificate, add/remove route. New proxy fields from fetched main must survive create/update, including service_name, hostname and TLS verification defaults.
- Secrets: choose instance, key/type/version metadata and deletion. Values never return in list/detail.
- Tenants: status/cursor/limit; name/slug/plan/status/created, detail and quota usage; suspend, unsuspend, delete. System administrator only.
- Audit: tenant, actor, resource, action and date range; time, actor, action, resource, resource ID and details. No audit cursor input exists, so display a bounded window.
- Providers: health/capabilities/location/instance counts, test health, linked instances; purge deletes workloads first and then orphan instances with partial failures. Purge requires explicit confirmation and must not silently truncate at 1000.
- Workers: scheduler presence, worker list and details, interval, run timing, duration and failures; no legacy control operation.
- Events: type groups for instance/deploy/health/domain/tenant/datacenter/route, time, actor, instance, payload. It is an in-process buffer, not persisted event history; global admin only.
- Templates: list/detail/create/edit/delete, Main image/resources/environment/secret references/config files, labels/notes/default kind/strategy. New authoring must preserve sidecars, init containers, variables and services/helm/manifests/argocd sources.
- Datacenters: status filter, provider/region/zone/location/capacity/labels/metadata and instance counts; info/instances/capacity/bootstrap services; create, delete, active/maintenance/draining/offline, retry a failed bootstrap only. Bootstrap reads/writes require platform authority.
- Widgets: system stats, recent deployments, bounded health summary and worker status become reusable React summary sections.
- Settings: read-only default provider, health interval, telemetry interval, per-tenant quota, audit setting, database configured flag and provider list. A configured URL is not proof of a connected database.

## Verification status

On 2026-10-08 the Ctrlplane root passed `go build ./...`, `go test ./...` and `golangci-lint run ./...` (zero issues). Contract tests use the real HTTP transport and Badger store. They cover anonymous and missing-scope denial, malformed tenant claims, configured policy refusal and failure, two-tenant isolation, foreign parent reads/writes, TypeID prefixes, secret redaction, route persistence and query invalidation. Badger workload tests reopen the database and exercise filtering, duplicate handling and scoped updates/deletes.

The React package passed typecheck, lint and workflow tests. The Go demo passed build and tests, including closing and reopening Badger, preserving edited workload/template data, preventing duplicate seeds and isolating two tenants. The isolated Ctrlplane shell also produced a production Vite bundle. Its configuration uses the shared host, entry, styles and proxy while excluding other plugins that are being edited concurrently.

Live browser review used the real Go demo and React shell. Every navigation surface rendered. Review covered workload replicas/deployments/releases/health/network tabs, template authoring, datacenter instances/bootstrap, provider observations, worker run counts, audit filters, event bounds, tenant details, secret metadata and curated settings. A workload scaled from two replicas to three. A template image edit retained its sidecar and survived a server restart. A negative scale command kept BAD_REQUEST inside its dialog; a corrected retry succeeded. A route was created and updated with hostname, service, weight, rewrite flags, upstream origin and TLS verification; browser refresh retained them. A failed bootstrap was requeued to pending through the live command.

A server-configured reader could read its workload but received PERMISSION_DENIED on restart and global tenant administration. A missing workload returned NOT_FOUND, separately from empty results. Desktop and 390-pixel layouts were inspected. The narrow workload list and template editor had no document overflow; wide tables and JSON use their own scrolling. Filtered empty results use the shared illustrated state and a reset action. The advanced service editor has syntax highlighting, folding and search.

Full shared-workspace checks were attempted. The continuing test run completed 22 of 24 package tasks; concurrent Cortex and host setup-screen tests failed. Shared typecheck failed on Cortex importing the nonexistent useNavigate export. Shared lint failed on concurrent Cortex and playground theme-provider changes. The shared shell TypeScript build failed on unowned design-preview AppSidebar scopes props and an implicit-any callback. These failures do not establish a passing shared workspace. They remain outside this migration's edits.

Cloud deployments, external DNS verification, ACME issuance, live container telemetry, remote exec and production credentials are unverified. The demo provider simulates operations, and its memory vault does not persist values. Destructive purge/delete operations are bound and guarded but were not exercised through the browser. The event buffer is intentionally transient. No walkthrough is registered, so no tour control is shown.

## Retirement mapping

`dashboard/contributor.go` and its old injected administrator claims are replaced by `extension/contract/` with trusted principal mapping and configured authorization. `dashboard/manifest.go` is replaced by the embedded contract manifest and React navigation. Package docs and page data structs are replaced by typed domain DTOs and the plugin's resource descriptors. Generated `_templ.go` files are removed with their source templates. The full source inventory above remains the audit trail after retirement.

The old dashboard was already detached from extension registration. After the focused checks and live review above, its source can be retired in a separate commit. Shared-workspace failures are recorded rather than represented as passing checks.

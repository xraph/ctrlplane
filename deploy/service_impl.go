package deploy

import (
	"context"
	"fmt"
	"time"

	gometrics "github.com/xraph/go-utils/metrics"
	"go.opentelemetry.io/otel/trace"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/dispatch"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/internal/obs"
	"github.com/xraph/ctrlplane/managed"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/secrets"
)

// service is the concrete implementation of Service.
type service struct {
	guard      *managed.Guard
	store      Store
	instStore  instance.Store
	providers  *provider.Registry
	events     event.Bus
	auth       auth.Provider
	vault      secrets.Vault
	strategies map[string]Strategy
	metrics    *obs.Metrics
}

// NewService creates a deploy service with the given dependencies.
func NewService(
	store Store,
	instStore instance.Store,
	providers *provider.Registry,
	events event.Bus,
	authProvider auth.Provider,
	vault secrets.Vault,
) *service {
	return &service{
		guard:      managed.NewGuard(instStore),
		store:      store,
		instStore:  instStore,
		providers:  providers,
		events:     events,
		auth:       authProvider,
		vault:      vault,
		strategies: make(map[string]Strategy),
	}
}

// SetVault replaces the vault backend used for config file storage.
func (s *service) SetVault(v secrets.Vault) {
	s.vault = v
}

// SetMetrics times every deployment and rollback, the provider calls their
// strategies make and the readiness waits that follow, on m. A nil m
// records nothing. In a Forge app the ctrlplane extension passes
// app.Metrics() in for you.
func (s *service) SetMetrics(m gometrics.Metrics) {
	s.metrics = obs.NewMetrics(m)
}

// RegisterStrategy adds a deployment strategy to the service.
func (s *service) RegisterStrategy(st Strategy) {
	s.strategies[st.Name()] = st
}

// Deploy creates a new release and deploys it to the instance.
func (s *service) Deploy(ctx context.Context, req DeployRequest) (*Deployment, error) {
	claims, err := auth.RequireAuthorized(ctx, s.auth, "deployments.create", req.InstanceID.String(), "")
	if err != nil {
		return nil, fmt.Errorf("deploy: authenticate: %w", err)
	}

	if err := s.guard.CheckInstance(ctx, managed.Key{TenantID: claims.TenantID, InstanceID: req.InstanceID}, "deployments.create"); err != nil {
		return nil, err
	}

	// Verify the instance exists.
	inst, err := s.instStore.GetByID(ctx, claims.TenantID, req.InstanceID)
	if err != nil {
		return nil, fmt.Errorf("deploy: get instance %s: %w", req.InstanceID, err)
	}

	// Determine the next release version.
	version, err := s.store.NextReleaseVersion(ctx, claims.TenantID, req.InstanceID)
	if err != nil {
		return nil, fmt.Errorf("deploy: next release version: %w", err)
	}

	// Build the new Release's per-service snapshot. Services listed in
	// req replace the prior Release's snapshot for that service name;
	// services not listed inherit from the prior Release.
	services, err := s.buildReleaseSnapshot(ctx, claims.TenantID, req.InstanceID, req.Services)
	if err != nil {
		return nil, fmt.Errorf("deploy: build release snapshot: %w", err)
	}

	// Create the immutable release snapshot.
	rel := &Release{
		Entity:     ctrlplane.NewEntity(id.PrefixRelease),
		TenantID:   claims.TenantID,
		InstanceID: req.InstanceID,
		Version:    version,
		Services:   services,
		Notes:      req.Notes,
		CommitSHA:  req.CommitSHA,
		Active:     true,
	}

	if err := s.store.InsertRelease(ctx, rel); err != nil {
		return nil, fmt.Errorf("deploy: insert release: %w", err)
	}

	// Choose the deployment strategy.
	strategy := req.Strategy
	if strategy == "" {
		strategy = "rolling"
	}

	// Initial per-service progress map: every service in this rollout
	// starts pending; the strategy bumps each entry as it runs.
	progress := make(map[string]string, len(req.Services))
	for _, sd := range req.Services {
		progress[sd.Name] = "pending"
	}

	// Create the deployment record.
	dep := &Deployment{
		Entity:          ctrlplane.NewEntity(id.PrefixDeployment),
		TenantID:        claims.TenantID,
		InstanceID:      req.InstanceID,
		ReleaseID:       rel.ID,
		State:           DeployPending,
		Strategy:        strategy,
		Services:        req.Services,
		ServiceProgress: progress,
		Initiator:       claims.SubjectID,
	}

	if err := s.store.InsertDeployment(ctx, dep); err != nil {
		return nil, fmt.Errorf("deploy: insert deployment: %w", err)
	}

	// Publish the deploy-started event.
	deployedNames := make([]string, len(req.Services))
	for i := range req.Services {
		deployedNames[i] = req.Services[i].Name
	}

	_ = s.events.Publish(ctx, event.NewEvent(event.DeployStarted, claims.TenantID).
		WithInstance(req.InstanceID).
		WithActor(claims.SubjectID).
		WithPayload(map[string]any{
			"deployment_id":     dep.ID.String(),
			"release_id":        rel.ID.String(),
			"services_deployed": deployedNames,
		}))

	// Persist any per-service ConfigFiles into the vault. ConfigFiles
	// live on the workload's Services spec; deployment time is when
	// they get written so the providers can mount them on next start.
	if s.vault != nil {
		for _, sd := range req.Services {
			_ = sd // stub: ServiceDeploySpec doesn't carry ConfigFiles today
		}
	}

	// Look up the strategy implementation.
	st, ok := s.strategies[strategy]
	if !ok {
		return nil, fmt.Errorf("deploy: unknown strategy %q: %w", strategy, ctrlplane.ErrDeploymentFailed)
	}

	// Resolve the infrastructure provider for this instance.
	pctx, prov, err := s.providers.Acquire(ctx, inst.ProviderName)
	if err != nil {
		return nil, fmt.Errorf("deploy: get provider %s: %w", inst.ProviderName, err)
	}

	// Transition to running state.
	now := time.Now().UTC()
	dep.State = DeployRunning
	dep.StartedAt = &now

	if err := s.store.UpdateDeployment(ctx, dep); err != nil {
		return nil, fmt.Errorf("deploy: update deployment to running: %w", err)
	}

	// Execute the deployment strategy. The OnServiceProgress callback
	// updates Deployment.ServiceProgress as the strategy advances each
	// service through its lifecycle so dashboards / observers can see
	// per-service granularity (especially useful for canary rollouts
	// that promote one service at a time).
	execErr := s.execute(ctx, pctx, st, prov, inst, dep, obs.ActionDeploy)

	finished := time.Now().UTC()
	dep.FinishedAt = &finished

	if execErr != nil {
		dep.State = DeployFailed
		dep.Error = execErr.Error()

		if updateErr := s.store.UpdateDeployment(ctx, dep); updateErr != nil {
			return nil, fmt.Errorf("deploy: update deployment after failure: %w", updateErr)
		}

		_ = s.events.Publish(ctx, event.NewEvent(event.DeployFailed, claims.TenantID).
			WithInstance(req.InstanceID).
			WithActor(claims.SubjectID).
			WithPayload(map[string]any{
				"deployment_id": dep.ID.String(),
				"error":         execErr.Error(),
			}))

		return dep, fmt.Errorf("deploy: strategy execute: %w", ctrlplane.ErrDeploymentFailed)
	}

	dep.State = DeploySucceeded

	if err := s.store.UpdateDeployment(ctx, dep); err != nil {
		return nil, fmt.Errorf("deploy: update deployment after success: %w", err)
	}

	_ = s.events.Publish(ctx, event.NewEvent(event.DeploySucceeded, claims.TenantID).
		WithInstance(req.InstanceID).
		WithActor(claims.SubjectID).
		WithPayload(map[string]any{
			"deployment_id": dep.ID.String(),
			"release_id":    rel.ID.String(),
		}))

	return dep, nil
}

// RecordInitial persists the v1 Release + a synthetic
// already-succeeded Deployment for a freshly-provisioned instance.
//
// Called from workload.spawnReplica after instance.Create returns
// successfully, so the dashboard's Deployments page reflects every
// running replica from the moment Create finishes — not just after
// the operator manually clicks "Deploy".
//
// Idempotent on (tenantID, instanceID): if any Release already
// exists for the instance we treat the v1 as already recorded and
// return the existing first Release. The caller (spawnReplica) is
// adoption-aware and may invoke us against an instance that
// already has a release history; we never insert a duplicate v1.
//
// The synthetic Deployment is stamped DeploySucceeded with
// strategy="initial" and ServiceProgress all "succeeded" — the
// container is already running, there is no rollout to drive,
// the row exists purely for audit and rollback continuity.
func (s *service) RecordInitial(ctx context.Context, instanceID id.ID) (*Release, error) {
	claims, err := auth.RequireAuthorized(ctx, s.auth, "deployments.create", instanceID.String(), "")
	if err != nil {
		return nil, fmt.Errorf("record initial release: authenticate: %w", err)
	}

	if err := s.guard.CheckInstance(ctx, managed.Key{TenantID: claims.TenantID, InstanceID: instanceID}, "deployments.create"); err != nil {
		return nil, err
	}

	inst, err := s.instStore.GetByID(ctx, claims.TenantID, instanceID)
	if err != nil {
		return nil, fmt.Errorf("record initial release: get instance %s: %w", instanceID, err)
	}

	// Idempotency guard: if a Release exists, the v1 is already
	// recorded. Return the existing first Release so callers can
	// treat success uniformly without re-checking.
	existing, listErr := s.store.ListReleases(ctx, claims.TenantID, instanceID, ListOptions{Limit: 1})
	if listErr == nil && existing != nil && len(existing.Items) > 0 {
		return existing.Items[0], nil
	}

	// Snapshot the instance's currently-running services as v1.
	// instance.Services is the source of truth here — it's what the
	// provider was asked to provision. Releases are immutable, so
	// every later Deploy will inherit un-listed services from this
	// row exactly as they ran on day one.
	snapshots := make([]provider.ServiceSnapshot, 0, len(inst.Services))

	for _, svc := range inst.Services {
		// Health checks, volumes and the like live on the workload
		// spec, not on the Release. Releases carry the bits a deploy
		// can change: image, env, command, args, ports and resources.
		snapshots = append(snapshots, provider.SnapshotOf(svc))
	}

	// Version bumps off NextReleaseVersion so a future legacy row
	// or out-of-band insert doesn't collide. Always 1 in the
	// fresh-instance path, but staying defensive costs nothing.
	version, err := s.store.NextReleaseVersion(ctx, claims.TenantID, instanceID)
	if err != nil {
		return nil, fmt.Errorf("record initial release: next version: %w", err)
	}

	rel := &Release{
		Entity:     ctrlplane.NewEntity(id.PrefixRelease),
		TenantID:   claims.TenantID,
		InstanceID: instanceID,
		Version:    version,
		Services:   snapshots,
		Notes:      "initial provisioning",
		Active:     true,
	}

	if err := s.store.InsertRelease(ctx, rel); err != nil {
		return nil, fmt.Errorf("record initial release: insert release: %w", err)
	}

	// Synthetic deployment row: state=succeeded immediately, since
	// the underlying container/pod is already up. ServiceProgress
	// reports every service as succeeded so per-service dashboards
	// don't render a half-rolled-out workload as "stuck".
	deploySpecs := make([]provider.ServiceDeploySpec, 0, len(inst.Services))
	progress := make(map[string]string, len(inst.Services))

	for _, svc := range inst.Services {
		deploySpecs = append(deploySpecs, provider.SnapshotOf(svc).DeploySpec())
		progress[svc.Name] = "succeeded"
	}

	now := time.Now().UTC()
	dep := &Deployment{
		Entity:          ctrlplane.NewEntity(id.PrefixDeployment),
		TenantID:        claims.TenantID,
		InstanceID:      instanceID,
		ReleaseID:       rel.ID,
		State:           DeploySucceeded,
		Strategy:        "initial",
		Services:        deploySpecs,
		ServiceProgress: progress,
		StartedAt:       &now,
		FinishedAt:      &now,
		Initiator:       claims.SubjectID,
	}

	if err := s.store.InsertDeployment(ctx, dep); err != nil {
		return nil, fmt.Errorf("record initial release: insert deployment: %w", err)
	}

	_ = s.events.Publish(ctx, event.NewEvent(event.DeploySucceeded, claims.TenantID).
		WithInstance(instanceID).
		WithActor(claims.SubjectID).
		WithPayload(map[string]any{
			"deployment_id": dep.ID.String(),
			"release_id":    rel.ID.String(),
			"strategy":      "initial",
			"version":       version,
		}))

	return rel, nil
}

// Rollback reverts to a specific release by creating a new deployment.
func (s *service) Rollback(ctx context.Context, instanceID id.ID, releaseID id.ID) (*Deployment, error) {
	claims, err := auth.RequireAuthorized(ctx, s.auth, "deployments.rollback", instanceID.String(), "")
	if err != nil {
		return nil, fmt.Errorf("rollback: authenticate: %w", err)
	}

	if err := s.guard.CheckInstance(ctx, managed.Key{TenantID: claims.TenantID, InstanceID: instanceID}, "deployments.rollback"); err != nil {
		return nil, err
	}

	// Verify the instance exists.
	inst, err := s.instStore.GetByID(ctx, claims.TenantID, instanceID)
	if err != nil {
		return nil, fmt.Errorf("rollback: get instance %s: %w", instanceID, err)
	}

	// Retrieve the original release to roll back to.
	rel, err := s.store.GetRelease(ctx, claims.TenantID, releaseID)
	if err != nil {
		return nil, fmt.Errorf("rollback: get release %s: %w", releaseID, err)
	}

	if rel.TenantID != claims.TenantID || rel.InstanceID != instanceID {
		return nil, ctrlplane.ErrForbidden
	}

	// Rollback restores every service in the target Release —
	// translate each ServiceSnapshot into a ServiceDeploySpec.
	services := make([]provider.ServiceDeploySpec, len(rel.Services))

	for i, snap := range rel.Services {
		services[i] = snap.DeploySpec()
	}

	progress := make(map[string]string, len(services))
	for _, sd := range services {
		progress[sd.Name] = "pending"
	}

	// Create a rollback deployment using the recreate strategy.
	dep := &Deployment{
		Entity:          ctrlplane.NewEntity(id.PrefixDeployment),
		TenantID:        claims.TenantID,
		InstanceID:      instanceID,
		ReleaseID:       releaseID,
		State:           DeployPending,
		Strategy:        "recreate",
		Services:        services,
		ServiceProgress: progress,
		Initiator:       claims.SubjectID,
	}

	if err := s.store.InsertDeployment(ctx, dep); err != nil {
		return nil, fmt.Errorf("rollback: insert deployment: %w", err)
	}

	// Publish the deploy-started event.
	_ = s.events.Publish(ctx, event.NewEvent(event.DeployStarted, claims.TenantID).
		WithInstance(instanceID).
		WithActor(claims.SubjectID).
		WithPayload(map[string]any{
			"deployment_id": dep.ID.String(),
			"release_id":    releaseID.String(),
			"rollback":      true,
		}))

	// Look up the recreate strategy.
	st, ok := s.strategies["recreate"]
	if !ok {
		return nil, fmt.Errorf("rollback: unknown strategy %q: %w", "recreate", ctrlplane.ErrDeploymentFailed)
	}

	// Resolve the infrastructure provider.
	pctx, prov, err := s.providers.Acquire(ctx, inst.ProviderName)
	if err != nil {
		return nil, fmt.Errorf("rollback: get provider %s: %w", inst.ProviderName, err)
	}

	// Transition to running state.
	now := time.Now().UTC()
	dep.State = DeployRunning
	dep.StartedAt = &now

	if err := s.store.UpdateDeployment(ctx, dep); err != nil {
		return nil, fmt.Errorf("rollback: update deployment to running: %w", err)
	}

	// Execute the rollback deployment.
	execErr := s.execute(ctx, pctx, st, prov, inst, dep, obs.ActionRollback)

	finished := time.Now().UTC()
	dep.FinishedAt = &finished

	if execErr != nil {
		dep.State = DeployFailed
		dep.Error = execErr.Error()

		if updateErr := s.store.UpdateDeployment(ctx, dep); updateErr != nil {
			return nil, fmt.Errorf("rollback: update deployment after failure: %w", updateErr)
		}

		_ = s.events.Publish(ctx, event.NewEvent(event.DeployFailed, claims.TenantID).
			WithInstance(instanceID).
			WithActor(claims.SubjectID).
			WithPayload(map[string]any{
				"deployment_id": dep.ID.String(),
				"error":         execErr.Error(),
			}))

		return dep, fmt.Errorf("rollback: strategy execute: %w", ctrlplane.ErrDeploymentFailed)
	}

	dep.State = DeploySucceeded

	if err := s.store.UpdateDeployment(ctx, dep); err != nil {
		return nil, fmt.Errorf("rollback: update deployment after success: %w", err)
	}

	// Publish the rolled-back event on success.
	_ = s.events.Publish(ctx, event.NewEvent(event.DeployRolledBack, claims.TenantID).
		WithInstance(instanceID).
		WithActor(claims.SubjectID).
		WithPayload(map[string]any{
			"deployment_id": dep.ID.String(),
			"release_id":    releaseID.String(),
		}))

	return dep, nil
}

// Cancel aborts an in-progress deployment.
func (s *service) Cancel(ctx context.Context, deploymentID id.ID) error {
	claims, err := auth.RequireAuthorized(ctx, s.auth, "deployments.cancel", deploymentID.String(), "")
	if err != nil {
		return fmt.Errorf("cancel: authenticate: %w", err)
	}

	dep, err := s.store.GetDeployment(ctx, claims.TenantID, deploymentID)
	if err != nil {
		return fmt.Errorf("cancel: get deployment %s: %w", deploymentID, err)
	}

	if dep.TenantID != claims.TenantID {
		return ctrlplane.ErrForbidden
	}

	if err := s.guard.CheckInstance(ctx, managed.Key{TenantID: claims.TenantID, InstanceID: dep.InstanceID}, "deployments.cancel"); err != nil {
		return err
	}

	if dep.State != DeployPending && dep.State != DeployRunning {
		return fmt.Errorf("cancel: deployment in state %s: %w", dep.State, ctrlplane.ErrInvalidState)
	}

	now := time.Now().UTC()
	dep.State = DeployCancelled
	dep.FinishedAt = &now

	if err := s.store.UpdateDeployment(ctx, dep); err != nil {
		return fmt.Errorf("cancel: update deployment: %w", err)
	}

	return nil
}

// GetDeployment returns a specific deployment.
func (s *service) GetDeployment(ctx context.Context, deploymentID id.ID) (*Deployment, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("get deployment: authenticate: %w", err)
	}

	dep, err := s.store.GetDeployment(ctx, claims.TenantID, deploymentID)
	if err != nil {
		return nil, fmt.Errorf("get deployment %s: %w", deploymentID, err)
	}

	return dep, nil
}

// ListDeployments lists deployments for an instance.
func (s *service) ListDeployments(ctx context.Context, instanceID id.ID, opts ListOptions) (*DeployListResult, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("list deployments: authenticate: %w", err)
	}

	result, err := s.store.ListDeployments(ctx, claims.TenantID, instanceID, opts)
	if err != nil {
		return nil, fmt.Errorf("list deployments for instance %s: %w", instanceID, err)
	}

	return result, nil
}

// GetRelease returns a specific release.
func (s *service) GetRelease(ctx context.Context, releaseID id.ID) (*Release, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("get release: authenticate: %w", err)
	}

	rel, err := s.store.GetRelease(ctx, claims.TenantID, releaseID)
	if err != nil {
		return nil, fmt.Errorf("get release %s: %w", releaseID, err)
	}

	return rel, nil
}

// ListReleases lists releases for an instance.
func (s *service) ListReleases(ctx context.Context, instanceID id.ID, opts ListOptions) (*ReleaseListResult, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("list releases: authenticate: %w", err)
	}

	result, err := s.store.ListReleases(ctx, claims.TenantID, instanceID, opts)
	if err != nil {
		return nil, fmt.Errorf("list releases for instance %s: %w", instanceID, err)
	}

	return result, nil
}

// buildReleaseSnapshot constructs the per-service Services slice for a
// new Release. Services in `updates` provide the new image/env;
// services not listed inherit their snapshot from the prior Release
// for the same instance (the most recent one with Active=true). When
// no prior Release exists (this is the first Deploy), the snapshot
// contains only the services in `updates`.
//
// This keeps Releases self-contained: rollback always has the full
// multi-service snapshot to restore from a single Release row.
func (s *service) buildReleaseSnapshot(ctx context.Context, tenantID string, instanceID id.ID, updates []provider.ServiceDeploySpec) ([]provider.ServiceSnapshot, error) {
	prior, err := s.store.ListReleases(ctx, tenantID, instanceID, ListOptions{Limit: 1})
	if err != nil {
		return nil, fmt.Errorf("look up prior release: %w", err)
	}

	var previous []provider.ServiceSnapshot
	if prior != nil && len(prior.Items) > 0 {
		previous = prior.Items[0].Services
	}

	previousByName := make(map[string]*provider.ServiceSnapshot, len(previous))
	for i := range previous {
		previousByName[previous[i].Name] = &previous[i]
	}

	out := make([]provider.ServiceSnapshot, 0, len(updates))
	covered := make(map[string]struct{}, len(updates))

	for _, u := range updates {
		// Command, args, ports and resources the update leaves unset
		// carry over from the prior snapshot of the same service.
		out = append(out, u.Snapshot(previousByName[u.Name]))
		covered[u.Name] = struct{}{}
	}

	if len(previous) > 0 {
		for _, prev := range previous {
			if _, replaced := covered[prev.Name]; replaced {
				continue
			}

			out = append(out, prev)
		}
	}

	return out, nil
}

// execute runs dep's strategy against prov, stores any endpoints the
// provider returned and waits for readiness. action is deploy or rollback.
// The whole rollout is one "execute deployment" span under a traced
// context, and one observation of the deployment duration histogram.
//
// ctx is used for the store, pctx (which may carry credentials) for the
// provider.
func (s *service) execute(ctx, pctx context.Context, st Strategy, prov provider.Provider, inst *instance.Instance, dep *Deployment, action string) error {
	started := time.Now()

	pctx, span := obs.StartChild(pctx, "execute deployment", trace.WithAttributes(
		obs.AttrDeploymentID.String(dep.ID.String()),
		obs.AttrInstanceID.String(dep.InstanceID.String()),
		obs.AttrDeploymentStrategy.String(st.Name()),
		obs.AttrDeploymentRollback.Bool(action == obs.ActionRollback),
		obs.AttrDeploymentServices.Int(len(dep.Services)),
		obs.AttrProviderType.String(provider.TypeName(prov)),
	))
	defer span.End()

	// The store calls below hang off the rollout span too.
	ctx = trace.ContextWithSpan(ctx, span)

	var endpoints []provider.Endpoint

	execErr := st.Execute(pctx, StrategyParams{
		Deployment: dep,
		Provider:   prov,
		OnProgress: func(string, int, string) {},
		OnServiceProgress: func(serviceName, state string) {
			if dep.ServiceProgress == nil {
				dep.ServiceProgress = make(map[string]string, 1)
			}

			dep.ServiceProgress[serviceName] = state

			// Best-effort persist; a failed update doesn't fail the
			// rollout itself — the in-memory state still drives the
			// final UpdateDeployment call below.
			_ = s.store.UpdateDeployment(ctx, dep)
		},
		OnEndpoints: func(eps []provider.Endpoint) { endpoints = eps },
		metrics:     s.metrics,
	})
	if err := s.recordEndpoints(ctx, dep.TenantID, dep.InstanceID, endpoints); err != nil && execErr == nil {
		execErr = err
	}

	if execErr == nil {
		execErr = s.awaitReady(ctx, pctx, prov, inst, dep)
	}

	obs.Fail(span, execErr)
	s.metrics.DeploymentFinished(st.Name(), action, time.Since(started), execErr)

	return execErr
}

// recordEndpoints stores the endpoints a provider returned from Deploy
// on the instance, replacing what it had. Nil means the provider left
// them unchanged, and nothing is written. It runs whether or not the
// strategy succeeded, because a provider that returned endpoints has
// already changed how the instance is reached. The instance is read
// again first so changes made while the rollout ran aren't lost.
func (s *service) recordEndpoints(ctx context.Context, tenantID string, instanceID id.ID, endpoints []provider.Endpoint) error {
	if endpoints == nil {
		return nil
	}

	inst, err := s.instStore.GetByID(ctx, tenantID, instanceID)
	if err != nil {
		return fmt.Errorf("deploy: reload instance for endpoints: %w", err)
	}

	inst.Endpoints = endpoints
	inst.UpdatedAt = time.Now().UTC()

	if err := s.instStore.Update(ctx, inst); err != nil {
		return fmt.Errorf("deploy: update instance endpoints: %w", err)
	}

	return nil
}

// awaitReady holds a deployment open until an asynchronous provider
// reports the instance ready again. Providers that do not implement
// provider.ReadinessWaiter return straight away. When the wait fails,
// every service the strategy marked succeeded is marked failed, so the
// progress map agrees with the deployment's state. ctx is used for the
// store, pctx (which may carry credentials) for the provider.
func (s *service) awaitReady(ctx, pctx context.Context, prov provider.Provider, inst *instance.Instance, dep *Deployment) error {
	waiter, ok := prov.(provider.ReadinessWaiter)
	if !ok {
		return nil
	}

	started := time.Now()

	st, err := provider.WaitReady(pctx, waiter.ReadinessPolicy(), func(ctx context.Context) (*provider.InstanceStatus, error) {
		ctx, call := s.metrics.StartProviderCall(ctx, provider.TypeName(prov), obs.OpStatus,
			obs.AttrInstanceID.String(inst.ID.String()))
		st, err := dispatch.Status(ctx, prov, inst.Source.Type, inst.ID)
		call.End(err)

		return st, err
	})

	s.metrics.ReadinessWaited(provider.ReadinessOutcome(st, err), time.Since(started))

	if err == nil {
		return nil
	}

	for name, state := range dep.ServiceProgress {
		if state == ServiceStateSucceeded {
			dep.ServiceProgress[name] = ServiceStateFailed
		}
	}

	_ = s.store.UpdateDeployment(ctx, dep)

	return fmt.Errorf("wait for readiness: %w", err)
}

// UsesManagedAuthority confirms the persistence object used by the protection guard.
func (s *service) UsesManagedAuthority(candidate any) bool {
	return s != nil && s.guard.UsesAuthority(candidate)
}

// UsesAuthorizationProvider confirms the policy object used for actual mutations.
func (s *service) UsesAuthorizationProvider(candidate auth.Provider) bool {
	return s != nil && managed.SameAuthority(s.auth, candidate)
}

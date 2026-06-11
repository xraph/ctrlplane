package instance

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/dispatch"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/render"
	"github.com/xraph/ctrlplane/vars"
)

// service implements the Service interface.
type service struct {
	store       Store
	providers   *provider.Registry
	datacenters DatacenterResolver
	events      event.Bus
	auth        auth.Provider
}

// NewService creates a new instance service.
// The dcResolver parameter is optional and may be nil when datacenters are not in use.
func NewService(store Store, providers *provider.Registry, events event.Bus, auth auth.Provider, dcResolver DatacenterResolver) Service {
	return &service{
		store:       store,
		providers:   providers,
		datacenters: dcResolver,
		events:      events,
		auth:        auth,
	}
}

// Create provisions a new instance on the resolved provider.
func (s *service) Create(ctx context.Context, req CreateRequest) (*Instance, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("create instance: %w", err)
	}

	// Resolve the provider: datacenter → explicit name → default.
	var p provider.Provider

	providerName := req.ProviderName

	if !req.DatacenterID.IsNil() && s.datacenters != nil {
		dcProvider, dcErr := s.datacenters.ResolveProvider(ctx, req.DatacenterID)
		if dcErr != nil {
			return nil, fmt.Errorf("create instance: resolve datacenter provider: %w", dcErr)
		}

		providerName = dcProvider
	}

	if providerName != "" {
		p, err = s.providers.Get(providerName)
	} else {
		p, err = s.providers.Default()
	}

	if err != nil {
		return nil, fmt.Errorf("create instance: resolve provider: %w", err)
	}

	// Resolve the effective deployment source: an explicit Source, or
	// legacy Services projected onto a services Source.
	source := req.Source
	if source.Type == "" && len(req.Services) > 0 {
		source = provider.DeploymentSource{Type: provider.SourceServices, Services: req.Services}
	}

	if source.Type == "" {
		return nil, errors.New("create instance: a source or services is required")
	}

	if err := source.Validate(); err != nil {
		return nil, fmt.Errorf("create instance: %w", err)
	}

	kind := req.Kind
	if kind == "" {
		kind = provider.KindDeployment
	}

	info := p.Info()
	inst := &Instance{
		Entity:       ctrlplane.NewEntity(id.PrefixInstance),
		TenantID:     claims.TenantID,
		Name:         req.Name,
		Slug:         slugify(req.Name),
		DatacenterID: req.DatacenterID,
		ProviderName: info.Name,
		Region:       req.Region,
		State:        provider.StateProvisioning,
		Kind:         kind,
		Services:     source.Services,
		Source:       source,
		Labels:       req.Labels,
	}

	if err := s.store.Insert(ctx, inst); err != nil {
		return nil, fmt.Errorf("create instance: insert: %w", err)
	}

	result, err := s.provisionSource(ctx, p, inst, source, req)
	if err != nil {
		// Mark the instance as failed if provisioning fails.
		inst.State = provider.StateFailed
		inst.UpdatedAt = time.Now().UTC()
		_ = s.store.Update(ctx, inst)

		return nil, fmt.Errorf("create instance: provision: %w", err)
	}

	inst.ProviderRef = result.ProviderRef
	inst.ServiceRefs = result.ServiceRefs
	inst.Endpoints = result.Endpoints
	// Advance state to Running after a successful Provision. Providers
	// like docker create + start the container synchronously inside
	// Provision, so by the time we get here the workload is live —
	// leaving the row at "provisioning" hides healthy instances from
	// state-filtering dashboards (e.g. /dashboard/instances?state=running).
	//
	// Async-rollout providers (e.g. kubernetes Deployment that takes
	// seconds to roll out) should override this via a subsequent
	// Status poll that bumps the state down to Starting and back up
	// to Running once Ready. The synchronous-provider default is the
	// safer initial behaviour; over-eager Running here is preferable
	// to indefinite Provisioning, since downstream consumers can
	// always cross-check via Status.
	inst.State = provider.StateRunning
	inst.UpdatedAt = time.Now().UTC()

	if err := s.store.Update(ctx, inst); err != nil {
		return nil, fmt.Errorf("create instance: update after provision: %w", err)
	}

	// Fire-and-forget event.
	_ = s.events.Publish(ctx, event.NewEvent(event.InstanceCreated, claims.TenantID).
		WithInstance(inst.ID).
		WithActor(claims.SubjectID))

	return inst, nil
}

// provisionSource resolves the instance's variables against its derived
// context, renders the deployment source, and dispatches provisioning to the
// appropriate provider engine. Services flow through the same path —
// rendering is a no-op when no templates are present and dispatch falls
// through to the core Provision.
func (s *service) provisionSource(
	ctx context.Context,
	p provider.Provider,
	inst *Instance,
	source provider.DeploymentSource,
	req CreateRequest,
) (*provider.ProvisionResult, error) {
	derived := vars.Scope{
		Instance: vars.InstanceContext{ID: inst.ID.String(), Name: inst.Name},
		Tenant:   vars.TenantContext{ID: inst.TenantID},
		Region:   inst.Region,
	}

	scope, _, err := vars.NewResolver().Resolve(ctx, req.Variables, req.VariableValues, derived)
	if err != nil {
		return nil, fmt.Errorf("resolve variables: %w", err)
	}

	rendered, err := render.Render(source, scope)
	if err != nil {
		return nil, fmt.Errorf("render source: %w", err)
	}

	return dispatch.Provision(ctx, p, dispatch.Request{
		InstanceID: inst.ID,
		TenantID:   inst.TenantID,
		Name:       inst.Name,
		Kind:       inst.Kind,
		Source:     rendered,
		Labels:     inst.Labels,
	})
}

// Get returns an instance by ID, scoped to the caller's tenant.
func (s *service) Get(ctx context.Context, instanceID id.ID) (*Instance, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("get instance: %w", err)
	}

	inst, err := s.store.GetByID(ctx, claims.TenantID, instanceID)
	if err != nil {
		return nil, fmt.Errorf("get instance: %w", err)
	}

	return inst, nil
}

// GetBySlug looks up an instance by slug in the caller's tenant.
// Surfaces ctrlplane.ErrNotFound (wrapped) when nothing matches so
// callers can use errors.Is to branch.
func (s *service) GetBySlug(ctx context.Context, slug string) (*Instance, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("get instance by slug: %w", err)
	}

	inst, err := s.store.GetBySlug(ctx, claims.TenantID, slug)
	if err != nil {
		return nil, fmt.Errorf("get instance by slug: %w", err)
	}

	return inst, nil
}

// List returns instances for the current tenant with optional filtering.
func (s *service) List(ctx context.Context, opts ListOptions) (*ListResult, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("list instances: %w", err)
	}

	result, err := s.store.List(ctx, claims.TenantID, opts)
	if err != nil {
		return nil, fmt.Errorf("list instances: %w", err)
	}

	return result, nil
}

// Update modifies mutable fields on an instance (name, env, labels).
func (s *service) Update(ctx context.Context, instanceID id.ID, req UpdateRequest) (*Instance, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("update instance: %w", err)
	}

	inst, err := s.store.GetByID(ctx, claims.TenantID, instanceID)
	if err != nil {
		return nil, fmt.Errorf("update instance: %w", err)
	}

	if req.Name != nil {
		inst.Name = *req.Name
		inst.Slug = slugify(*req.Name)
	}

	if req.Services != nil {
		inst.Services = req.Services
	}

	if req.Labels != nil {
		inst.Labels = req.Labels
	}

	inst.UpdatedAt = time.Now().UTC()

	if err := s.store.Update(ctx, inst); err != nil {
		return nil, fmt.Errorf("update instance: %w", err)
	}

	return inst, nil
}

// Delete tears down the instance's provider-side resources
// (containers, pods, allocations) and removes the instance row.
//
// Delete is convergent: the goal is "instance gone, all containers
// stopped". To that end:
//
//   - State transitions are NOT validated. Delete is operator-driven
//     and must always succeed regardless of the current state. An
//     instance stuck mid-Provision (StateStarting) or already
//     marked StateDestroyed still gets cleaned up.
//   - When the row is already in StateDestroyed, we skip Deprovision
//     (resources are gone) and just remove the row, in case a prior
//     Delete crashed between Deprovision and the row delete.
//   - Provider Deprovision is best-effort — providers treat
//     "resource already gone" as success. A real runtime error
//     leaves the row in StateFailed for operator retry.
//   - When the configured provider is no longer registered (re-
//     configuration, etc.), the row is dropped anyway: keeping it
//     pointing at a vanished provider is worse than the operator
//     having to reap any orphaned runtime resources by hand.
func (s *service) Delete(ctx context.Context, instanceID id.ID) error {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return fmt.Errorf("delete instance: %w", err)
	}

	inst, err := s.store.GetByID(ctx, claims.TenantID, instanceID)
	if err != nil {
		return fmt.Errorf("delete instance: %w", err)
	}

	wasDestroyed := inst.State == provider.StateDestroyed

	if !wasDestroyed {
		inst.State = provider.StateDestroying
		inst.UpdatedAt = time.Now().UTC()
		_ = s.store.Update(ctx, inst)
	}

	if !wasDestroyed {
		p, providerErr := s.providers.Get(inst.ProviderName)
		if providerErr != nil {
			// Provider deconfigured — drop the row anyway. There's
			// no point keeping it pointing at a vanished provider;
			// operators reconcile any orphan runtime resources
			// out-of-band.
			_ = s.store.Delete(ctx, claims.TenantID, instanceID)

			_ = s.events.Publish(ctx, event.NewEvent(event.InstanceDeleted, claims.TenantID).
				WithInstance(inst.ID).
				WithActor(claims.SubjectID).
				WithPayload(map[string]any{
					"warning": "provider not registered: " + providerErr.Error(),
				}))

			//nolint:nilerr // convergent delete: provider gone, drop the row
			return nil
		}

		if err := dispatch.Deprovision(ctx, p, inst.Source.Type, inst.ID); err != nil {
			inst.State = provider.StateFailed
			inst.UpdatedAt = time.Now().UTC()
			_ = s.store.Update(ctx, inst)

			return fmt.Errorf("delete instance: deprovision: %w", err)
		}
	}

	if err := s.store.Delete(ctx, claims.TenantID, instanceID); err != nil {
		return fmt.Errorf("delete instance: remove: %w", err)
	}

	_ = s.events.Publish(ctx, event.NewEvent(event.InstanceDeleted, claims.TenantID).
		WithInstance(inst.ID).
		WithActor(claims.SubjectID))

	return nil
}

// Start starts a stopped instance.
func (s *service) Start(ctx context.Context, instanceID id.ID) error {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return fmt.Errorf("start instance: %w", err)
	}

	inst, err := s.store.GetByID(ctx, claims.TenantID, instanceID)
	if err != nil {
		return fmt.Errorf("start instance: %w", err)
	}

	if err := ValidateTransition(inst.State, provider.StateStarting); err != nil {
		return fmt.Errorf("start instance: %w", err)
	}

	inst.State = provider.StateStarting
	inst.UpdatedAt = time.Now().UTC()

	if err := s.store.Update(ctx, inst); err != nil {
		return fmt.Errorf("start instance: update state: %w", err)
	}

	p, err := s.providers.Get(inst.ProviderName)
	if err != nil {
		return fmt.Errorf("start instance: resolve provider: %w", err)
	}

	if err := p.Start(ctx, inst.ID); err != nil {
		inst.State = provider.StateFailed
		inst.UpdatedAt = time.Now().UTC()
		_ = s.store.Update(ctx, inst)

		return fmt.Errorf("start instance: provider start: %w", err)
	}

	inst.State = provider.StateRunning
	inst.UpdatedAt = time.Now().UTC()

	if err := s.store.Update(ctx, inst); err != nil {
		return fmt.Errorf("start instance: update running state: %w", err)
	}

	// Fire-and-forget event.
	_ = s.events.Publish(ctx, event.NewEvent(event.InstanceStarted, claims.TenantID).
		WithInstance(inst.ID).
		WithActor(claims.SubjectID))

	return nil
}

// Stop gracefully stops a running instance.
func (s *service) Stop(ctx context.Context, instanceID id.ID) error {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return fmt.Errorf("stop instance: %w", err)
	}

	inst, err := s.store.GetByID(ctx, claims.TenantID, instanceID)
	if err != nil {
		return fmt.Errorf("stop instance: %w", err)
	}

	if err := ValidateTransition(inst.State, provider.StateStopping); err != nil {
		return fmt.Errorf("stop instance: %w", err)
	}

	inst.State = provider.StateStopping
	inst.UpdatedAt = time.Now().UTC()

	if err := s.store.Update(ctx, inst); err != nil {
		return fmt.Errorf("stop instance: update state: %w", err)
	}

	p, err := s.providers.Get(inst.ProviderName)
	if err != nil {
		return fmt.Errorf("stop instance: resolve provider: %w", err)
	}

	if err := p.Stop(ctx, inst.ID); err != nil {
		inst.State = provider.StateFailed
		inst.UpdatedAt = time.Now().UTC()
		_ = s.store.Update(ctx, inst)

		return fmt.Errorf("stop instance: provider stop: %w", err)
	}

	inst.State = provider.StateStopped
	inst.UpdatedAt = time.Now().UTC()

	if err := s.store.Update(ctx, inst); err != nil {
		return fmt.Errorf("stop instance: update stopped state: %w", err)
	}

	// Fire-and-forget event.
	_ = s.events.Publish(ctx, event.NewEvent(event.InstanceStopped, claims.TenantID).
		WithInstance(inst.ID).
		WithActor(claims.SubjectID))

	return nil
}

// Restart performs a stop+start cycle on the instance.
func (s *service) Restart(ctx context.Context, instanceID id.ID) error {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return fmt.Errorf("restart instance: %w", err)
	}

	inst, err := s.store.GetByID(ctx, claims.TenantID, instanceID)
	if err != nil {
		return fmt.Errorf("restart instance: %w", err)
	}

	p, err := s.providers.Get(inst.ProviderName)
	if err != nil {
		return fmt.Errorf("restart instance: resolve provider: %w", err)
	}

	if err := p.Restart(ctx, inst.ID); err != nil {
		inst.State = provider.StateFailed
		inst.UpdatedAt = time.Now().UTC()
		_ = s.store.Update(ctx, inst)

		return fmt.Errorf("restart instance: provider restart: %w", err)
	}

	inst.State = provider.StateRunning
	inst.UpdatedAt = time.Now().UTC()

	if err := s.store.Update(ctx, inst); err != nil {
		return fmt.Errorf("restart instance: update state: %w", err)
	}

	// Fire-and-forget event.
	_ = s.events.Publish(ctx, event.NewEvent(event.InstanceStarted, claims.TenantID).
		WithInstance(inst.ID).
		WithActor(claims.SubjectID))

	return nil
}

// Scale adjusts the resource allocation for an instance.
func (s *service) Scale(ctx context.Context, instanceID id.ID, req ScaleRequest) error {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return fmt.Errorf("scale instance: %w", err)
	}

	inst, err := s.store.GetByID(ctx, claims.TenantID, instanceID)
	if err != nil {
		return fmt.Errorf("scale instance: %w", err)
	}

	// Scale targets the Main service's resources — the per-service
	// resource model means CPU/memory tweaks always apply to the
	// Main; per-service Scale is a future API.
	main := inst.MainService()
	if main == nil {
		return fmt.Errorf("scale instance %s: no Main service", instanceID)
	}

	spec := main.Resources

	if req.CPUMillis != nil {
		spec.CPUMillis = *req.CPUMillis
	}

	if req.MemoryMB != nil {
		spec.MemoryMB = *req.MemoryMB
	}

	if req.Replicas != nil {
		spec.Replicas = *req.Replicas
	}

	p, err := s.providers.Get(inst.ProviderName)
	if err != nil {
		return fmt.Errorf("scale instance: resolve provider: %w", err)
	}

	if err := p.Scale(ctx, inst.ID, spec); err != nil {
		return fmt.Errorf("scale instance: provider scale: %w", err)
	}

	main.Resources = spec
	inst.UpdatedAt = time.Now().UTC()

	if err := s.store.Update(ctx, inst); err != nil {
		return fmt.Errorf("scale instance: update: %w", err)
	}

	// Fire-and-forget event.
	_ = s.events.Publish(ctx, event.NewEvent(event.InstanceScaled, claims.TenantID).
		WithInstance(inst.ID).
		WithActor(claims.SubjectID).
		WithPayload(map[string]any{
			"cpu_millis": spec.CPUMillis,
			"memory_mb":  spec.MemoryMB,
			"replicas":   spec.Replicas,
		}))

	return nil
}

// Suspend marks an instance as suspended and stops it.
func (s *service) Suspend(ctx context.Context, instanceID id.ID, reason string) error {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return fmt.Errorf("suspend instance: %w", err)
	}

	inst, err := s.store.GetByID(ctx, claims.TenantID, instanceID)
	if err != nil {
		return fmt.Errorf("suspend instance: %w", err)
	}

	now := time.Now().UTC()
	inst.SuspendedAt = &now
	inst.UpdatedAt = now

	p, err := s.providers.Get(inst.ProviderName)
	if err != nil {
		return fmt.Errorf("suspend instance: resolve provider: %w", err)
	}

	if err := p.Stop(ctx, inst.ID); err != nil {
		return fmt.Errorf("suspend instance: provider stop: %w", err)
	}

	inst.State = provider.StateStopped

	if err := s.store.Update(ctx, inst); err != nil {
		return fmt.Errorf("suspend instance: update: %w", err)
	}

	// Fire-and-forget event.
	_ = s.events.Publish(ctx, event.NewEvent(event.InstanceSuspended, claims.TenantID).
		WithInstance(inst.ID).
		WithActor(claims.SubjectID).
		WithPayload(map[string]any{
			"reason": reason,
		}))

	return nil
}

// Unsuspend restores a suspended instance and starts it.
func (s *service) Unsuspend(ctx context.Context, instanceID id.ID) error {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return fmt.Errorf("unsuspend instance: %w", err)
	}

	inst, err := s.store.GetByID(ctx, claims.TenantID, instanceID)
	if err != nil {
		return fmt.Errorf("unsuspend instance: %w", err)
	}

	inst.SuspendedAt = nil
	inst.UpdatedAt = time.Now().UTC()

	p, err := s.providers.Get(inst.ProviderName)
	if err != nil {
		return fmt.Errorf("unsuspend instance: resolve provider: %w", err)
	}

	if err := p.Start(ctx, inst.ID); err != nil {
		return fmt.Errorf("unsuspend instance: provider start: %w", err)
	}

	inst.State = provider.StateRunning

	if err := s.store.Update(ctx, inst); err != nil {
		return fmt.Errorf("unsuspend instance: update: %w", err)
	}

	// Fire-and-forget event.
	_ = s.events.Publish(ctx, event.NewEvent(event.InstanceUnsuspended, claims.TenantID).
		WithInstance(inst.ID).
		WithActor(claims.SubjectID))

	return nil
}

// slugify converts a name to a URL-friendly slug.
func slugify(name string) string {
	return strings.ReplaceAll(strings.ToLower(name), " ", "-")
}

// Logs returns a log stream for the instance via the resolved
// provider. Caller closes the returned ReadCloser to stop. Honours
// Follow / Since / Tail in the LogsOptions.
func (s *service) Logs(ctx context.Context, instanceID id.ID, opts LogsOptions) (io.ReadCloser, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("logs: %w", err)
	}

	inst, err := s.store.GetByID(ctx, claims.TenantID, instanceID)
	if err != nil {
		return nil, fmt.Errorf("logs: get instance: %w", err)
	}

	p, err := s.providers.Get(inst.ProviderName)
	if err != nil {
		return nil, fmt.Errorf("logs: resolve provider: %w", err)
	}

	rc, err := p.Logs(ctx, inst.ID, provider.LogOptions{
		Follow: opts.Follow,
		Since:  opts.Since,
		Tail:   opts.Tail,
	})
	if err != nil {
		return nil, fmt.Errorf("logs: provider: %w", err)
	}

	return rc, nil
}

// Resources returns a one-shot resource-usage sample via the
// instance's provider. Errors that look like "container gone"
// collapse to a zero-valued usage so the metrics poller doesn't
// treat container-restart windows as failures.
func (s *service) Resources(ctx context.Context, instanceID id.ID) (*provider.ResourceUsage, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("resources: %w", err)
	}

	inst, err := s.store.GetByID(ctx, claims.TenantID, instanceID)
	if err != nil {
		if errors.Is(err, ctrlplane.ErrNotFound) {
			return &provider.ResourceUsage{}, nil
		}

		return nil, fmt.Errorf("resources: get instance: %w", err)
	}

	p, err := s.providers.Get(inst.ProviderName)
	if err != nil {
		return nil, fmt.Errorf("resources: resolve provider: %w", err)
	}

	usage, err := p.Resources(ctx, inst.ID)
	if err != nil {
		return nil, fmt.Errorf("resources: provider: %w", err)
	}

	if usage == nil {
		return &provider.ResourceUsage{}, nil
	}

	return usage, nil
}

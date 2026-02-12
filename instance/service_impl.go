package instance

import (
	"context"
	"fmt"
	"strings"
	"time"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

// service implements the Service interface.
type service struct {
	store     Store
	providers *provider.Registry
	events    event.Bus
	auth      auth.Provider
}

// NewService creates a new instance service.
func NewService(store Store, providers *provider.Registry, events event.Bus, auth auth.Provider) Service {
	return &service{
		store:     store,
		providers: providers,
		events:    events,
		auth:      auth,
	}
}

// Create provisions a new instance on the resolved provider.
func (s *service) Create(ctx context.Context, req CreateRequest) (*Instance, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("create instance: %w", err)
	}

	// Resolve the provider: use the requested name or fall back to default.
	var p provider.Provider

	if req.ProviderName != "" {
		p, err = s.providers.Get(req.ProviderName)
	} else {
		p, err = s.providers.Default()
	}

	if err != nil {
		return nil, fmt.Errorf("create instance: resolve provider: %w", err)
	}

	info := p.Info()
	inst := &Instance{
		Entity:       ctrlplane.NewEntity(id.PrefixInstance),
		TenantID:     claims.TenantID,
		Name:         req.Name,
		Slug:         slugify(req.Name),
		ProviderName: info.Name,
		Region:       req.Region,
		State:        provider.StateProvisioning,
		Image:        req.Image,
		Resources:    req.Resources,
		Env:          req.Env,
		Ports:        req.Ports,
		Labels:       req.Labels,
	}

	if err := s.store.Insert(ctx, inst); err != nil {
		return nil, fmt.Errorf("create instance: insert: %w", err)
	}

	result, err := p.Provision(ctx, provider.ProvisionRequest{
		InstanceID: inst.ID,
		TenantID:   claims.TenantID,
		Name:       req.Name,
		Image:      req.Image,
		Resources:  req.Resources,
		Env:        req.Env,
		Ports:      req.Ports,
		Labels:     req.Labels,
	})
	if err != nil {
		// Mark the instance as failed if provisioning fails.
		inst.State = provider.StateFailed
		inst.UpdatedAt = time.Now().UTC()
		_ = s.store.Update(ctx, inst)

		return nil, fmt.Errorf("create instance: provision: %w", err)
	}

	inst.ProviderRef = result.ProviderRef
	inst.Endpoints = result.Endpoints
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

	if req.Env != nil {
		inst.Env = req.Env
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

// Delete deprovisions and removes an instance.
func (s *service) Delete(ctx context.Context, instanceID id.ID) error {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return fmt.Errorf("delete instance: %w", err)
	}

	inst, err := s.store.GetByID(ctx, claims.TenantID, instanceID)
	if err != nil {
		return fmt.Errorf("delete instance: %w", err)
	}

	if err := ValidateTransition(inst.State, provider.StateDestroying); err != nil {
		return fmt.Errorf("delete instance: %w", err)
	}

	inst.State = provider.StateDestroying
	inst.UpdatedAt = time.Now().UTC()

	if err := s.store.Update(ctx, inst); err != nil {
		return fmt.Errorf("delete instance: update state: %w", err)
	}

	p, err := s.providers.Get(inst.ProviderName)
	if err != nil {
		return fmt.Errorf("delete instance: resolve provider: %w", err)
	}

	if err := p.Deprovision(ctx, inst.ID); err != nil {
		inst.State = provider.StateFailed
		inst.UpdatedAt = time.Now().UTC()
		_ = s.store.Update(ctx, inst)

		return fmt.Errorf("delete instance: deprovision: %w", err)
	}

	if err := s.store.Delete(ctx, claims.TenantID, instanceID); err != nil {
		return fmt.Errorf("delete instance: remove: %w", err)
	}

	// Fire-and-forget event.
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

	// Build the new resource spec by merging requested changes with current values.
	spec := inst.Resources

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

	inst.Resources = spec
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

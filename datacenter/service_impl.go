package datacenter

import (
	"context"
	"errors"
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

// NewService creates a new datacenter service.
func NewService(store Store, providers *provider.Registry, events event.Bus, auth auth.Provider) Service {
	return &service{
		store:     store,
		providers: providers,
		events:    events,
		auth:      auth,
	}
}

// Create registers a new datacenter backed by a named provider.
func (s *service) Create(ctx context.Context, req CreateRequest) (*Datacenter, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("create datacenter: %w", err)
	}

	// Validate that the named provider is registered.
	if _, provErr := s.providers.Get(req.ProviderName); provErr != nil {
		return nil, fmt.Errorf("create datacenter: %w", provErr)
	}

	// Empty TenantID on the claims means "create as platform-shared".
	// Only system admins may do that — regular customers always get
	// their own claims.TenantID stamped on the DC.
	if claims.TenantID == "" && !claims.IsSystemAdmin() {
		return nil, fmt.Errorf("create datacenter: %w", ctrlplane.ErrForbidden)
	}

	dc := NewDatacenter()
	dc.TenantID = claims.TenantID
	dc.Name = req.Name
	dc.Slug = slugify(req.Name)
	dc.ProviderName = req.ProviderName
	dc.Region = req.Region
	dc.Zone = req.Zone
	dc.Labels = req.Labels
	dc.Metadata = req.Metadata

	if req.Location != nil {
		dc.Location = *req.Location
	}

	if req.Capacity != nil {
		dc.Capacity = *req.Capacity
	}

	// BootstrapServices is purely declarative on Create — the row is
	// persisted as-is, and the bootstrap reconciler picks it up on
	// its next tick. Create stays synchronous; no Provision happens
	// here.
	if req.BootstrapServices != nil {
		dc.BootstrapServices = req.BootstrapServices
	}

	// Idempotency guard: surface ErrAlreadyExists when a datacenter
	// with the same (tenant_id, slug) already exists for this tenant
	// so callers (e.g. boot-time seeders that retry on every restart)
	// can skip via errors.Is. The memory store enforces this at the
	// store layer, but pg/mongo/sqlite/badger don't — making the check
	// here keeps every backend consistent without each driver having
	// to map its own duplicate-key error to ErrAlreadyExists.
	//
	// GetDatacenterBySlug uses hybrid visibility (tenant-owned OR
	// platform-shared), so we additionally compare TenantID — a tenant
	// creating "docker" must not be blocked by a platform-shared
	// "docker", and a platform admin seeding "docker" must not be
	// blocked by a tenant-X "docker".
	existing, lookupErr := s.store.GetDatacenterBySlug(ctx, claims.TenantID, dc.Slug)
	if lookupErr != nil && !errors.Is(lookupErr, ctrlplane.ErrNotFound) {
		return nil, fmt.Errorf("create datacenter: dedupe check: %w", lookupErr)
	}

	if existing != nil && existing.TenantID == claims.TenantID {
		return nil, fmt.Errorf("%w: datacenter slug %q in tenant %q",
			ctrlplane.ErrAlreadyExists, dc.Slug, claims.TenantID)
	}

	if err := s.store.InsertDatacenter(ctx, dc); err != nil {
		return nil, fmt.Errorf("create datacenter: insert: %w", err)
	}

	_ = s.events.Publish(ctx, event.NewEvent(event.DatacenterCreated, claims.TenantID).
		WithDatacenter(dc.ID).
		WithActor(claims.SubjectID).
		WithPayload(map[string]any{
			"name":     dc.Name,
			"provider": dc.ProviderName,
			"region":   dc.Region,
		}))

	return dc, nil
}

// Get returns a datacenter by ID, scoped to the caller's tenant.
func (s *service) Get(ctx context.Context, datacenterID id.ID) (*Datacenter, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("get datacenter: %w", err)
	}

	dc, err := s.store.GetDatacenterByID(ctx, claims.TenantID, datacenterID)
	if err != nil {
		return nil, fmt.Errorf("get datacenter: %w", err)
	}

	return dc, nil
}

// GetBySlug returns a datacenter by slug, scoped to the caller's tenant.
func (s *service) GetBySlug(ctx context.Context, slug string) (*Datacenter, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("get datacenter by slug: %w", err)
	}

	dc, err := s.store.GetDatacenterBySlug(ctx, claims.TenantID, slug)
	if err != nil {
		return nil, fmt.Errorf("get datacenter by slug: %w", err)
	}

	return dc, nil
}

// List returns datacenters for the current tenant with optional filtering.
func (s *service) List(ctx context.Context, opts ListOptions) (*ListResult, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("list datacenters: %w", err)
	}

	result, err := s.store.ListDatacenters(ctx, claims.TenantID, opts)
	if err != nil {
		return nil, fmt.Errorf("list datacenters: %w", err)
	}

	return result, nil
}

// Update modifies a datacenter's configuration.
func (s *service) Update(ctx context.Context, datacenterID id.ID, req UpdateRequest) (*Datacenter, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("update datacenter: %w", err)
	}

	dc, err := s.store.GetDatacenterByID(ctx, claims.TenantID, datacenterID)
	if err != nil {
		return nil, fmt.Errorf("update datacenter: get: %w", err)
	}

	if req.Name != nil {
		dc.Name = *req.Name
		dc.Slug = slugify(*req.Name)
	}

	if req.Zone != nil {
		dc.Zone = *req.Zone
	}

	if req.Location != nil {
		dc.Location = *req.Location
	}

	if req.Capacity != nil {
		dc.Capacity = *req.Capacity
	}

	if req.Labels != nil {
		dc.Labels = req.Labels
	}

	if req.Metadata != nil {
		dc.Metadata = req.Metadata
	}

	// BootstrapServices uses pointer-to-slice on the request so a
	// nil value means "leave alone" while a non-nil zero-length
	// slice means "clear". The reconciler diffs against whatever
	// gets persisted here on its next tick.
	if req.BootstrapServices != nil {
		dc.BootstrapServices = *req.BootstrapServices
	}

	dc.UpdatedAt = time.Now().UTC()

	if err := s.store.UpdateDatacenter(ctx, dc); err != nil {
		return nil, fmt.Errorf("update datacenter: store: %w", err)
	}

	_ = s.events.Publish(ctx, event.NewEvent(event.DatacenterUpdated, claims.TenantID).
		WithDatacenter(dc.ID).
		WithActor(claims.SubjectID))

	return dc, nil
}

// Delete removes a datacenter. Fails if instances still reference it.
func (s *service) Delete(ctx context.Context, datacenterID id.ID) error {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return fmt.Errorf("delete datacenter: %w", err)
	}

	dc, err := s.store.GetDatacenterByID(ctx, claims.TenantID, datacenterID)
	if err != nil {
		return fmt.Errorf("delete datacenter: get: %w", err)
	}

	count, err := s.store.CountInstancesByDatacenter(ctx, claims.TenantID, datacenterID)
	if err != nil {
		return fmt.Errorf("delete datacenter: count instances: %w", err)
	}

	if count > 0 {
		return fmt.Errorf("delete datacenter: %d active instances: %w", count, ctrlplane.ErrDatacenterUnavailable)
	}

	if err := s.store.DeleteDatacenter(ctx, claims.TenantID, datacenterID); err != nil {
		return fmt.Errorf("delete datacenter: store: %w", err)
	}

	_ = s.events.Publish(ctx, event.NewEvent(event.DatacenterDeleted, claims.TenantID).
		WithDatacenter(datacenterID).
		WithActor(claims.SubjectID).
		WithPayload(map[string]any{
			"name": dc.Name,
		}))

	return nil
}

// SetStatus transitions a datacenter to a new operational status.
func (s *service) SetStatus(ctx context.Context, datacenterID id.ID, status Status) error {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return fmt.Errorf("set datacenter status: %w", err)
	}

	dc, err := s.store.GetDatacenterByID(ctx, claims.TenantID, datacenterID)
	if err != nil {
		return fmt.Errorf("set datacenter status: get: %w", err)
	}

	if err := ValidateTransition(dc.Status, status); err != nil {
		return fmt.Errorf("set datacenter status: %w", err)
	}

	oldStatus := dc.Status
	dc.Status = status
	dc.UpdatedAt = time.Now().UTC()

	if err := s.store.UpdateDatacenter(ctx, dc); err != nil {
		return fmt.Errorf("set datacenter status: store: %w", err)
	}

	_ = s.events.Publish(ctx, event.NewEvent(event.DatacenterStatusChanged, claims.TenantID).
		WithDatacenter(datacenterID).
		WithActor(claims.SubjectID).
		WithPayload(map[string]any{
			"from": string(oldStatus),
			"to":   string(status),
		}))

	return nil
}

// ResolveProvider returns the provider name for a given datacenter.
func (s *service) ResolveProvider(ctx context.Context, datacenterID id.ID) (string, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return "", fmt.Errorf("resolve datacenter provider: %w", err)
	}

	dc, err := s.store.GetDatacenterByID(ctx, claims.TenantID, datacenterID)
	if err != nil {
		return "", fmt.Errorf("resolve datacenter provider: %w", err)
	}

	if dc.Status != StatusActive {
		return "", fmt.Errorf("resolve datacenter provider: datacenter %q is %s: %w",
			dc.Name, dc.Status, ctrlplane.ErrDatacenterUnavailable)
	}

	return dc.ProviderName, nil
}

// slugify produces a URL-safe slug from a name.
func slugify(name string) string {
	return strings.ReplaceAll(strings.ToLower(name), " ", "-")
}

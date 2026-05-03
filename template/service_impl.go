package template

import (
	"context"
	"errors"
	"fmt"
	"strings"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

// service is the concrete Service implementation.
type service struct {
	store    Store
	workload WorkloadSpecReader
	events   event.Bus
}

// NewService wires the template service. The WorkloadSpecReader is
// optional at construction time — call SetWorkloadReader once the
// workload service is built to enable CreateFromWorkload. Returns a
// concrete *service so the caller can use both Service methods and
// SetWorkloadReader.
//
// The event bus is also optional — pass nil and template lifecycle
// events will be silently dropped. The default app wiring always
// passes the shared bus so events flow into the audit hook.
func NewService(store Store, events event.Bus) *service { //nolint:revive // unexported return matches deploy.NewService
	return &service{store: store, events: events}
}

// SetWorkloadReader registers the WorkloadSpecReader used by
// CreateFromWorkload. Calling without a reader leaves
// CreateFromWorkload returning an error.
func (s *service) SetWorkloadReader(r WorkloadSpecReader) {
	s.workload = r
}

// publish is a nil-safe wrapper that swallows publish errors.
func (s *service) publish(ctx context.Context, evt *event.Event) {
	if s.events == nil || evt == nil {
		return
	}

	_ = s.events.Publish(ctx, evt)
}

// Create persists a new template authored from raw fields.
func (s *service) Create(ctx context.Context, req CreateRequest) (*Template, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("create template: %w", err)
	}

	if strings.TrimSpace(req.Name) == "" {
		return nil, errors.New("create template: name is required")
	}

	if err := validateServices(req.Services); err != nil {
		return nil, fmt.Errorf("create template: %w", err)
	}

	kind := req.DefaultKind
	if kind == "" {
		kind = provider.KindDeployment
	}

	tmpl := &Template{
		Entity:          ctrlplane.NewEntity(id.PrefixTemplate),
		TenantID:        claims.TenantID,
		Name:            req.Name,
		Description:     req.Description,
		DefaultKind:     kind,
		DefaultStrategy: req.DefaultStrategy,
		Services:        req.Services,
		Labels:          req.Labels,
		Notes:           req.Notes,
	}

	if err := s.store.InsertTemplate(ctx, tmpl); err != nil {
		return nil, fmt.Errorf("create template: insert: %w", err)
	}

	s.publish(ctx, event.NewEvent(event.TemplateCreated, claims.TenantID).
		WithActor(claims.SubjectID).
		WithTemplate(tmpl.ID).
		WithPayload(map[string]any{
			"template_id":   tmpl.ID.String(),
			"name":          tmpl.Name,
			"service_count": len(tmpl.Services),
			"kind":          string(tmpl.DefaultKind),
		}))

	return tmpl, nil
}

// CreateFromWorkload forks a new template from a workload's current spec.
func (s *service) CreateFromWorkload(ctx context.Context, workloadID id.ID, req CreateFromWorkloadRequest) (*Template, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("create template from workload: %w", err)
	}

	if s.workload == nil {
		return nil, errors.New("create template from workload: workload reader not configured")
	}

	if strings.TrimSpace(req.Name) == "" {
		return nil, errors.New("create template from workload: name is required")
	}

	spec, err := s.workload.ReadWorkloadSpec(ctx, claims.TenantID, workloadID)
	if err != nil {
		return nil, fmt.Errorf("create template from workload %s: %w", workloadID, err)
	}

	tmpl := &Template{
		Entity:      ctrlplane.NewEntity(id.PrefixTemplate),
		TenantID:    claims.TenantID,
		Name:        req.Name,
		Description: req.Description,
		DefaultKind: spec.Kind,
		Services:    spec.Services,
		Labels:      spec.Labels,
		Notes:       req.Notes,
	}

	if err := s.store.InsertTemplate(ctx, tmpl); err != nil {
		return nil, fmt.Errorf("create template from workload: insert: %w", err)
	}

	s.publish(ctx, event.NewEvent(event.TemplateCreated, claims.TenantID).
		WithActor(claims.SubjectID).
		WithTemplate(tmpl.ID).
		WithWorkload(workloadID).
		WithPayload(map[string]any{
			"template_id":   tmpl.ID.String(),
			"name":          tmpl.Name,
			"service_count": len(tmpl.Services),
			"forked_from":   workloadID.String(),
			"source":        "workload",
		}))

	return tmpl, nil
}

// Get returns a specific template scoped to the caller's tenant.
func (s *service) Get(ctx context.Context, templateID id.ID) (*Template, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("get template: %w", err)
	}

	tmpl, err := s.store.GetTemplate(ctx, claims.TenantID, templateID)
	if err != nil {
		return nil, fmt.Errorf("get template %s: %w", templateID, err)
	}

	return tmpl, nil
}

// Update mutates an existing template.
func (s *service) Update(ctx context.Context, templateID id.ID, req UpdateRequest) (*Template, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("update template: %w", err)
	}

	tmpl, err := s.store.GetTemplate(ctx, claims.TenantID, templateID)
	if err != nil {
		return nil, fmt.Errorf("update template: get %s: %w", templateID, err)
	}

	if req.Name != nil {
		tmpl.Name = *req.Name
	}

	if req.Description != nil {
		tmpl.Description = *req.Description
	}

	if req.DefaultKind != nil {
		tmpl.DefaultKind = *req.DefaultKind
	}

	if req.DefaultStrategy != nil {
		tmpl.DefaultStrategy = *req.DefaultStrategy
	}

	if req.Services != nil {
		if err := validateServices(req.Services); err != nil {
			return nil, fmt.Errorf("update template: %w", err)
		}

		tmpl.Services = req.Services
	}

	if req.Labels != nil {
		tmpl.Labels = req.Labels
	}

	if req.Notes != nil {
		tmpl.Notes = *req.Notes
	}

	if err := s.store.UpdateTemplate(ctx, tmpl); err != nil {
		return nil, fmt.Errorf("update template %s: %w", templateID, err)
	}

	s.publish(ctx, event.NewEvent(event.TemplateUpdated, claims.TenantID).
		WithActor(claims.SubjectID).
		WithTemplate(tmpl.ID).
		WithPayload(map[string]any{
			"template_id": tmpl.ID.String(),
			"name":        tmpl.Name,
		}))

	return tmpl, nil
}

// Delete removes a template.
func (s *service) Delete(ctx context.Context, templateID id.ID) error {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return fmt.Errorf("delete template: %w", err)
	}

	if err := s.store.DeleteTemplate(ctx, claims.TenantID, templateID); err != nil {
		return fmt.Errorf("delete template %s: %w", templateID, err)
	}

	s.publish(ctx, event.NewEvent(event.TemplateDeleted, claims.TenantID).
		WithActor(claims.SubjectID).
		WithTemplate(templateID).
		WithPayload(map[string]any{
			"template_id": templateID.String(),
		}))

	return nil
}

// List returns templates for the caller's tenant.
func (s *service) List(ctx context.Context, opts ListOptions) (*ListResult, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("list templates: %w", err)
	}

	result, err := s.store.ListTemplates(ctx, claims.TenantID, opts)
	if err != nil {
		return nil, fmt.Errorf("list templates: %w", err)
	}

	return result, nil
}

// validateServices enforces the multi-service invariants:
//   - At least one service.
//   - Unique names within the workload.
//   - Every service has a non-empty Image.
//   - Exactly one Main service (Role defaults to Main when empty).
//   - DependsOn references resolve to siblings.
func validateServices(services []provider.ServiceSpec) error {
	if len(services) == 0 {
		return errors.New("services: at least one service is required")
	}

	names := make(map[string]struct{}, len(services))
	mainCount := 0

	for i := range services {
		svc := &services[i]

		if strings.TrimSpace(svc.Name) == "" {
			return fmt.Errorf("services[%d]: name is required", i)
		}

		if strings.TrimSpace(svc.Image) == "" {
			return fmt.Errorf("services[%d] (%s): image is required", i, svc.Name)
		}

		if _, dup := names[svc.Name]; dup {
			return fmt.Errorf("services[%d]: duplicate service name %q", i, svc.Name)
		}

		names[svc.Name] = struct{}{}

		// Default Role to Main when empty so callers don't have to set it
		// for the simple single-service case.
		if svc.Role == "" {
			svc.Role = provider.RoleMain
		}

		if svc.Role == provider.RoleMain {
			mainCount++
		}
	}

	if mainCount != 1 {
		return fmt.Errorf("services: exactly one Main service required, found %d", mainCount)
	}

	for i := range services {
		svc := &services[i]
		for _, dep := range svc.DependsOn {
			if _, ok := names[dep]; !ok {
				return fmt.Errorf("services[%d] (%s): depends_on references unknown service %q", i, svc.Name, dep)
			}
		}
	}

	return nil
}

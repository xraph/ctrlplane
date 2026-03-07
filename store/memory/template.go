package memory

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/id"
)

// InsertTemplate persists a new deployment template.
func (s *Store) InsertTemplate(_ context.Context, t *deploy.Template) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := idStr(t.ID)
	if _, exists := s.templates[key]; exists {
		return fmt.Errorf("%w: template %s", ctrlplane.ErrAlreadyExists, key)
	}

	s.templates[key] = cloneTemplate(t)

	return nil
}

// GetTemplate retrieves a deployment template by ID within a tenant.
func (s *Store) GetTemplate(_ context.Context, tenantID string, templateID id.ID) (*deploy.Template, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	t, ok := s.templates[idStr(templateID)]
	if !ok || t.TenantID != tenantID {
		return nil, fmt.Errorf("%w: template %s", ctrlplane.ErrNotFound, templateID)
	}

	return cloneTemplate(t), nil
}

// UpdateTemplate persists changes to an existing deployment template.
func (s *Store) UpdateTemplate(_ context.Context, t *deploy.Template) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := idStr(t.ID)
	if _, ok := s.templates[key]; !ok {
		return fmt.Errorf("%w: template %s", ctrlplane.ErrNotFound, key)
	}

	t.UpdatedAt = now()
	s.templates[key] = cloneTemplate(t)

	return nil
}

// DeleteTemplate removes a deployment template.
func (s *Store) DeleteTemplate(_ context.Context, tenantID string, templateID id.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := idStr(templateID)

	t, ok := s.templates[key]
	if !ok || t.TenantID != tenantID {
		return fmt.Errorf("%w: template %s", ctrlplane.ErrNotFound, templateID)
	}

	delete(s.templates, key)

	return nil
}

// ListTemplates returns a paginated list of deployment templates for a tenant.
func (s *Store) ListTemplates(_ context.Context, tenantID string, opts deploy.ListOptions) (*deploy.TemplateListResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var items []*deploy.Template

	for _, t := range s.templates {
		if t.TenantID != tenantID {
			continue
		}

		items = append(items, cloneTemplate(t))
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})

	total := len(items)

	limit := opts.Limit
	if limit <= 0 || limit > total {
		limit = total
	}

	items = items[:limit]

	return &deploy.TemplateListResult{
		Items: items,
		Total: total,
	}, nil
}

// cloneTemplate returns a deep copy of a Template.
func cloneTemplate(t *deploy.Template) *deploy.Template {
	clone := *t

	if t.Env != nil {
		clone.Env = maps.Clone(t.Env)
	}

	if t.Labels != nil {
		clone.Labels = maps.Clone(t.Labels)
	}

	if t.Annotations != nil {
		clone.Annotations = maps.Clone(t.Annotations)
	}

	if t.Ports != nil {
		clone.Ports = slices.Clone(t.Ports)
	}

	if t.Volumes != nil {
		clone.Volumes = slices.Clone(t.Volumes)
	}

	if t.Secrets != nil {
		clone.Secrets = slices.Clone(t.Secrets)
	}

	if t.ConfigFiles != nil {
		clone.ConfigFiles = slices.Clone(t.ConfigFiles)
	}

	if t.HealthCheck != nil {
		hc := *t.HealthCheck
		clone.HealthCheck = &hc
	}

	return &clone
}

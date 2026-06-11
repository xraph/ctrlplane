package memory

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/template"
)

// InsertTemplate persists a new workload template.
func (s *Store) InsertTemplate(_ context.Context, t *template.Template) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := idStr(t.ID)
	if _, exists := s.templates[key]; exists {
		return fmt.Errorf("%w: template %s", ctrlplane.ErrAlreadyExists, key)
	}

	s.templates[key] = cloneTemplate(t)

	return nil
}

// GetTemplate retrieves a template by ID within a tenant.
func (s *Store) GetTemplate(_ context.Context, tenantID string, templateID id.ID) (*template.Template, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	t, ok := s.templates[idStr(templateID)]
	if !ok || t.TenantID != tenantID {
		return nil, fmt.Errorf("%w: template %s", ctrlplane.ErrNotFound, templateID)
	}

	return cloneTemplate(t), nil
}

// UpdateTemplate persists changes to an existing template.
func (s *Store) UpdateTemplate(_ context.Context, t *template.Template) error {
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

// DeleteTemplate removes a template.
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

// ListTemplates returns a paginated list of templates for a tenant.
func (s *Store) ListTemplates(_ context.Context, tenantID string, opts template.ListOptions) (*template.ListResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var items []*template.Template

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

	return &template.ListResult{
		Items: items,
		Total: total,
	}, nil
}

// cloneTemplate returns a shallow copy of a Template with independent
// top-level slices and maps. Per-service nested fields share storage —
// templates are immutable post-write so the deep clone overhead is
// unwarranted.
func cloneTemplate(t *template.Template) *template.Template {
	clone := *t

	if t.Labels != nil {
		clone.Labels = maps.Clone(t.Labels)
	}

	if t.Services != nil {
		clone.Services = slices.Clone(t.Services)
	}

	if t.Variables != nil {
		clone.Variables = slices.Clone(t.Variables)
	}

	return &clone
}

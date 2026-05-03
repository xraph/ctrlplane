package template

import (
	"context"
	"errors"
	"sync"
	"testing"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

func tenantCtx(tenantID string) context.Context {
	return auth.WithClaims(context.Background(), &auth.Claims{TenantID: tenantID, SubjectID: "test"})
}

// memStore is a tiny in-memory Template Store used by the package's
// unit tests. It avoids importing store/memory (which would create a
// cycle: template <- store/memory <- template).
type memStore struct {
	mu    sync.Mutex
	items map[string]*Template
}

func newMemStore() *memStore {
	return &memStore{items: make(map[string]*Template)}
}

func (s *memStore) InsertTemplate(_ context.Context, t *Template) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.items[t.ID.String()] = t

	return nil
}

func (s *memStore) GetTemplate(_ context.Context, tenantID string, templateID id.ID) (*Template, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.items[templateID.String()]
	if !ok || t.TenantID != tenantID {
		return nil, ctrlplane.ErrNotFound
	}

	return t, nil
}

func (s *memStore) UpdateTemplate(_ context.Context, t *Template) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.items[t.ID.String()]; !ok {
		return ctrlplane.ErrNotFound
	}

	s.items[t.ID.String()] = t

	return nil
}

func (s *memStore) DeleteTemplate(_ context.Context, tenantID string, templateID id.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.items[templateID.String()]
	if !ok || t.TenantID != tenantID {
		return ctrlplane.ErrNotFound
	}

	delete(s.items, templateID.String())

	return nil
}

func (s *memStore) ListTemplates(_ context.Context, tenantID string, _ ListOptions) (*ListResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	items := make([]*Template, 0, len(s.items))
	for _, t := range s.items {
		if t.TenantID == tenantID {
			items = append(items, t)
		}
	}

	return &ListResult{Items: items, Total: len(items)}, nil
}

// TestCreateAndGet round-trips a template through the in-memory store.
func TestCreateAndGet(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	svc := NewService(store, nil)

	ctx := tenantCtx("ten_123")

	created, err := svc.Create(ctx, CreateRequest{
		Name:            "web-api",
		DefaultStrategy: "rolling",
		Services: []provider.ServiceSpec{{
			Name:  "main",
			Image: "myapp:1.0",
			Role:  provider.RoleMain,
			Resources: provider.ResourceSpec{
				CPUMillis: 500,
				MemoryMB:  256,
				Replicas:  2,
			},
			Env:     map[string]string{"LOG_LEVEL": "info"},
			Secrets: []SecretRef{{Key: "DATABASE_URL"}},
		}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if created.TenantID != "ten_123" {
		t.Fatalf("tenant id: want ten_123, got %q", created.TenantID)
	}

	got, err := svc.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	main := got.MainService()
	if got.Name != "web-api" || main == nil || main.Image != "myapp:1.0" {
		t.Fatalf("Get returned wrong template: %+v", got)
	}

	if len(main.Secrets) != 1 || main.Secrets[0].Key != "DATABASE_URL" {
		t.Fatalf("secrets did not round-trip: %+v", main.Secrets)
	}
}

// TestCreateRejectsMissingName ensures the service guards required fields.
func TestCreateRejectsMissingName(t *testing.T) {
	t.Parallel()

	svc := NewService(newMemStore(), nil)

	_, err := svc.Create(tenantCtx("ten_x"), CreateRequest{
		Services: []provider.ServiceSpec{{Name: "main", Image: "img:1", Role: provider.RoleMain}},
	})
	if err == nil {
		t.Fatalf("expected error for missing name")
	}
}

// TestCreateRequiresAuth ensures the service rejects calls without claims.
func TestCreateRequiresAuth(t *testing.T) {
	t.Parallel()

	svc := NewService(newMemStore(), nil)

	_, err := svc.Create(context.Background(), CreateRequest{
		Name:     "x",
		Services: []provider.ServiceSpec{{Name: "main", Image: "i", Role: provider.RoleMain}},
	})
	if !errors.Is(err, auth.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
}

// TestUpdatePartial verifies pointer-field semantics — nil fields untouched.
func TestUpdatePartial(t *testing.T) {
	t.Parallel()

	svc := NewService(newMemStore(), nil)
	ctx := tenantCtx("ten_1")

	created, err := svc.Create(ctx, CreateRequest{
		Name:     "x",
		Services: []provider.ServiceSpec{{Name: "main", Image: "img:1", Role: provider.RoleMain}},
		Notes:    "original",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	newServices := []provider.ServiceSpec{{Name: "main", Image: "img:2", Role: provider.RoleMain}}

	updated, err := svc.Update(ctx, created.ID, UpdateRequest{Services: newServices})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if main := updated.MainService(); main == nil || main.Image != "img:2" {
		t.Fatalf("Image: want img:2, got %+v", main)
	}

	if updated.Notes != "original" {
		t.Fatalf("Notes should be unchanged: got %q", updated.Notes)
	}
}

// TestDelete + TestList sanity-check the remaining CRUD surface.
func TestDeleteAndList(t *testing.T) {
	t.Parallel()

	svc := NewService(newMemStore(), nil)
	ctx := tenantCtx("ten_1")

	t1, _ := svc.Create(ctx, CreateRequest{
		Name:     "a",
		Services: []provider.ServiceSpec{{Name: "main", Image: "i:1", Role: provider.RoleMain}},
	})
	_, _ = svc.Create(ctx, CreateRequest{
		Name:     "b",
		Services: []provider.ServiceSpec{{Name: "main", Image: "i:2", Role: provider.RoleMain}},
	})

	res, err := svc.List(ctx, ListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if res.Total != 2 {
		t.Fatalf("Total: want 2, got %d", res.Total)
	}

	if err := svc.Delete(ctx, t1.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	res, _ = svc.List(ctx, ListOptions{Limit: 10})
	if res.Total != 1 {
		t.Fatalf("after Delete: want 1, got %d", res.Total)
	}
}

// fakeReader implements WorkloadSpecReader for the CreateFromWorkload test.
type fakeReader struct {
	spec *WorkloadSpec
	err  error
}

func (f fakeReader) ReadWorkloadSpec(_ context.Context, _ string, _ id.ID) (*WorkloadSpec, error) {
	return f.spec, f.err
}

// TestCreateFromWorkload verifies the fork path projects workload spec
// fields onto the new template.
func TestCreateFromWorkload(t *testing.T) {
	t.Parallel()

	svc := NewService(newMemStore(), nil)
	svc.SetWorkloadReader(fakeReader{
		spec: &WorkloadSpec{
			Kind: provider.KindDeployment,
			Services: []provider.ServiceSpec{{
				Name:    "main",
				Image:   "myapp:5",
				Role:    provider.RoleMain,
				Env:     map[string]string{"FOO": "bar"},
				Secrets: []SecretRef{{Key: "S1"}},
			}},
		},
	})

	ctx := tenantCtx("ten_1")

	tmpl, err := svc.CreateFromWorkload(ctx, id.New(id.PrefixWorkload), CreateFromWorkloadRequest{
		Name:        "forked",
		Description: "from a running workload",
	})
	if err != nil {
		t.Fatalf("CreateFromWorkload: %v", err)
	}

	main := tmpl.MainService()
	if main == nil || main.Image != "myapp:5" {
		t.Fatalf("forked image: want myapp:5, got %+v", main)
	}

	if main.Env["FOO"] != "bar" {
		t.Fatalf("forked env: %+v", main.Env)
	}

	if len(main.Secrets) != 1 {
		t.Fatalf("forked secrets: want 1, got %d", len(main.Secrets))
	}
}

// TestCreateFromWorkloadWithoutReader returns an error rather than nil-deref.
func TestCreateFromWorkloadWithoutReader(t *testing.T) {
	t.Parallel()

	svc := NewService(newMemStore(), nil)
	// Reader intentionally not set.

	_, err := svc.CreateFromWorkload(tenantCtx("ten_1"), id.New(id.PrefixWorkload), CreateFromWorkloadRequest{Name: "x"})
	if err == nil {
		t.Fatalf("expected error when reader missing")
	}
}

// TestGetMissingNotFound surfaces the sentinel for callers using errors.Is.
func TestGetMissingNotFound(t *testing.T) {
	t.Parallel()

	svc := NewService(newMemStore(), nil)

	_, err := svc.Get(tenantCtx("ten_1"), id.New(id.PrefixTemplate))
	if !errors.Is(err, ctrlplane.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

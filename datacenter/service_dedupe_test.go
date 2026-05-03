package datacenter_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/datacenter"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/testutil"
)

// TestCreate_dedupeReturnsErrAlreadyExists locks the idempotency
// guarantee that platform-shared seeders depend on: the second
// Create with identical (tenant_id, slug) must return
// ErrAlreadyExists so callers can `errors.Is` and skip.
//
// Regression test for the "datacenters keep accumulating on every
// studio restart" bug. Before this fix, postgres/mongo/sqlite/badger
// silently inserted a fresh row on each call.
func TestCreate_dedupeReturnsErrAlreadyExists(t *testing.T) {
	ts := testutil.NewServer(t,
		testutil.WithProvider("docker", &stubProvider{name: "docker", region: "local"}),
	)
	ctx := ts.AdminContext()

	req := datacenter.CreateRequest{
		Name:         "docker",
		ProviderName: "docker",
		Region:       "local",
	}

	if _, err := ts.CP.Datacenters.Create(ctx, req); err != nil {
		t.Fatalf("first Create: %v", err)
	}

	_, err := ts.CP.Datacenters.Create(ctx, req)
	if err == nil {
		t.Fatal("second Create with same name should fail")
	}

	if !errors.Is(err, ctrlplane.ErrAlreadyExists) {
		t.Fatalf("want ErrAlreadyExists, got %v", err)
	}
}

// TestCreate_dedupeRespectsTenantBoundary covers the cross-tenant
// case: a tenant-owned "docker" must not be blocked by a
// platform-shared "docker" (and vice versa). The hybrid-visibility
// query in GetDatacenterBySlug returns either, so the dedupe check
// has to compare tenant IDs explicitly — without that comparison
// every customer would be unable to register their own "docker"
// after the platform seeder wrote a shared one.
func TestCreate_dedupeRespectsTenantBoundary(t *testing.T) {
	ts := testutil.NewServer(t,
		testutil.WithProvider("docker", &stubProvider{name: "docker", region: "local"}),
	)

	// Platform admin (TenantID="") creates the shared "docker" row.
	if _, err := ts.CP.Datacenters.Create(ts.AdminContext(), datacenter.CreateRequest{
		Name:         "docker",
		ProviderName: "docker",
		Region:       "local",
	}); err != nil {
		t.Fatalf("seed shared dc: %v", err)
	}

	// Tenant X must still be able to register its own "docker" — the
	// shared one shouldn't squat the slug in their namespace.
	tenantCtx := auth.WithClaims(context.Background(), &auth.Claims{
		SubjectID: "user-1",
		TenantID:  "tenant-x",
		Roles:     []string{"system:admin"}, // simplifies create gating; tenant-X is its own scope
	})
	if _, err := ts.CP.Datacenters.Create(tenantCtx, datacenter.CreateRequest{
		Name:         "docker",
		ProviderName: "docker",
		Region:       "local",
	}); err != nil {
		t.Fatalf("tenant-X create should succeed: %v", err)
	}

	// And a SECOND tenant-X create of the same slug should fail.
	_, err := ts.CP.Datacenters.Create(tenantCtx, datacenter.CreateRequest{
		Name:         "docker",
		ProviderName: "docker",
		Region:       "local",
	})
	if !errors.Is(err, ctrlplane.ErrAlreadyExists) {
		t.Fatalf("second tenant-X create should fail with ErrAlreadyExists, got %v", err)
	}
}

// stubProvider is a no-op implementation of provider.Provider for
// tests that just need the registry lookup in datacenter.Create to
// succeed. Datacenter.Create only consumes Info() so the rest of
// the interface returns zero values / nil.
type stubProvider struct {
	name   string
	region string
}

func (s *stubProvider) Info() provider.ProviderInfo {
	return provider.ProviderInfo{Name: s.name, Region: s.region}
}
func (s *stubProvider) Capabilities() []provider.Capability { return nil }
func (s *stubProvider) Provision(context.Context, provider.ProvisionRequest) (*provider.ProvisionResult, error) {
	return nil, nil
}
func (s *stubProvider) Deprovision(context.Context, id.ID) error { return nil }
func (s *stubProvider) Start(context.Context, id.ID) error       { return nil }
func (s *stubProvider) Stop(context.Context, id.ID) error        { return nil }
func (s *stubProvider) Restart(context.Context, id.ID) error     { return nil }
func (s *stubProvider) Status(context.Context, id.ID) (*provider.InstanceStatus, error) {
	return nil, nil
}
func (s *stubProvider) Deploy(context.Context, provider.DeployRequest) (*provider.DeployResult, error) {
	return nil, nil
}
func (s *stubProvider) Rollback(context.Context, id.ID, id.ID) error              { return nil }
func (s *stubProvider) Scale(context.Context, id.ID, provider.ResourceSpec) error { return nil }
func (s *stubProvider) Resources(context.Context, id.ID) (*provider.ResourceUsage, error) {
	return nil, nil
}
func (s *stubProvider) Logs(context.Context, id.ID, provider.LogOptions) (io.ReadCloser, error) {
	return nil, nil
}
func (s *stubProvider) Exec(context.Context, id.ID, provider.ExecRequest) (*provider.ExecResult, error) {
	return nil, nil
}

// keep time import live in the unlikely case the test ever needs it
// for deadline contexts; cheaper than a separate import line.
var _ = time.Second

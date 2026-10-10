package contract

import (
	"context"
	"errors"
	"testing"

	dash "github.com/xraph/forge/extensions/dashboard/contract"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/app"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/managed"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/store/memory"
)

type purgePolicy struct {
	auth.NoopProvider

	requests []auth.AuthzRequest
	deny     bool
}

func (p *purgePolicy) Authorize(_ context.Context, r auth.AuthzRequest) (bool, error) {
	p.requests = append(p.requests, r)

	return !p.deny, nil
}

type purgeProviderFixture struct{ provider.Provider }

func (p *purgeProviderFixture) Info() provider.ProviderInfo {
	return provider.ProviderInfo{Name: "fixture"}
}

func TestProviderPurgeExactAuthorizationAndPartialRefusalError(t *testing.T) {
	s := memory.New()
	policy := &purgePolicy{}

	cp, err := app.New(app.WithStore(s), app.WithAuth(policy))
	if err != nil {
		t.Fatal(err)
	}

	cp.Providers().Register("fixture", &purgeProviderFixture{})

	ctx := auth.WithClaims(t.Context(), &auth.Claims{SubjectID: "operator", TenantID: "tenant", Roles: []string{"system:admin"}})

	inst := &instance.Instance{Entity: ctrlplane.NewEntity(id.PrefixInstance), TenantID: "tenant", ProviderName: "fixture", State: provider.StateDestroyed}
	if err := s.Insert(ctx, inst); err != nil {
		t.Fatal(err)
	}

	spec := []byte(`{"image":"fixture"}`)

	identity := managed.Identity{Key: managed.Key{TenantID: "tenant", InstanceID: inst.ID}, ProvisionOperationID: id.New(managed.PrefixOperation), Physical: managed.Physical{Provider: "fixture", Placement: "fixture", Project: inst.ID.String(), Generation: inst.ID.String()}, Manifest: managed.Manifest{Version: 1, Spec: spec, SpecDigest: managed.Digest(spec), Members: []managed.Member{{Name: "worker", Service: "worker", Installation: "installation", Namespace: "namespace", Queue: "queue", Build: "build", ArtifactDigest: managed.Digest(spec), ConfigurationDigest: managed.Digest(spec)}}}}
	if _, err := s.CreateManagedTarget(ctx, identity); err != nil {
		t.Fatal(err)
	}

	policy.deny = true

	if _, err := purgeProvider(ctx, cp, "fixture"); !errors.Is(err, ctrlplane.ErrForbidden) {
		t.Fatal(err)
	}

	if len(policy.requests) != 1 || policy.requests[0].Action != "providers.purge" || policy.requests[0].ResourceID != "fixture" || policy.requests[0].TenantID != "tenant" {
		t.Fatalf("wrong purge request: %+v", policy.requests)
	}

	policy.deny = false
	_, err = purgeProvider(ctx, cp, "fixture")

	var refusal *dash.Error
	if !errors.As(err, &refusal) || refusal.Code != dash.CodeConflict {
		t.Fatalf("expected partial refusal: %v", err)
	}

	summary, ok := refusal.Details["summary"].(purgeSummary)
	if !ok || summary.InstancesDeleted != 0 || summary.WorkloadsDeleted != 0 || len(summary.Failures) != 1 {
		t.Fatalf("refusal counted as deletion: %+v", refusal.Details)
	}

	if _, err := s.GetByID(ctx, "tenant", inst.ID); err != nil {
		t.Fatal(err)
	}
}

func TestUnsupportedBackendMutationRefusesThroughContract(t *testing.T) {
	cp, h := harness(t)

	inst := &instance.Instance{Entity: ctrlplane.NewEntity(id.PrefixInstance), TenantID: "alpha", Name: "retained", State: provider.StateDestroyed}
	if err := cp.Store().Insert(t.Context(), inst); err != nil {
		t.Fatal(err)
	}

	response := request(t, h, principal("alpha", false), dash.KindCommand, "instances.delete", map[string]any{"id": inst.ID.String()})
	if response.OK {
		t.Fatal("unsupported backend mutation accepted")
	}

	if _, err := cp.Store().GetByID(t.Context(), "alpha", inst.ID); err != nil {
		t.Fatal(err)
	}
}

package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/app"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/managed"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/store/memory"
)

func guardIdentity(owner id.ID) managed.Identity {
	iid := id.New(id.PrefixInstance)
	spec := []byte(`{"image":"fixture"}`)

	return managed.Identity{Key: managed.Key{TenantID: "tenant", InstanceID: iid}, ProvisionOperationID: id.New(managed.PrefixOperation), Physical: managed.Physical{Provider: "missing", Placement: "fixture", Project: "cp-" + iid.String(), Generation: iid.String()}, Manifest: managed.Manifest{Version: 1, Spec: spec, SpecDigest: managed.Digest(spec), Members: []managed.Member{{Name: "worker", Service: "worker", OwnerID: owner, Installation: "installation", Namespace: "namespace", Queue: "queue", Build: "build", ArtifactDigest: managed.Digest([]byte("image")), ConfigurationDigest: managed.Digest([]byte("config"))}}}}
}

func TestRESTManagedGuardRefusesDelete(t *testing.T) {
	s := memory.New()

	identity := guardIdentity(id.ID{})
	if _, err := s.CreateManagedTarget(t.Context(), identity); err != nil {
		t.Fatal(err)
	}

	inst := &instance.Instance{Entity: ctrlplane.NewEntity(id.PrefixInstance), TenantID: "tenant", State: provider.StateDestroyed}

	inst.ID = identity.InstanceID
	if err := s.Insert(t.Context(), inst); err != nil {
		t.Fatal(err)
	}

	cp, err := app.New(app.WithStore(s), app.WithAuth(&auth.NoopProvider{DefaultTenantID: "tenant"}))
	if err != nil {
		t.Fatal(err)
	}

	h := New(cp, nil).Handler()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/v1/instances/"+inst.ID.String(), nil)
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)

	if response.Code < 400 || response.Code == http.StatusNotFound {
		t.Fatalf("expected guard failure: %d %s", response.Code, response.Body.String())
	}

	if _, err := s.GetByID(t.Context(), "tenant", inst.ID); err != nil {
		t.Fatalf("REST removed instance: %v", err)
	}
}

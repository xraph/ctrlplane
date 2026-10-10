package worker

import (
	"testing"
	"time"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/event"
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

func TestGCManagedGuardUsesActualPersistence(t *testing.T) {
	s := memory.New()
	events := event.NewInMemoryBus()

	identity := guardIdentity(id.New(id.PrefixWorkload))
	if _, err := s.CreateManagedTarget(t.Context(), identity); err != nil {
		t.Fatal(err)
	}

	inst := &instance.Instance{Entity: ctrlplane.NewEntity(id.PrefixInstance), TenantID: "tenant", State: provider.StateDestroyed, Labels: map[string]string{workloadLabelKey: identity.Manifest.Members[0].OwnerID.String()}}
	inst.ID = identity.InstanceID

	inst.CreatedAt = time.Now().Add(-time.Hour)
	if err := s.Insert(t.Context(), inst); err != nil {
		t.Fatal(err)
	}

	service := instance.NewService(s, provider.NewRegistry(), events, &auth.NoopProvider{}, nil)
	gc := NewGarbageCollector(s, service, s, events, time.Minute, GCConfig{})

	count, err := gc.sweepTenant(t.Context(), "tenant")
	if err != nil || count != 0 {
		t.Fatalf("protected count=%d error=%v", count, err)
	}

	if _, err := s.GetByID(t.Context(), "tenant", inst.ID); err != nil {
		t.Fatalf("GC removed retained identity: %v", err)
	}

	for _, e := range events.RecentEvents(100) {
		if e.Type == event.InstanceDeleted {
			t.Fatal("refusal emitted deletion")
		}
	}
}

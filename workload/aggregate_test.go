package workload

import (
	"context"
	"testing"
	"time"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/network"
)

// TestListDeployments_AggregatesAcrossReplicas verifies the
// per-replica concat semantic — each replica's deployments merge
// into one slice, total tracks the count, items sorted newest-first.
func TestListDeployments_AggregatesAcrossReplicas(t *testing.T) {
	t.Parallel()

	wid := id.New(id.PrefixWorkload)
	insts := newRestartFakeInstances(wid, 3)

	older := time.Now().Add(-2 * time.Hour)
	newer := time.Now().Add(-1 * time.Hour)
	deploys := newFakeDeploys()
	deploys.appendDeployments([]*deploy.Deployment{{Entity: ctrlplane.Entity{ID: id.New(id.PrefixDeployment), CreatedAt: older}}})
	deploys.appendDeployments([]*deploy.Deployment{{Entity: ctrlplane.Entity{ID: id.New(id.PrefixDeployment), CreatedAt: newer}}})

	store := newRestartFakeStore()
	store.put(seedWorkload(wid, 3))

	svc := &service{store: store, instances: insts, deploys: deploys, events: event.NewInMemoryBus()}

	res, err := svc.ListDeployments(adminCtxRestart(), wid, deploy.ListOptions{Limit: 50})
	if err != nil {
		t.Fatalf("ListDeployments: %v", err)
	}

	if len(res.Items) != 2 {
		t.Fatalf("items: want 2 (concat), got %d", len(res.Items))
	}

	if !res.Items[0].CreatedAt.Equal(newer) {
		t.Fatalf("expected newest-first sort; got %v then %v", res.Items[0].CreatedAt, res.Items[1].CreatedAt)
	}

	if res.Total != 2 {
		t.Fatalf("total: want 2, got %d", res.Total)
	}
}

// TestListReleases_DedupsByID verifies the same release returned
// from multiple replicas appears once in the aggregated set.
func TestListReleases_DedupsByID(t *testing.T) {
	t.Parallel()

	wid := id.New(id.PrefixWorkload)
	insts := newRestartFakeInstances(wid, 2)

	sharedRel := &deploy.Release{Entity: ctrlplane.Entity{ID: id.New(id.PrefixRelease), CreatedAt: time.Now()}}
	uniqueRel := &deploy.Release{Entity: ctrlplane.Entity{ID: id.New(id.PrefixRelease), CreatedAt: time.Now().Add(-time.Hour)}}
	deploys := newFakeDeploys()
	deploys.appendReleases([]*deploy.Release{sharedRel, uniqueRel})
	deploys.appendReleases([]*deploy.Release{sharedRel}) // same ID — must collapse on dedup

	store := newRestartFakeStore()
	store.put(seedWorkload(wid, 2))

	svc := &service{store: store, instances: insts, deploys: deploys, events: event.NewInMemoryBus()}

	res, err := svc.ListReleases(adminCtxRestart(), wid, deploy.ListOptions{Limit: 50})
	if err != nil {
		t.Fatalf("ListReleases: %v", err)
	}

	if len(res.Items) != 2 {
		t.Fatalf("items: want 2 (deduped), got %d", len(res.Items))
	}

	if res.Total != 2 {
		t.Fatalf("total: want 2, got %d", res.Total)
	}
}

// TestListDomains_DedupsAndSkipsWhenNoNetwork covers two cases:
// (1) duplicate domains across replicas collapse, (2) nil network
// service yields an empty slice with no error so callers' rendering
// paths stay uniform regardless of the optional dependency.
func TestListDomains_DedupsAndSkipsWhenNoNetwork(t *testing.T) {
	t.Parallel()

	wid := id.New(id.PrefixWorkload)
	insts := newRestartFakeInstances(wid, 2)

	d1 := network.Domain{Entity: ctrlplane.Entity{ID: id.New(id.PrefixDomain)}, Hostname: "a.example"}
	d2 := network.Domain{Entity: ctrlplane.Entity{ID: id.New(id.PrefixDomain)}, Hostname: "b.example"}
	net := newFakeNetwork()
	net.appendDomains([]network.Domain{d1, d2})
	net.appendDomains([]network.Domain{d1}) // same ID → dedup

	store := newRestartFakeStore()
	store.put(seedWorkload(wid, 2))

	// With network wired.
	svc := &service{store: store, instances: insts, network: net, events: event.NewInMemoryBus()}

	got, err := svc.ListDomains(adminCtxRestart(), wid)
	if err != nil {
		t.Fatalf("ListDomains: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("items: want 2 (deduped), got %d", len(got))
	}

	// Without network wired — empty slice, no error.
	svcNoNet := &service{store: store, instances: insts, events: event.NewInMemoryBus()}

	got, err = svcNoNet.ListDomains(adminCtxRestart(), wid)
	if err != nil {
		t.Fatalf("ListDomains (no net): %v", err)
	}

	if len(got) != 0 {
		t.Fatalf("no-network: want empty slice, got %d items", len(got))
	}
}

// --- fakes ---

// fakeDeploys serves preloaded slices in FIFO order — one slice
// per replica per call. Tests append per-replica payloads in the
// order ListInstances will return them.
type fakeDeploys struct {
	deployments [][]*deploy.Deployment
	releases    [][]*deploy.Release
}

func newFakeDeploys() *fakeDeploys {
	return &fakeDeploys{}
}

func (f *fakeDeploys) appendDeployments(items []*deploy.Deployment) {
	f.deployments = append(f.deployments, items)
}

func (f *fakeDeploys) appendReleases(items []*deploy.Release) {
	f.releases = append(f.releases, items)
}

func (f *fakeDeploys) ListDeployments(_ context.Context, _ id.ID, _ deploy.ListOptions) (*deploy.DeployListResult, error) {
	if len(f.deployments) == 0 {
		return &deploy.DeployListResult{}, nil
	}

	items := f.deployments[0]
	f.deployments = f.deployments[1:]

	return &deploy.DeployListResult{Items: items, Total: len(items)}, nil
}

func (f *fakeDeploys) ListReleases(_ context.Context, _ id.ID, _ deploy.ListOptions) (*deploy.ReleaseListResult, error) {
	if len(f.releases) == 0 {
		return &deploy.ReleaseListResult{}, nil
	}

	items := f.releases[0]
	f.releases = f.releases[1:]

	return &deploy.ReleaseListResult{Items: items, Total: len(items)}, nil
}

// Unused — panic if accidentally called.
func (f *fakeDeploys) Deploy(context.Context, deploy.DeployRequest) (*deploy.Deployment, error) {
	panic("not used")
}
func (f *fakeDeploys) Rollback(context.Context, id.ID, id.ID) (*deploy.Deployment, error) {
	panic("not used")
}
func (f *fakeDeploys) Cancel(context.Context, id.ID) error { panic("not used") }
func (f *fakeDeploys) GetDeployment(context.Context, id.ID) (*deploy.Deployment, error) {
	panic("not used")
}
func (f *fakeDeploys) GetRelease(context.Context, id.ID) (*deploy.Release, error) {
	panic("not used")
}

func (f *fakeDeploys) RecordInitial(context.Context, id.ID) (*deploy.Release, error) {
	return nil, nil
}

type fakeNetwork struct {
	domains [][]network.Domain
	routes  [][]network.Route
}

func newFakeNetwork() *fakeNetwork { return &fakeNetwork{} }

func (f *fakeNetwork) appendDomains(items []network.Domain) { f.domains = append(f.domains, items) }
func (f *fakeNetwork) appendRoutes(items []network.Route)   { f.routes = append(f.routes, items) }

func (f *fakeNetwork) ListDomains(_ context.Context, _ id.ID) ([]network.Domain, error) {
	if len(f.domains) == 0 {
		return nil, nil
	}

	items := f.domains[0]
	f.domains = f.domains[1:]

	return items, nil
}

func (f *fakeNetwork) ListRoutes(_ context.Context, _ id.ID) ([]network.Route, error) {
	if len(f.routes) == 0 {
		return nil, nil
	}

	items := f.routes[0]
	f.routes = f.routes[1:]

	return items, nil
}

func (f *fakeNetwork) AddDomain(context.Context, network.AddDomainRequest) (*network.Domain, error) {
	panic("not used")
}
func (f *fakeNetwork) VerifyDomain(context.Context, id.ID) (*network.Domain, error) {
	panic("not used")
}
func (f *fakeNetwork) RemoveDomain(context.Context, id.ID) error { panic("not used") }
func (f *fakeNetwork) AddRoute(context.Context, network.AddRouteRequest) (*network.Route, error) {
	panic("not used")
}
func (f *fakeNetwork) UpdateRoute(context.Context, id.ID, network.UpdateRouteRequest) (*network.Route, error) {
	panic("not used")
}
func (f *fakeNetwork) RemoveRoute(context.Context, id.ID) error { panic("not used") }
func (f *fakeNetwork) ProvisionCert(context.Context, id.ID) (*network.Certificate, error) {
	panic("not used")
}
func (f *fakeNetwork) ListCerts(context.Context, id.ID) ([]network.Certificate, error) {
	panic("not used")
}

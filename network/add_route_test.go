package network

import (
	"context"
	"testing"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/id"
)

// captureStore records the routes AddRoute inserts. Only InsertRoute is
// exercised; every other Store method panics so an accidental new call
// site shows up as a failure rather than a silent nil.
type captureStore struct {
	Store

	seeded   *Route
	inserted []*Route
	updated  []*Route
}

func (s *captureStore) InsertRoute(_ context.Context, route *Route) error {
	s.inserted = append(s.inserted, route)

	return nil
}

// GetRoute hands back the seeded route. UpdateRoute mutates it in place,
// so `seeded` doubles as the persisted copy the assertions read back.
func (s *captureStore) GetRoute(_ context.Context, _ string, _ id.ID) (*Route, error) {
	if s.seeded == nil {
		return nil, ctrlplane.ErrNotFound
	}

	return s.seeded, nil
}

func (s *captureStore) UpdateRoute(_ context.Context, route *Route) error {
	s.updated = append(s.updated, route)

	return nil
}

// newTestService wires a service with a capture store, no router, and a
// real in-memory bus.
func newTestService(t *testing.T) (*service, *captureStore) {
	t.Helper()

	st := &captureStore{}

	return &service{
		store:  st,
		events: event.NewInMemoryBus(),
	}, st
}

// testContext returns a context carrying claims, which AddRoute requires.
func testContext(t *testing.T) context.Context {
	t.Helper()

	return auth.WithClaims(context.Background(), &auth.Claims{
		SubjectID: "user-1",
		TenantID:  "tenant-x",
	})
}

// TestAddRoute_TLSVerifyDefaultsTrue pins the one asymmetric default in
// the proxy fields. TLSVerify is the only one whose safe value is true,
// so AddRouteRequest carries it as *bool: a caller that omits the field
// must get verification on, not off. Getting this backwards would
// silently disable upstream certificate checking for every route
// created by a client that predates the field.
func TestAddRoute_TLSVerifyDefaultsTrue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  *bool
		want bool
	}{
		{"unset defaults to true", nil, true},
		{"explicit true stays true", new(true), true},
		{"explicit false is honoured", new(false), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc, st := newTestService(t)

			route, err := svc.AddRoute(testContext(t), AddRouteRequest{
				InstanceID: id.New(id.PrefixInstance),
				Path:       "/api",
				Port:       8080,
				TLSVerify:  tt.req,
			})
			if err != nil {
				t.Fatalf("AddRoute: %v", err)
			}

			if route.TLSVerify != tt.want {
				t.Errorf("returned TLSVerify = %v, want %v", route.TLSVerify, tt.want)
			}

			if len(st.inserted) != 1 {
				t.Fatalf("inserted %d routes, want 1", len(st.inserted))
			}

			if st.inserted[0].TLSVerify != tt.want {
				t.Errorf("persisted TLSVerify = %v, want %v", st.inserted[0].TLSVerify, tt.want)
			}
		})
	}
}

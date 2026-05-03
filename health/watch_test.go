package health

import (
	"context"
	"sync"
	"testing"
	"time"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/id"
)

// TestService_Watch_fansOutToMultipleSubscribers asserts two
// concurrent Watch subscribers on the same instance both receive
// every HealthResult that lands in the store via RunCheck.
func TestService_Watch_fansOutToMultipleSubscribers(t *testing.T) {
	svc, instanceID, checkID := setupWatchHarness(t)

	ctxA, cancelA := context.WithCancel(adminCtx())
	defer cancelA()
	chA, err := svc.Watch(ctxA, instanceID)
	if err != nil {
		t.Fatalf("Watch A: %v", err)
	}
	ctxB, cancelB := context.WithCancel(adminCtx())
	defer cancelB()
	chB, err := svc.Watch(ctxB, instanceID)
	if err != nil {
		t.Fatalf("Watch B: %v", err)
	}

	if _, err := svc.RunCheck(adminCtx(), checkID); err != nil {
		t.Fatalf("RunCheck: %v", err)
	}

	for i, ch := range []<-chan *HealthResult{chA, chB} {
		select {
		case r := <-ch:
			if r == nil {
				t.Fatalf("subscriber %d: nil result", i)
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d: timeout waiting for result", i)
		}
	}
}

// TestService_Watch_closesOnContextCancel guarantees the Watch
// channel is closed when the consumer cancels its context.
func TestService_Watch_closesOnContextCancel(t *testing.T) {
	svc, instanceID, _ := setupWatchHarness(t)

	ctx, cancel := context.WithCancel(adminCtx())
	ch, err := svc.Watch(ctx, instanceID)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}

	cancel()

	select {
	case _, ok := <-ch:
		if !ok {
			return // channel closed cleanly
		}
		// drained one (residual) value; next read should be the close
		select {
		case _, ok := <-ch:
			if ok {
				t.Fatal("expected channel to be closed after cancel")
			}
		case <-time.After(time.Second):
			t.Fatal("channel not closed within 1s of cancel")
		}
	case <-time.After(time.Second):
		t.Fatal("no read on channel within 1s of cancel")
	}
}

// TestService_Watch_dropsOnSlowConsumer asserts the buffered-drop
// semantics: a Watch consumer that never drains shouldn't be able
// to backpressure RunCheck (and therefore the worker pipeline).
func TestService_Watch_dropsOnSlowConsumer(t *testing.T) {
	svc, instanceID, checkID := setupWatchHarness(t)

	ctxStuck, cancelStuck := context.WithCancel(adminCtx())
	defer cancelStuck()
	if _, err := svc.Watch(ctxStuck, instanceID); err != nil {
		t.Fatalf("Watch stuck: %v", err)
	}

	done := make(chan struct{})
	go func() {
		for i := 0; i < 50; i++ {
			_, _ = svc.RunCheck(adminCtx(), checkID)
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunCheck blocked on stuck consumer for >2s")
	}
}

// --- harness ---

func setupWatchHarness(t *testing.T) (Service, id.ID, id.ID) {
	t.Helper()
	store := newFakeStore()
	svc := NewService(store, event.NewInMemoryBus(), &auth.NoopProvider{})
	svc.RegisterChecker(&fakeChecker{})

	instanceID := id.New(id.PrefixInstance)

	check := &HealthCheck{
		Entity:     ctrlplane.NewEntity(id.PrefixHealthCheck),
		TenantID:   "test-tenant",
		InstanceID: instanceID,
		Name:       "test",
		Type:       "fake",
		Target:     "fake://",
		Interval:   1 * time.Second,
		Timeout:    1 * time.Second,
		Retries:    1,
		Enabled:    true,
	}
	if err := store.InsertCheck(context.Background(), check); err != nil {
		t.Fatalf("insert check: %v", err)
	}
	return svc, instanceID, check.ID
}

func adminCtx() context.Context {
	return auth.WithClaims(context.Background(), &auth.Claims{
		SubjectID: "test",
		TenantID:  "test-tenant",
		Roles:     []string{"system:admin"},
	})
}

// fakeStore is a minimal in-package Store impl. Avoids the import
// cycle that would result from depending on store/memory (memory
// imports health for its types).
type fakeStore struct {
	mu      sync.Mutex
	checks  map[string]*HealthCheck
	results []*HealthResult
}

func newFakeStore() *fakeStore {
	return &fakeStore{checks: make(map[string]*HealthCheck)}
}

func (s *fakeStore) InsertCheck(_ context.Context, c *HealthCheck) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checks[c.ID.String()] = c
	return nil
}

func (s *fakeStore) GetCheck(_ context.Context, _ string, checkID id.ID) (*HealthCheck, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.checks[checkID.String()]
	if !ok {
		return nil, ctrlplane.ErrNotFound
	}
	return c, nil
}

func (s *fakeStore) ListChecks(_ context.Context, _ string, _ id.ID) ([]HealthCheck, error) {
	return nil, nil
}

func (s *fakeStore) UpdateCheck(_ context.Context, c *HealthCheck) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checks[c.ID.String()] = c
	return nil
}

func (s *fakeStore) DeleteCheck(_ context.Context, _ string, checkID id.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.checks, checkID.String())
	return nil
}

func (s *fakeStore) InsertResult(_ context.Context, r *HealthResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.results = append(s.results, r)
	return nil
}

func (s *fakeStore) ListResults(_ context.Context, _ string, _ id.ID, _ HistoryOptions) ([]HealthResult, error) {
	return nil, nil
}

func (s *fakeStore) GetLatestResult(_ context.Context, _ string, checkID id.ID) (*HealthResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.results) - 1; i >= 0; i-- {
		if s.results[i].CheckID == checkID {
			return s.results[i], nil
		}
	}
	return nil, ctrlplane.ErrNotFound
}

// fakeChecker always returns a healthy result.
type fakeChecker struct{}

func (f *fakeChecker) Type() CheckType { return "fake" }

func (f *fakeChecker) Check(_ context.Context, c *HealthCheck) (*HealthResult, error) {
	return &HealthResult{
		Entity:     ctrlplane.NewEntity(id.PrefixHealthResult),
		CheckID:    c.ID,
		InstanceID: c.InstanceID,
		TenantID:   c.TenantID,
		Status:     StatusHealthy,
		CheckedAt:  time.Now(),
	}, nil
}

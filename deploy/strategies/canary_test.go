package strategies

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

// fakeProvider records each Deploy call's services so a test can
// assert the strategy's per-service rollout pattern.
type fakeProvider struct {
	deploys      []provider.DeployRequest
	failOnNumber int // 1-indexed; 0 disables
	callCount    int
}

// Capability stubs to satisfy provider.Provider.
func (f *fakeProvider) Info() provider.ProviderInfo         { return provider.ProviderInfo{Name: "fake"} }
func (f *fakeProvider) Capabilities() []provider.Capability { return nil }
func (f *fakeProvider) Provision(_ context.Context, _ provider.ProvisionRequest) (*provider.ProvisionResult, error) {
	return &provider.ProvisionResult{}, nil
}
func (f *fakeProvider) Deprovision(_ context.Context, _ id.ID) error { return nil }
func (f *fakeProvider) Start(_ context.Context, _ id.ID) error       { return nil }
func (f *fakeProvider) Stop(_ context.Context, _ id.ID) error        { return nil }
func (f *fakeProvider) Restart(_ context.Context, _ id.ID) error     { return nil }
func (f *fakeProvider) Status(_ context.Context, _ id.ID) (*provider.InstanceStatus, error) {
	return &provider.InstanceStatus{}, nil
}
func (f *fakeProvider) Deploy(_ context.Context, req provider.DeployRequest) (*provider.DeployResult, error) {
	f.callCount++

	f.deploys = append(f.deploys, req)
	if f.failOnNumber > 0 && f.callCount == f.failOnNumber {
		return nil, errors.New("simulated provider failure")
	}

	return &provider.DeployResult{Status: "deployed"}, nil
}
func (f *fakeProvider) Rollback(_ context.Context, _ id.ID, _ id.ID) error { return nil }
func (f *fakeProvider) Scale(_ context.Context, _ id.ID, _ provider.ResourceSpec) error {
	return nil
}
func (f *fakeProvider) Resources(_ context.Context, _ id.ID) (*provider.ResourceUsage, error) {
	return &provider.ResourceUsage{}, nil
}
func (f *fakeProvider) Logs(_ context.Context, _ id.ID, _ provider.LogOptions) (io.ReadCloser, error) {
	return nil, nil
}
func (f *fakeProvider) Exec(_ context.Context, _ id.ID, _ provider.ExecRequest) (*provider.ExecResult, error) {
	return &provider.ExecResult{}, nil
}

// TestCanary_RollsServicesOneAtATime verifies the canary strategy
// issues one Deploy call per service in declaration order, and
// reports per-service progress.
func TestCanary_RollsServicesOneAtATime(t *testing.T) {
	t.Parallel()

	dep := &deploy.Deployment{
		Services: []provider.ServiceDeploySpec{
			{Name: "api", Image: "api:v2"},
			{Name: "web", Image: "web:v2"},
			{Name: "worker", Image: "worker:v2"},
		},
	}

	provider := &fakeProvider{}

	progress := map[string][]string{}

	deployStrategy := NewCanary()

	err := deployStrategy.Execute(context.Background(), deploy.StrategyParams{
		Deployment: dep,
		Provider:   provider,
		OnProgress: func(_ string, _ int, _ string) {},
		OnServiceProgress: func(name, state string) {
			progress[name] = append(progress[name], state)
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if provider.callCount != 3 {
		t.Fatalf("provider Deploy calls: want 3, got %d", provider.callCount)
	}

	wantOrder := []string{"api", "web", "worker"}
	for i, want := range wantOrder {
		if len(provider.deploys[i].Services) != 1 {
			t.Fatalf("call[%d].Services: want 1 service per call, got %d", i, len(provider.deploys[i].Services))
		}

		if provider.deploys[i].Services[0].Name != want {
			t.Fatalf("call[%d]: want service %q, got %q", i, want, provider.deploys[i].Services[0].Name)
		}
	}

	for _, svc := range wantOrder {
		got := progress[svc]
		if len(got) < 2 || got[0] != deploy.ServiceStateRunning || got[len(got)-1] != deploy.ServiceStateSucceeded {
			t.Fatalf("progress for %q: want running→succeeded, got %v", svc, got)
		}
	}
}

// TestCanary_AbortsOnFirstFailure verifies the strategy stops at the
// first service failure and leaves later services in their initial
// state (the deploy.Service initialises them as "pending").
func TestCanary_AbortsOnFirstFailure(t *testing.T) {
	t.Parallel()

	dep := &deploy.Deployment{
		Services: []provider.ServiceDeploySpec{
			{Name: "api", Image: "api:v2"},
			{Name: "web", Image: "web:v2"}, // this one will fail
			{Name: "worker", Image: "worker:v2"},
		},
	}

	provider := &fakeProvider{failOnNumber: 2}

	progress := map[string]string{}

	deployStrategy := NewCanary()

	err := deployStrategy.Execute(context.Background(), deploy.StrategyParams{
		Deployment: dep,
		Provider:   provider,
		OnProgress: func(_ string, _ int, _ string) {},
		OnServiceProgress: func(name, state string) {
			progress[name] = state
		},
	})
	if err == nil {
		t.Fatalf("expected error from canary, got nil")
	}

	if provider.callCount != 2 {
		t.Fatalf("expected only 2 Deploy calls (abort on second), got %d", provider.callCount)
	}

	if progress["api"] != deploy.ServiceStateSucceeded {
		t.Fatalf("api should have succeeded, got %q", progress["api"])
	}

	if progress["web"] != deploy.ServiceStateFailed {
		t.Fatalf("web should be failed, got %q", progress["web"])
	}

	if _, touched := progress["worker"]; touched {
		t.Fatalf("worker should not have been touched, got %q", progress["worker"])
	}
}

// TestRolling_AdvancesAllInLockstep verifies the rolling strategy
// marks every service running then succeeded with one Deploy call.
func TestRolling_AdvancesAllInLockstep(t *testing.T) {
	t.Parallel()

	dep := &deploy.Deployment{
		Services: []provider.ServiceDeploySpec{
			{Name: "api", Image: "api:v2"},
			{Name: "worker", Image: "worker:v2"},
		},
	}

	provider := &fakeProvider{}
	progress := map[string][]string{}

	deployStrategy := NewRolling()

	err := deployStrategy.Execute(context.Background(), deploy.StrategyParams{
		Deployment: dep,
		Provider:   provider,
		OnProgress: func(_ string, _ int, _ string) {},
		OnServiceProgress: func(name, state string) {
			progress[name] = append(progress[name], state)
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if provider.callCount != 1 {
		t.Fatalf("rolling: want 1 Deploy call, got %d", provider.callCount)
	}

	for _, name := range []string{"api", "worker"} {
		got := progress[name]
		if len(got) != 2 || got[0] != deploy.ServiceStateRunning || got[1] != deploy.ServiceStateSucceeded {
			t.Fatalf("progress for %q: want [running, succeeded], got %v", name, got)
		}
	}
}

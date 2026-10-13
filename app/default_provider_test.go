package app

import (
	"context"
	"testing"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/store/memory"
)

// stubProvider implements only what instance creation calls. The embedded
// interface is nil, so any other method panics and fails the test loudly.
type stubProvider struct {
	provider.Provider

	name       string
	provisions int
}

func (p *stubProvider) Info() provider.ProviderInfo { return provider.ProviderInfo{Name: p.name} }

func (p *stubProvider) Provision(context.Context, provider.ProvisionRequest) (*provider.ProvisionResult, error) {
	p.provisions++

	return &provider.ProvisionResult{ProviderRef: "ref"}, nil
}

func TestNew_AppliesConfigDefaultProvider(t *testing.T) {
	t.Parallel()

	fallback := &stubProvider{name: "fallback"}
	other := &stubProvider{name: "other"}

	tests := []struct {
		name string
		opts []Option
		want *stubProvider
	}{
		{
			name: "config default provider is used",
			opts: []Option{
				WithConfig(ctrlplane.Config{DefaultProvider: "fallback"}),
			},
			want: fallback,
		},
		{
			name: "explicit WithDefaultProvider wins over config",
			opts: []Option{
				WithDefaultProvider("other"),
				WithConfig(ctrlplane.Config{DefaultProvider: "fallback"}),
			},
			want: other,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			opts := append([]Option{
				WithStore(memory.New()),
				WithProvider("fallback", fallback),
				WithProvider("other", other),
			}, tt.opts...)

			cp, err := New(opts...)
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			got, err := cp.Providers().Default()
			if err != nil {
				t.Fatalf("Default: %v", err)
			}

			if got != tt.want {
				t.Fatalf("Default() = %v, want %v", got.Info().Name, tt.want.name)
			}
		})
	}
}

// TestCreateInstance_UsesConfigDefaultProvider covers the user-facing
// effect: a create request that names no provider and no datacenter lands
// on Config.DefaultProvider.
func TestCreateInstance_UsesConfigDefaultProvider(t *testing.T) {
	t.Parallel()

	fallback := &stubProvider{name: "fallback"}

	cp, err := New(
		WithStore(memory.New()),
		WithProvider("fallback", fallback),
		WithConfig(ctrlplane.Config{DefaultProvider: "fallback"}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx := auth.WithClaims(context.Background(), &auth.Claims{
		SubjectID: "user_1",
		TenantID:  "tenant-a",
		Roles:     []string{"system:admin"},
	})

	inst, err := cp.Instances.Create(ctx, instance.CreateRequest{
		Name:     "api",
		Services: []provider.ServiceSpec{{Name: "api", Image: "nginx:1.27", Role: provider.RoleMain}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if fallback.provisions != 1 {
		t.Fatalf("Provision calls = %d, want 1", fallback.provisions)
	}

	if inst.ProviderName != "fallback" {
		t.Fatalf("ProviderName = %q, want %q", inst.ProviderName, "fallback")
	}
}

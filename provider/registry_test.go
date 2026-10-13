package provider

import (
	"errors"
	"testing"
)

// namedProvider reports a fixed Info().Name. Nothing else is called.
type namedProvider struct {
	Provider

	name string
}

func (p *namedProvider) Info() ProviderInfo { return ProviderInfo{Name: p.name} }

func TestRegistry_Resolve(t *testing.T) {
	t.Parallel()

	connA := &namedProvider{name: "cloudrun"}
	connB := &namedProvider{name: "ecs"}
	ecsTwo := &namedProvider{name: "ecs"}
	docker := &namedProvider{name: "docker"}

	r := NewRegistry()
	r.Register("gcp-conn-a", connA)
	r.Register("aws-conn-b", connB)
	r.Register("aws-conn-c", ecsTwo)
	r.Register("docker", docker)

	tests := []struct {
		name     string
		lookup   string
		wantKey  string
		wantProv Provider
		wantErr  bool
	}{
		{"exact registry key", "gcp-conn-a", "gcp-conn-a", connA, false},
		{"key equal to name", "docker", "docker", docker, false},
		{"legacy row holding a unique Info().Name", "cloudrun", "gcp-conn-a", connA, false},
		{"name shared by two providers is ambiguous", "ecs", "", nil, true},
		{"unknown", "nomad", "", nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			key, p, err := r.Resolve(tt.lookup)
			if tt.wantErr {
				if !errors.Is(err, ErrProviderNotFound) {
					t.Fatalf("Resolve(%q) error = %v, want ErrProviderNotFound", tt.lookup, err)
				}

				return
			}

			if err != nil {
				t.Fatalf("Resolve(%q): %v", tt.lookup, err)
			}

			if key != tt.wantKey || p != tt.wantProv {
				t.Fatalf("Resolve(%q) = (%q, %v), want (%q, %v)", tt.lookup, key, p, tt.wantKey, tt.wantProv)
			}

			got, err := r.Get(tt.lookup)
			if err != nil || got != tt.wantProv {
				t.Fatalf("Get(%q) = (%v, %v), want %v", tt.lookup, got, err, tt.wantProv)
			}
		})
	}
}

func TestRegistry_ResolveDefault(t *testing.T) {
	t.Parallel()

	conn := &namedProvider{name: "cloudrun"}

	r := NewRegistry()
	r.Register("gcp-conn-a", conn)
	r.SetDefault("gcp-conn-a")

	key, p, err := r.ResolveDefault()
	if err != nil {
		t.Fatalf("ResolveDefault: %v", err)
	}

	if key != "gcp-conn-a" || p != conn {
		t.Fatalf("ResolveDefault() = (%q, %v), want (%q, %v)", key, p, "gcp-conn-a", conn)
	}
}

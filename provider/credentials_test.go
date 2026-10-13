package provider

import (
	"context"
	"errors"
	"testing"
)

func TestRegistry_AcquireAttachesCredentials(t *testing.T) {
	t.Parallel()

	conn := &namedProvider{name: "cloudrun"}

	var seenKeys []string

	r := NewRegistry()
	r.Register("gcp-conn-1", conn)

	// No resolver: the context comes back without credentials.
	ctx, p, err := r.Acquire(context.Background(), "gcp-conn-1")
	if err != nil || p != conn {
		t.Fatalf("Acquire without resolver = (%v, %v)", p, err)
	}

	if _, ok := CredentialsFromContext(ctx); ok {
		t.Fatal("credentials attached although no resolver is set")
	}

	r.SetCredentialsResolver(CredentialsResolverFunc(func(_ context.Context, key string) (*Credentials, error) {
		seenKeys = append(seenKeys, key)

		return &Credentials{Values: map[string]string{"token": "t-" + key}}, nil
	}))

	// Looking the provider up by its Info().Name still hands the resolver
	// the registry key.
	for _, lookup := range []string{"gcp-conn-1", "cloudrun"} {
		ctx, _, err := r.Acquire(context.Background(), lookup)
		if err != nil {
			t.Fatalf("Acquire(%q): %v", lookup, err)
		}

		creds, ok := CredentialsFromContext(ctx)
		if !ok || creds.Values["token"] != "t-gcp-conn-1" {
			t.Fatalf("Acquire(%q) credentials = %+v, %v", lookup, creds, ok)
		}
	}

	if len(seenKeys) != 2 || seenKeys[0] != "gcp-conn-1" || seenKeys[1] != "gcp-conn-1" {
		t.Fatalf("resolver saw keys %v, want the registry key once per call", seenKeys)
	}
}

func TestRegistry_AcquireResolverError(t *testing.T) {
	t.Parallel()

	errVault := errors.New("vault sealed")

	r := NewRegistry()
	r.Register("gcp-conn-1", &namedProvider{name: "cloudrun"})
	r.SetCredentialsResolver(CredentialsResolverFunc(func(context.Context, string) (*Credentials, error) {
		return nil, errVault
	}))

	if _, _, err := r.Acquire(context.Background(), "gcp-conn-1"); !errors.Is(err, errVault) {
		t.Fatalf("Acquire error = %v, want it to wrap %v", err, errVault)
	}
}

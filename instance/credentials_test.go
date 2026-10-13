package instance

import (
	"context"
	"fmt"
	"testing"

	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

// credProvider records the per-call credentials it was handed.
type credProvider struct {
	*delProvider

	seen []string
}

func (p *credProvider) record(ctx context.Context) {
	creds, ok := provider.CredentialsFromContext(ctx)
	if !ok {
		p.seen = append(p.seen, "<none>")

		return
	}

	p.seen = append(p.seen, creds.Values["token"])
}

func (p *credProvider) Provision(ctx context.Context, req provider.ProvisionRequest) (*provider.ProvisionResult, error) {
	p.record(ctx)

	return p.delProvider.Provision(ctx, req)
}

func (p *credProvider) Stop(ctx context.Context, _ id.ID) error {
	p.record(ctx)

	return nil
}

// TestCredentialsResolvedPerCall rotates the token on every resolve and
// checks each provider call sees a fresh one, keyed by the registry key.
func TestCredentialsResolvedPerCall(t *testing.T) {
	t.Parallel()

	prov := &credProvider{delProvider: newDelProvider("cloudrun")}
	registry := provider.NewRegistry()
	registry.Register("gcp-conn-1", prov)

	calls := 0

	registry.SetCredentialsResolver(provider.CredentialsResolverFunc(func(_ context.Context, key string) (*provider.Credentials, error) {
		calls++

		return &provider.Credentials{Values: map[string]string{"token": fmt.Sprintf("%s#%d", key, calls)}}, nil
	}))

	svc := NewService(newDelStore(), registry, event.NewInMemoryBus(), &guardAuthorizer{}, nil)

	inst, err := svc.Create(srcCtx(), CreateRequest{
		Name:         "api",
		ProviderName: "gcp-conn-1",
		Services:     []provider.ServiceSpec{{Name: "api", Image: "nginx:1.27", Role: provider.RoleMain}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.Stop(srcCtx(), inst.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	want := []string{"gcp-conn-1#1", "gcp-conn-1#2"}
	if fmt.Sprint(prov.seen) != fmt.Sprint(want) {
		t.Fatalf("credentials seen by Provision then Stop = %v, want %v", prov.seen, want)
	}
}

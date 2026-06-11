package kubernetes

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

// newArgoTestProvider builds a Provider wired to a fake dynamic client with
// the argo namespace configured.
func newArgoTestProvider() *Provider {
	return &Provider{
		cfg:     Config{Namespace: "default", ArgoNamespace: "argocd"},
		dynamic: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()),
	}
}

func argoReq(instanceID id.ID) provider.ArgoApplyRequest {
	return provider.ArgoApplyRequest{
		InstanceID: instanceID,
		TenantID:   "ten_1",
		App: provider.ArgoCDSource{
			RepoURL:       "https://github.com/acme/repo.git",
			Path:          "apps/web",
			DestNamespace: "prod",
		},
		Labels: map[string]string{labelInstanceID: instanceID.String()},
	}
}

func TestArgoApply(t *testing.T) {
	p := newArgoTestProvider()
	ctx := context.Background()
	instID := id.New(id.PrefixInstance)

	res, err := p.ArgoApply(ctx, argoReq(instID))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	if res.ProviderRef == "" {
		t.Error("expected a provider ref")
	}

	got, err := p.dynamic.Resource(argoGVR).Namespace("argocd").Get(ctx, deploymentName(instID), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get application: %v", err)
	}

	if repo, _, _ := unstructured.NestedString(got.Object, "spec", "source", "repoURL"); repo != "https://github.com/acme/repo.git" {
		t.Errorf("repoURL = %q", repo)
	}

	if got.GetLabels()[labelInstanceID] != instID.String() {
		t.Errorf("missing instance label: %v", got.GetLabels())
	}

	// Re-apply updates in place.
	if _, err := p.ArgoApply(ctx, argoReq(instID)); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
}

func TestArgoStateFor(t *testing.T) {
	tests := []struct {
		sync   string
		health string
		want   provider.InstanceState
	}{
		{"Synced", "Healthy", provider.StateRunning},
		{"OutOfSync", "Healthy", provider.StateStarting},
		{"Synced", "Progressing", provider.StateStarting},
		{"Synced", "Degraded", provider.StateFailed},
		{"Synced", "Suspended", provider.StateStopped},
		{"OutOfSync", "Missing", provider.StateProvisioning},
		{"", "", provider.StateProvisioning},
	}

	for _, tt := range tests {
		t.Run(tt.sync+"/"+tt.health, func(t *testing.T) {
			if got := argoStateFor(tt.sync, tt.health); got != tt.want {
				t.Errorf("argoStateFor(%q, %q) = %s, want %s", tt.sync, tt.health, got, tt.want)
			}
		})
	}
}

func TestBuildArgoApplication(t *testing.T) {
	instID := id.New(id.PrefixInstance)
	req := provider.ArgoApplyRequest{
		InstanceID: instID,
		TenantID:   "ten_1",
		App: provider.ArgoCDSource{
			RepoURL:        "https://github.com/acme/repo.git",
			Path:           "apps/web",
			TargetRevision: "main",
			DestNamespace:  "prod",
			SyncPolicy:     provider.ArgoSyncPolicy{Automated: true, SelfHeal: true, Prune: true},
		},
		Labels: map[string]string{labelInstanceID: instID.String()},
	}

	u, err := buildArgoApplication(req, "myapp", "argocd")
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if u.GetAPIVersion() != "argoproj.io/v1alpha1" || u.GetKind() != "Application" {
		t.Errorf("gvk = %s/%s", u.GetAPIVersion(), u.GetKind())
	}

	if u.GetName() != "myapp" || u.GetNamespace() != "argocd" {
		t.Errorf("name/ns = %s/%s", u.GetName(), u.GetNamespace())
	}

	if u.GetLabels()[labelInstanceID] != instID.String() {
		t.Errorf("missing instance label: %v", u.GetLabels())
	}

	if project, _, _ := unstructured.NestedString(u.Object, "spec", "project"); project != "default" {
		t.Errorf("project = %q, want default", project)
	}

	if repo, _, _ := unstructured.NestedString(u.Object, "spec", "source", "repoURL"); repo != "https://github.com/acme/repo.git" {
		t.Errorf("repoURL = %q", repo)
	}

	if server, _, _ := unstructured.NestedString(u.Object, "spec", "destination", "server"); server != "https://kubernetes.default.svc" {
		t.Errorf("dest server = %q, want in-cluster default", server)
	}

	if ns, _, _ := unstructured.NestedString(u.Object, "spec", "destination", "namespace"); ns != "prod" {
		t.Errorf("dest namespace = %q", ns)
	}

	if selfHeal, _, _ := unstructured.NestedBool(u.Object, "spec", "syncPolicy", "automated", "selfHeal"); !selfHeal {
		t.Errorf("selfHeal not set")
	}
}

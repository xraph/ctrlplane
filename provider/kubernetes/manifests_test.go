package kubernetes

import (
	"context"
	"encoding/json"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

var depGVR = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}

// manifestReq builds a two-document apply request for the given instance.
func manifestReq(instanceID id.ID) provider.ManifestApplyRequest {
	return provider.ManifestApplyRequest{
		InstanceID: instanceID,
		TenantID:   "ten_1",
		Manifests: provider.RenderedManifests{Docs: []string{
			"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm1\ndata:\n  k: v\n",
			"apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: dep1\n",
		}},
	}
}


// testMapper returns a RESTMapper that knows the resource kinds used in
// these tests, standing in for discovery-backed mapping in production.
func testMapper() meta.RESTMapper {
	m := meta.NewDefaultRESTMapper([]schema.GroupVersion{{Version: "v1"}, {Group: "apps", Version: "v1"}})
	m.Add(schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"}, meta.RESTScopeNamespace)
	m.Add(schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}, meta.RESTScopeNamespace)

	return m
}

// cmObject builds an unstructured ConfigMap in the default namespace.
func cmObject(name string, data map[string]any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata":   map[string]any{"name": name, "namespace": "default"},
		"data":       data,
	}}
}

// newManifestTestProvider builds a Provider wired to a fake dynamic client
// and the test RESTMapper.
func newManifestTestProvider() *Provider {
	return &Provider{
		cfg:     Config{Namespace: "default"},
		dynamic: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()),
		mapper:  testMapper(),
	}
}

func TestApplyObject_CreateThenUpdate(t *testing.T) {
	p := newManifestTestProvider()
	ctx := context.Background()

	if err := p.applyObject(ctx, cmObject("cm1", map[string]any{"k": "v1"})); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := p.dynamic.Resource(configMapGVR).Namespace("default").Get(ctx, "cm1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get after create: %v", err)
	}

	if data, _, _ := unstructured.NestedString(got.Object, "data", "k"); data != "v1" {
		t.Errorf("data.k = %q, want v1", data)
	}

	if err := p.applyObject(ctx, cmObject("cm1", map[string]any{"k": "v2"})); err != nil {
		t.Fatalf("update: %v", err)
	}

	got2, err := p.dynamic.Resource(configMapGVR).Namespace("default").Get(ctx, "cm1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}

	if data, _, _ := unstructured.NestedString(got2.Object, "data", "k"); data != "v2" {
		t.Errorf("data.k = %q, want v2", data)
	}
}

func TestParseManifests(t *testing.T) {
	docs := []string{
		"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm1\ndata:\n  k: v\n",
		"apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: dep1\n",
	}

	objs, err := parseManifests(docs)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if len(objs) != 2 {
		t.Fatalf("objs = %d, want 2", len(objs))
	}

	if objs[0].GetKind() != "ConfigMap" || objs[0].GetName() != "cm1" {
		t.Errorf("obj0 = %s/%s", objs[0].GetKind(), objs[0].GetName())
	}

	if objs[1].GetAPIVersion() != "apps/v1" || objs[1].GetName() != "dep1" {
		t.Errorf("obj1 = %s %s", objs[1].GetAPIVersion(), objs[1].GetName())
	}
}

func TestParseManifests_MissingKind(t *testing.T) {
	if _, err := parseManifests([]string{"apiVersion: v1\nmetadata:\n  name: x\n"}); err == nil {
		t.Fatal("expected error for manifest without kind")
	}
}

func TestApplyManifests(t *testing.T) {
	p := newManifestTestProvider()
	ctx := context.Background()
	instID := id.New(id.PrefixInstance)

	res, err := p.ApplyManifests(ctx, manifestReq(instID))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	if res.ProviderRef == "" {
		t.Error("expected a provider ref")
	}

	cm, err := p.dynamic.Resource(configMapGVR).Namespace("default").Get(ctx, "cm1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get cm1: %v", err)
	}

	if cm.GetLabels()[labelInstanceID] != instID.String() {
		t.Errorf("cm1 missing instance label: %v", cm.GetLabels())
	}

	if _, err := p.dynamic.Resource(depGVR).Namespace("default").Get(ctx, "dep1", metav1.GetOptions{}); err != nil {
		t.Fatalf("get dep1: %v", err)
	}

	track, err := p.dynamic.Resource(configMapGVR).Namespace("default").Get(ctx, trackingName(instID), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get tracking configmap: %v", err)
	}

	refsJSON, _, _ := unstructured.NestedString(track.Object, "data", "refs")

	var refs []objectRef
	if err := json.Unmarshal([]byte(refsJSON), &refs); err != nil {
		t.Fatalf("unmarshal refs: %v", err)
	}

	if len(refs) != 2 {
		t.Errorf("tracked refs = %d, want 2", len(refs))
	}
}

func TestDeleteManifests(t *testing.T) {
	p := newManifestTestProvider()
	ctx := context.Background()
	instID := id.New(id.PrefixInstance)

	if _, err := p.ApplyManifests(ctx, manifestReq(instID)); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if err := p.DeleteManifests(ctx, instID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err := p.dynamic.Resource(configMapGVR).Namespace("default").Get(ctx, "cm1", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("cm1 should be gone, got err=%v", err)
	}

	if _, err := p.dynamic.Resource(depGVR).Namespace("default").Get(ctx, "dep1", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("dep1 should be gone, got err=%v", err)
	}

	if _, err := p.dynamic.Resource(configMapGVR).Namespace("default").Get(ctx, trackingName(instID), metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("tracking configmap should be gone, got err=%v", err)
	}

	// Deleting again with no tracking is a no-op.
	if err := p.DeleteManifests(ctx, instID); err != nil {
		t.Fatalf("second delete should be no-op, got %v", err)
	}
}

func TestManifestStatus(t *testing.T) {
	p := newManifestTestProvider()
	ctx := context.Background()
	instID := id.New(id.PrefixInstance)

	if _, err := p.ApplyManifests(ctx, manifestReq(instID)); err != nil {
		t.Fatalf("apply: %v", err)
	}

	st, err := p.ManifestStatus(ctx, instID)
	if err != nil {
		t.Fatalf("status: %v", err)
	}

	if st.State != provider.StateRunning || !st.Ready {
		t.Errorf("after apply: state=%s ready=%v, want running/ready", st.State, st.Ready)
	}

	if err := p.dynamic.Resource(depGVR).Namespace("default").Delete(ctx, "dep1", metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete dep1: %v", err)
	}

	st2, err := p.ManifestStatus(ctx, instID)
	if err != nil {
		t.Fatalf("status after delete: %v", err)
	}

	if st2.State == provider.StateRunning {
		t.Errorf("after deleting one object, state should not be running, got %s", st2.State)
	}
}

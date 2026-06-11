package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strconv"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/yaml"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

// fieldManager identifies ctrlplane as the writer of applied objects.
const fieldManager = "ctrlplane"

// manifestTrackingSuffix is appended to the per-instance ConfigMap that
// records which objects a manifests source applied.
const manifestTrackingSuffix = "-manifests"

// configMapGVR is the GroupVersionResource for core ConfigMaps, used for
// the per-instance manifest-tracking object.
var configMapGVR = schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}

// objectRef locates one applied object so it can be fetched or deleted
// without re-parsing the source.
type objectRef struct {
	Group     string `json:"group"`
	Version   string `json:"version"`
	Resource  string `json:"resource"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// trackingName is the name of the per-instance manifest-tracking ConfigMap.
func trackingName(instanceID id.ID) string {
	return deploymentName(instanceID) + manifestTrackingSuffix
}

// parseManifests decodes rendered YAML documents into unstructured objects.
// Empty documents are skipped; an object missing apiVersion or kind is an
// error (it cannot be mapped to a resource).
func parseManifests(docs []string) ([]*unstructured.Unstructured, error) {
	objs := make([]*unstructured.Unstructured, 0, len(docs))

	for i, doc := range docs {
		var m map[string]any
		if err := yaml.Unmarshal([]byte(doc), &m); err != nil {
			return nil, fmt.Errorf("parse manifest %d: %w", i, err)
		}

		if len(m) == 0 {
			continue
		}

		obj := &unstructured.Unstructured{Object: m}
		if obj.GetKind() == "" || obj.GetAPIVersion() == "" {
			return nil, fmt.Errorf("manifest %d: missing apiVersion or kind", i)
		}

		objs = append(objs, obj)
	}

	return objs, nil
}

// resourceFor resolves the dynamic resource interface for an object, using
// the RESTMapper to map its GVK to a GVR and choosing the namespaced or
// cluster-scoped interface. A namespaced object with no namespace set
// inherits (and is stamped with) the provider's default namespace.
func (p *Provider) resourceFor(obj *unstructured.Unstructured) (dynamic.ResourceInterface, error) {
	gvk := obj.GroupVersionKind()

	mapping, err := p.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: rest mapping for %s: %w", gvk, err)
	}

	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		ns := obj.GetNamespace()
		if ns == "" {
			ns = p.cfg.Namespace
			obj.SetNamespace(ns)
		}

		return p.dynamic.Resource(mapping.Resource).Namespace(ns), nil
	}

	return p.dynamic.Resource(mapping.Resource), nil
}

// applyObject creates the object, or updates it in place when it already
// exists (create-or-update). True server-side apply is deferred to a later
// iteration; this form is deterministic against the fake dynamic client.
func (p *Provider) applyObject(ctx context.Context, obj *unstructured.Unstructured) error {
	ri, err := p.resourceFor(obj)
	if err != nil {
		return err
	}

	name := obj.GetName()

	existing, err := ri.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			if _, err := ri.Create(ctx, obj, metav1.CreateOptions{FieldManager: fieldManager}); err != nil {
				return fmt.Errorf("kubernetes: create %s %s: %w", obj.GetKind(), name, err)
			}

			return nil
		}

		return fmt.Errorf("kubernetes: get %s %s: %w", obj.GetKind(), name, err)
	}

	obj.SetResourceVersion(existing.GetResourceVersion())

	if _, err := ri.Update(ctx, obj, metav1.UpdateOptions{FieldManager: fieldManager}); err != nil {
		return fmt.Errorf("kubernetes: update %s %s: %w", obj.GetKind(), name, err)
	}

	return nil
}

// objectRefFor builds the tracking ref for an object, resolving its GVR and
// effective namespace via the RESTMapper.
func (p *Provider) objectRefFor(obj *unstructured.Unstructured) (objectRef, error) {
	gvk := obj.GroupVersionKind()

	mapping, err := p.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return objectRef{}, fmt.Errorf("kubernetes: rest mapping for %s: %w", gvk, err)
	}

	ns := obj.GetNamespace()
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace && ns == "" {
		ns = p.cfg.Namespace
	}

	return objectRef{
		Group:     mapping.Resource.Group,
		Version:   mapping.Resource.Version,
		Resource:  mapping.Resource.Resource,
		Namespace: ns,
		Name:      obj.GetName(),
	}, nil
}

// setLabels merges labels into an object's existing label set.
func setLabels(obj *unstructured.Unstructured, labels map[string]string) {
	existing := obj.GetLabels()
	if existing == nil {
		existing = make(map[string]string, len(labels))
	}

	maps.Copy(existing, labels)
	obj.SetLabels(existing)
}

// ApplyManifests applies every rendered document for an instance, labels
// each object, and records the applied refs in a per-instance tracking
// ConfigMap so they can later be deleted or inspected.
func (p *Provider) ApplyManifests(ctx context.Context, req provider.ManifestApplyRequest) (*provider.ProvisionResult, error) {
	objs, err := parseManifests(req.Manifests.Docs)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: %w", err)
	}

	extra := make(map[string]string, len(p.cfg.Labels)+len(req.Labels))
	maps.Copy(extra, p.cfg.Labels)
	maps.Copy(extra, req.Labels)
	labels := instanceLabels(req.InstanceID, req.TenantID, extra)

	refs := make([]objectRef, 0, len(objs))

	for _, obj := range objs {
		setLabels(obj, labels)

		if err := p.applyObject(ctx, obj); err != nil {
			return nil, err
		}

		ref, err := p.objectRefFor(obj)
		if err != nil {
			return nil, err
		}

		refs = append(refs, ref)
	}

	if err := p.writeTracking(ctx, req.InstanceID, labels, refs); err != nil {
		return nil, err
	}

	return &provider.ProvisionResult{
		ProviderRef: providerRef(p.cfg.Namespace, req.InstanceID),
		Metadata:    map[string]string{"objects": strconv.Itoa(len(refs))},
	}, nil
}

// writeTracking stores the applied object refs in a per-instance ConfigMap.
func (p *Provider) writeTracking(ctx context.Context, instanceID id.ID, labels map[string]string, refs []objectRef) error {
	data, err := json.Marshal(refs)
	if err != nil {
		return fmt.Errorf("kubernetes: marshal manifest tracking: %w", err)
	}

	cm := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      trackingName(instanceID),
			"namespace": p.cfg.Namespace,
		},
		"data": map[string]any{"refs": string(data)},
	}}
	setLabels(cm, labels)

	return p.applyObject(ctx, cm)
}

// readTracking returns the object refs recorded for an instance, or nil when
// no tracking ConfigMap exists (nothing was applied, or it was deleted).
func (p *Provider) readTracking(ctx context.Context, instanceID id.ID) ([]objectRef, error) {
	cm, err := p.dynamic.Resource(configMapGVR).Namespace(p.cfg.Namespace).
		Get(ctx, trackingName(instanceID), metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}

		return nil, fmt.Errorf("kubernetes: read manifest tracking: %w", err)
	}

	refsJSON, _, err := unstructured.NestedString(cm.Object, "data", "refs")
	if err != nil {
		return nil, fmt.Errorf("kubernetes: manifest tracking data: %w", err)
	}

	var refs []objectRef
	if refsJSON != "" {
		if err := json.Unmarshal([]byte(refsJSON), &refs); err != nil {
			return nil, fmt.Errorf("kubernetes: unmarshal manifest tracking: %w", err)
		}
	}

	return refs, nil
}

// deleteRef deletes one tracked object, treating NotFound as success.
func (p *Provider) deleteRef(ctx context.Context, ref objectRef) error {
	gvr := schema.GroupVersionResource{Group: ref.Group, Version: ref.Version, Resource: ref.Resource}

	var ri dynamic.ResourceInterface = p.dynamic.Resource(gvr)
	if ref.Namespace != "" {
		ri = p.dynamic.Resource(gvr).Namespace(ref.Namespace)
	}

	if err := ri.Delete(ctx, ref.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("kubernetes: delete %s %s: %w", ref.Resource, ref.Name, err)
	}

	return nil
}

// DeleteManifests removes every object previously applied for an instance,
// then the tracking ConfigMap itself. A missing tracking object means there
// is nothing to delete.
func (p *Provider) DeleteManifests(ctx context.Context, instanceID id.ID) error {
	refs, err := p.readTracking(ctx, instanceID)
	if err != nil {
		return err
	}

	if refs == nil {
		return nil
	}

	for _, ref := range refs {
		if err := p.deleteRef(ctx, ref); err != nil {
			return err
		}
	}

	err = p.dynamic.Resource(configMapGVR).Namespace(p.cfg.Namespace).
		Delete(ctx, trackingName(instanceID), metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("kubernetes: delete manifest tracking: %w", err)
	}

	return nil
}

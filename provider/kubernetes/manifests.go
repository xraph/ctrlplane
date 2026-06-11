package kubernetes

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/yaml"
)

// fieldManager identifies ctrlplane as the writer of applied objects.
const fieldManager = "ctrlplane"

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

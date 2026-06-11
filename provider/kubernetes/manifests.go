package kubernetes

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

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

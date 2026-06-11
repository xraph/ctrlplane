package render

import (
	"fmt"

	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/vars"
)

// renderHelm carries the chart coordinates through and deep-templates the
// values tree (string leaves only — numbers, bools, and nulls pass through
// unchanged).
func renderHelm(src *provider.HelmSource, scope vars.Scope) (*provider.RenderedHelm, error) {
	values, err := templateValue(src.Values, scope)
	if err != nil {
		return nil, fmt.Errorf("helm values: %w", err)
	}

	out := &provider.RenderedHelm{
		Repo:        src.Repo,
		Chart:       src.Chart,
		Version:     src.Version,
		ReleaseName: src.ReleaseName,
		Namespace:   src.Namespace,
	}

	if m, ok := values.(map[string]any); ok {
		out.Values = m
	}

	return out, nil
}

// templateValue walks an arbitrary values tree, templating string leaves.
// Maps and slices are rebuilt; scalar non-strings are returned unchanged.
func templateValue(v any, scope vars.Scope) (any, error) {
	switch t := v.(type) {
	case string:
		return tmplString(t, scope)
	case map[string]any:
		out := make(map[string]any, len(t))

		for k, val := range t {
			rendered, err := templateValue(val, scope)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", k, err)
			}

			out[k] = rendered
		}

		return out, nil
	case []any:
		out := make([]any, len(t))

		for i, val := range t {
			rendered, err := templateValue(val, scope)
			if err != nil {
				return nil, err
			}

			out[i] = rendered
		}

		return out, nil
	default:
		return v, nil
	}
}

package render

import (
	"fmt"
	"strings"
	"text/template"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/vars"
)

// Render resolves a DeploymentSource against a variable scope into a
// concrete RenderedSource the provider can apply. The source's Type
// selects which payload is rendered.
func Render(src provider.DeploymentSource, scope vars.Scope) (provider.RenderedSource, error) {
	switch src.Type {
	case provider.SourceServices:
		services, err := renderServices(src.Services, scope)
		if err != nil {
			return provider.RenderedSource{}, err
		}

		return provider.RenderedSource{Type: provider.SourceServices, Services: services}, nil
	case provider.SourceManifests:
		manifests, err := renderManifests(src.Manifests, scope)
		if err != nil {
			return provider.RenderedSource{}, err
		}

		return provider.RenderedSource{Type: provider.SourceManifests, Manifests: manifests}, nil
	case provider.SourceHelm:
		helm, err := renderHelm(src.Helm, scope)
		if err != nil {
			return provider.RenderedSource{}, err
		}

		return provider.RenderedSource{Type: provider.SourceHelm, Helm: helm}, nil
	default:
		return provider.RenderedSource{}, fmt.Errorf("%w: %q", ctrlplane.ErrUnsupportedSource, src.Type)
	}
}

// tmplString renders a single template string against the scope. Strings
// without a template action are returned unchanged. A missing key (e.g. an
// undefined or secret-typed variable) is an error.
func tmplString(s string, scope vars.Scope) (string, error) {
	if !strings.Contains(s, "{{") {
		return s, nil
	}

	tmpl, err := template.New("r").Option("missingkey=error").Parse(s)
	if err != nil {
		return "", fmt.Errorf("parse template %q: %w", s, err)
	}

	var sb strings.Builder
	if err := tmpl.Execute(&sb, scope.Root()); err != nil {
		return "", fmt.Errorf("render template %q: %w", s, err)
	}

	return sb.String(), nil
}

// tmplStrings renders each element of a string slice, returning a new slice.
func tmplStrings(in []string, scope vars.Scope) ([]string, error) {
	if len(in) == 0 {
		return in, nil
	}

	out := make([]string, len(in))

	for i, s := range in {
		rendered, err := tmplString(s, scope)
		if err != nil {
			return nil, err
		}

		out[i] = rendered
	}

	return out, nil
}

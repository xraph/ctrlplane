package render

import (
	"fmt"

	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/vars"
)

// renderArgo templates the string fields of an ArgoCDSource, returning a new
// source. Helm parameters and the sync policy are carried through unchanged
// — Argo CD resolves chart parameters itself.
func renderArgo(src *provider.ArgoCDSource, scope vars.Scope) (*provider.ArgoCDSource, error) {
	out := *src

	for _, f := range []*string{
		&out.Project,
		&out.RepoURL,
		&out.Path,
		&out.TargetRevision,
		&out.DestServer,
		&out.DestNamespace,
	} {
		rendered, err := tmplString(*f, scope)
		if err != nil {
			return nil, fmt.Errorf("argocd field: %w", err)
		}

		*f = rendered
	}

	return &out, nil
}

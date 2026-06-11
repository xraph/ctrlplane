package render

import (
	"fmt"
	"path"

	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/kyaml/filesys"

	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/vars"
)

// renderKustomize templates each kustomization file with the scope, writes
// them into an in-memory filesystem, and runs a kustomize build, returning
// the resulting YAML documents. Variables are substituted before the build
// so values can flow into both resources and the kustomization itself.
func renderKustomize(src *provider.KustomizeSource, scope vars.Scope) ([]string, error) {
	fSys := filesys.MakeFsInMemory()

	for filePath, content := range src.Files {
		templated, err := tmplString(content, scope)
		if err != nil {
			return nil, fmt.Errorf("kustomize file %s: %w", filePath, err)
		}

		if dir := path.Dir(filePath); dir != "." && dir != "/" {
			if err := fSys.MkdirAll(dir); err != nil {
				return nil, fmt.Errorf("kustomize mkdir %s: %w", dir, err)
			}
		}

		if err := fSys.WriteFile(filePath, []byte(templated)); err != nil {
			return nil, fmt.Errorf("kustomize write %s: %w", filePath, err)
		}
	}

	root := src.Root
	if root == "" {
		root = "/"
	}

	resMap, err := krusty.MakeKustomizer(krusty.MakeDefaultOptions()).Run(fSys, root)
	if err != nil {
		return nil, fmt.Errorf("kustomize build: %w", err)
	}

	out, err := resMap.AsYaml()
	if err != nil {
		return nil, fmt.Errorf("kustomize as yaml: %w", err)
	}

	return splitYAMLDocs(out)
}

package render

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"

	utilyaml "k8s.io/apimachinery/pkg/util/yaml"

	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/vars"
)

// renderManifests resolves a ManifestSource into concrete YAML documents.
// Inline YAML is templated then split; kustomize sources are templated then
// built in memory (see renderKustomize).
func renderManifests(src *provider.ManifestSource, scope vars.Scope) (*provider.RenderedManifests, error) {
	if src.Kustomize != nil {
		docs, err := renderKustomize(src.Kustomize, scope)
		if err != nil {
			return nil, err
		}

		return &provider.RenderedManifests{Docs: docs}, nil
	}

	rendered, err := tmplString(src.Inline, scope)
	if err != nil {
		return nil, fmt.Errorf("manifests inline: %w", err)
	}

	docs, err := splitYAMLDocs([]byte(rendered))
	if err != nil {
		return nil, err
	}

	return &provider.RenderedManifests{Docs: docs}, nil
}

// splitYAMLDocs splits multi-document YAML into individual documents,
// skipping whitespace-only documents.
func splitYAMLDocs(data []byte) ([]string, error) {
	reader := utilyaml.NewYAMLReader(bufio.NewReader(bytes.NewReader(data)))

	var docs []string

	for {
		doc, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return nil, fmt.Errorf("split yaml: %w", err)
		}

		if isEmptyYAMLDoc(doc) {
			continue
		}

		docs = append(docs, string(doc))
	}

	return docs, nil
}

// isEmptyYAMLDoc reports whether a YAML document carries no content — every
// line is blank or a bare "---" separator. The apimachinery reader includes
// the leading separator in each frame, so a separator-only frame is not
// whitespace-empty and must be detected line by line.
func isEmptyYAMLDoc(doc []byte) bool {
	for _, line := range bytes.Split(doc, []byte("\n")) {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("---")) {
			continue
		}

		return false
	}

	return true
}

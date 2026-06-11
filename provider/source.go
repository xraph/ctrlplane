package provider

import (
	"fmt"
	"strings"

	ctrlplane "github.com/xraph/ctrlplane"
)

// SourceType identifies which kind of deployment a DeploymentSource
// describes. Exactly one of the DeploymentSource payload fields is set,
// matching the Type.
type SourceType string

const (
	// SourceServices deploys container services (the ServiceSpec model).
	SourceServices SourceType = "services"

	// SourceHelm deploys a Helm chart.
	SourceHelm SourceType = "helm"

	// SourceManifests deploys raw multi-doc YAML and/or a kustomize build.
	SourceManifests SourceType = "manifests"

	// SourceArgoCD delegates deployment to Argo CD via an Application CR.
	SourceArgoCD SourceType = "argocd"
)

// DeploymentSource is the typed union describing what a workload deploys.
// Exactly one payload field is populated, matching Type.
type DeploymentSource struct {
	Type      SourceType      `json:"type"                validate:"required"`
	Services  []ServiceSpec   `json:"services,omitempty"`
	Helm      *HelmSource     `json:"helm,omitempty"`
	Manifests *ManifestSource `json:"manifests,omitempty"`
	ArgoCD    *ArgoCDSource   `json:"argocd,omitempty"`
}

// HelmSource describes a Helm chart to install. Values are the base values
// templated with variables before install; secret values are supplied at
// apply time and never persisted in a snapshot.
type HelmSource struct {
	Repo        string         `json:"repo,omitempty"`         // oci:// or https chart repo
	Chart       string         `json:"chart"                   validate:"required"`
	Version     string         `json:"version,omitempty"`
	ReleaseName string         `json:"release_name,omitempty"` // defaulted from the instance
	Namespace   string         `json:"namespace,omitempty"`
	Values      map[string]any `json:"values,omitempty"`
	ValuesFiles []string       `json:"values_files,omitempty"`
}

// ManifestSource describes raw Kubernetes manifests, either as inline
// multi-doc YAML or as a kustomize build. At least one is set.
type ManifestSource struct {
	Inline    string           `json:"inline,omitempty"`
	Kustomize *KustomizeSource `json:"kustomize,omitempty"`
}

// KustomizeSource describes an in-memory kustomization. Files maps a
// relative path to its content (one entry is the kustomization.yaml).
// Root is the build path passed to kustomize, defaulting to the fs root.
type KustomizeSource struct {
	Files map[string]string `json:"files"`
	Root  string            `json:"root,omitempty"`
}

// ArgoCDSource describes an Argo CD Application that ctrlplane manages.
type ArgoCDSource struct {
	Project        string         `json:"project,omitempty"`
	RepoURL        string         `json:"repo_url"                  validate:"required"`
	Path           string         `json:"path,omitempty"`
	TargetRevision string         `json:"target_revision,omitempty"`
	DestServer     string         `json:"dest_server,omitempty"`
	DestNamespace  string         `json:"dest_namespace,omitempty"`
	Helm           *ArgoHelm      `json:"helm,omitempty"`
	SyncPolicy     ArgoSyncPolicy `json:"sync_policy,omitempty"`
}

// ArgoHelm carries Helm-specific parameters for a Helm-typed Argo
// Application source.
type ArgoHelm struct {
	ValueFiles []string          `json:"value_files,omitempty"`
	Parameters map[string]string `json:"parameters,omitempty"`
}

// ArgoSyncPolicy configures how Argo CD reconciles the Application.
type ArgoSyncPolicy struct {
	Automated bool `json:"automated,omitempty"`
	SelfHeal  bool `json:"self_heal,omitempty"`
	Prune     bool `json:"prune,omitempty"`
}

// Validate checks that Type matches a single populated payload with its
// required fields present. It does not deeply validate the payload (e.g.
// ServiceSpec invariants) — that is the owning service's responsibility.
func (s DeploymentSource) Validate() error {
	switch s.Type {
	case SourceServices:
		if len(s.Services) == 0 {
			return fmt.Errorf("%w: services source requires services", ctrlplane.ErrInvalidSource)
		}
	case SourceHelm:
		if s.Helm == nil || strings.TrimSpace(s.Helm.Chart) == "" {
			return fmt.Errorf("%w: helm source requires a chart", ctrlplane.ErrInvalidSource)
		}
	case SourceManifests:
		if s.Manifests == nil || (strings.TrimSpace(s.Manifests.Inline) == "" && s.Manifests.Kustomize == nil) {
			return fmt.Errorf("%w: manifests source requires inline or kustomize", ctrlplane.ErrInvalidSource)
		}
	case SourceArgoCD:
		if s.ArgoCD == nil || strings.TrimSpace(s.ArgoCD.RepoURL) == "" {
			return fmt.Errorf("%w: argocd source requires repo_url", ctrlplane.ErrInvalidSource)
		}
	default:
		return fmt.Errorf("%w: unknown source type %q", ctrlplane.ErrInvalidSource, s.Type)
	}

	return nil
}

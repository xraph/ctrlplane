package provider

// RenderedSource is the fully-resolved deployment source a provider applies.
// It is produced by the render package from a DeploymentSource plus a
// resolved variable scope; the provider never sees raw templates or the
// variable layer. Secret values are never embedded here — they travel
// separately as SecretBindings and are materialized at apply time.
type RenderedSource struct {
	Type      SourceType         `json:"type"`
	Services  []ServiceSpec      `json:"services,omitempty"`
	Helm      *RenderedHelm      `json:"helm,omitempty"`
	Manifests *RenderedManifests `json:"manifests,omitempty"`
	ArgoCD    *ArgoCDSource      `json:"argocd,omitempty"` // templated in place
}

// RenderedHelm is a Helm chart reference with its values fully resolved.
type RenderedHelm struct {
	Repo        string         `json:"repo,omitempty"`
	Chart       string         `json:"chart"`
	Version     string         `json:"version,omitempty"`
	ReleaseName string         `json:"release_name,omitempty"`
	Namespace   string         `json:"namespace,omitempty"`
	Values      map[string]any `json:"values,omitempty"`
}

// RenderedManifests holds concrete Kubernetes YAML documents — the output
// of templating inline YAML or running a kustomize build. Each Docs entry
// is exactly one YAML document.
type RenderedManifests struct {
	Docs []string `json:"docs"`
}

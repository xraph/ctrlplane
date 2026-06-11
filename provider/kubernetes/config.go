package kubernetes

// Config holds configuration for the Kubernetes provider.
type Config struct {
	// Kubeconfig is the path to a kubeconfig file.
	// When empty, the provider tries in-cluster config first, then falls back
	// to the default kubeconfig loading rules (KUBECONFIG env, ~/.kube/config).
	Kubeconfig string `env:"CP_K8S_KUBECONFIG" json:"kubeconfig,omitempty"`

	// Context is the kubeconfig context to use.
	// When empty, the current context is used.
	Context string `env:"CP_K8S_CONTEXT" json:"context,omitempty"`

	// Namespace is the Kubernetes namespace for all managed resources.
	Namespace string `default:"default" env:"CP_K8S_NAMESPACE" json:"namespace"`

	// Region is the region label reported in provider info.
	Region string `default:"local" env:"CP_K8S_REGION" json:"region"`

	// Country is reported as part of the provider's Location in
	// ProviderInfo. Defaults to "Local" so dev environments without
	// explicit configuration still surface a usable region in the
	// studio catalog. Override for production clusters with the real
	// country (e.g. "US").
	Country string `default:"Local" env:"CP_K8S_COUNTRY" json:"country"`

	// City is reported as part of the provider's Location. Defaults
	// to "Localhost" for the same reason as Country. Override for
	// production with the cluster's geographic city.
	City string `default:"Localhost" env:"CP_K8S_CITY" json:"city"`

	// InCluster forces in-cluster configuration only, skipping the local
	// kubeconfig fallback. Use this in production to prevent accidentally
	// picking up a developer's local kubeconfig.
	InCluster bool `env:"CP_K8S_IN_CLUSTER" json:"in_cluster,omitempty"`

	// Labels are additional labels applied to all managed resources.
	Labels map[string]string `json:"labels,omitempty"`

	// ImagePullSecrets is the list of Secret names in the same namespace
	// that contain credentials to pull private container images.
	// Corresponds to the Kubernetes podSpec.imagePullSecrets field.
	// Use CP_K8S_IMAGE_PULL_SECRETS (comma-separated) to configure via env.
	ImagePullSecrets []string `env:"CP_K8S_IMAGE_PULL_SECRETS" json:"image_pull_secrets,omitempty"`

	// ArgoNamespace is the namespace where ctrlplane creates Argo CD
	// Application CRs (where Argo CD itself runs). Defaults to "argocd".
	ArgoNamespace string `default:"argocd" env:"CP_K8S_ARGO_NAMESPACE" json:"argo_namespace,omitempty"`
}

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

	// InCluster forces in-cluster configuration only, skipping the local
	// kubeconfig fallback. Use this in production to prevent accidentally
	// picking up a developer's local kubeconfig.
	InCluster bool `env:"CP_K8S_IN_CLUSTER" json:"in_cluster,omitempty"`

	// Labels are additional labels applied to all managed resources.
	Labels map[string]string `json:"labels,omitempty"`
}

package docker

// Config holds configuration for the Docker provider.
type Config struct {
	// Host is the Docker daemon address (e.g., unix:///var/run/docker.sock).
	Host string `env:"CP_DOCKER_HOST" json:"host"`

	// Network is the Docker network to attach containers to.
	Network string `default:"bridge" env:"CP_DOCKER_NETWORK" json:"network"`

	// Registry is the default image registry prefix.
	Registry string `env:"CP_DOCKER_REGISTRY" json:"registry,omitempty"`
}

package nomad

// Config holds configuration for the Nomad provider.
type Config struct {
	// Address is the Nomad API endpoint.
	Address string `default:"http://localhost:4646" env:"CP_NOMAD_ADDRESS" json:"address,omitempty"`

	// Token is the Nomad ACL token for authentication.
	Token string `env:"CP_NOMAD_TOKEN" json:"-"`

	// Region is the Nomad region to target.
	Region string `default:"global" env:"CP_NOMAD_REGION" json:"region"`

	// Namespace is the Nomad namespace for job submissions.
	Namespace string `default:"default" env:"CP_NOMAD_NAMESPACE" json:"namespace"`
}

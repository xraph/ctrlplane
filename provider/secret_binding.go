package provider

// SecretBinding is a resolved secret-typed variable. The provider
// materializes it as a native Secret and wires it via valueFrom/envFrom;
// the plaintext value is never carried in this struct, the render output,
// or any persisted snapshot. Lives in the provider package alongside
// SecretRef so vars and render can reference it without a provider → vars
// import edge.
type SecretBinding struct {
	// VarName is the logical variable name the binding was resolved from.
	VarName string `json:"var_name"`

	// EnvKey is the environment-variable name the workload sees. Defaults
	// to UPPER_SNAKE(VarName) when the variable does not specify one.
	EnvKey string `json:"env_key"`

	// Ref locates the secret value in the vault, resolved at apply time.
	Ref SecretRef `json:"ref"`
}

// Package secrets manages encrypted secrets for ctrlplane instances.
// It defines the Vault interface for pluggable secret storage backends
// (HashiCorp Vault, AWS Secrets Manager, etc.) and the Service/Store
// interfaces for secret lifecycle management.
package secrets

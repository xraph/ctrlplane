package secrets

import (
	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
)

// SecretType identifies the kind of secret.
type SecretType string

const (
	// SecretEnvVar is an environment variable secret.
	SecretEnvVar SecretType = "env"

	// SecretFile is a file-based secret.
	SecretFile SecretType = "file"

	// SecretRegistry holds Docker registry credentials.
	SecretRegistry SecretType = "registry"

	// SecretTLS holds TLS certificate material.
	SecretTLS SecretType = "tls"
)

// Secret represents a managed secret.
// The Value field is never serialized to JSON — only metadata is exposed.
type Secret struct {
	ctrlplane.Entity

	TenantID   string     `db:"tenant_id"   json:"tenant_id"`
	InstanceID id.ID      `db:"instance_id" json:"instance_id"`
	Key        string     `db:"key"         json:"key"`
	Type       SecretType `db:"type"        json:"type"`
	Version    int        `db:"version"     json:"version"`
	Value      []byte     `db:"value"       json:"-"`
}

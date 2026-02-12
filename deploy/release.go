package deploy

import (
	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
)

// Release is an immutable snapshot of an application version.
type Release struct {
	ctrlplane.Entity

	TenantID   string            `db:"tenant_id"   json:"tenant_id"`
	InstanceID id.ID             `db:"instance_id" json:"instance_id"`
	Version    int               `db:"version"     json:"version"`
	Image      string            `db:"image"       json:"image"`
	Env        map[string]string `db:"env"         json:"env,omitempty"`
	Notes      string            `db:"notes"       json:"notes,omitempty"`
	CommitSHA  string            `db:"commit_sha"  json:"commit_sha,omitempty"`
	Active     bool              `db:"active"      json:"active"`
}

package deploy

import (
	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

// Release is an immutable snapshot of an application version.
//
// Services holds a per-service snapshot at the moment of the deploy.
// Releases are always self-contained: a partial deploy that only
// touches service "api" still produces a Release whose other services'
// snapshots are inherited from the prior Release. Rollback always
// restores one Release to its full multi-service state.
type Release struct {
	ctrlplane.Entity

	TenantID   string                     `db:"tenant_id"   json:"tenant_id"`
	InstanceID id.ID                      `db:"instance_id" json:"instance_id"`
	Version    int                        `db:"version"     json:"version"`
	Services   []provider.ServiceSnapshot `db:"services"    json:"services"`
	Notes      string                     `db:"notes"       json:"notes,omitempty"`
	CommitSHA  string                     `db:"commit_sha"  json:"commit_sha,omitempty"`
	Active     bool                       `db:"active"      json:"active"`
}

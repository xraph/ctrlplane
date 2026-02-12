package deploy

import (
	"time"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
)

// DeployState represents the lifecycle state of a deployment.
type DeployState string

const (
	// DeployPending indicates the deployment is queued.
	DeployPending DeployState = "pending"

	// DeployRunning indicates the deployment is in progress.
	DeployRunning DeployState = "running"

	// DeploySucceeded indicates the deployment completed successfully.
	DeploySucceeded DeployState = "succeeded"

	// DeployFailed indicates the deployment failed.
	DeployFailed DeployState = "failed"

	// DeployRolledBack indicates the deployment was rolled back.
	DeployRolledBack DeployState = "rolled_back"

	// DeployCancelled indicates the deployment was cancelled.
	DeployCancelled DeployState = "cancelled"
)

// Deployment tracks a single deploy operation for an instance.
type Deployment struct {
	ctrlplane.Entity

	TenantID    string            `db:"tenant_id"    json:"tenant_id"`
	InstanceID  id.ID             `db:"instance_id"  json:"instance_id"`
	ReleaseID   id.ID             `db:"release_id"   json:"release_id"`
	State       DeployState       `db:"state"        json:"state"`
	Strategy    string            `db:"strategy"     json:"strategy"`
	Image       string            `db:"image"        json:"image"`
	Env         map[string]string `db:"env"          json:"env,omitempty"`
	ProviderRef string            `db:"provider_ref" json:"provider_ref,omitempty"`
	StartedAt   *time.Time        `db:"started_at"   json:"started_at,omitempty"`
	FinishedAt  *time.Time        `db:"finished_at"  json:"finished_at,omitempty"`
	Error       string            `db:"error"        json:"error,omitempty"`
	Initiator   string            `db:"initiator"    json:"initiator"`
}

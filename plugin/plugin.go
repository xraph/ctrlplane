// Package plugin defines the plugin system for CtrlPlane.
//
// Plugins are notified of lifecycle events (instance created, deploy
// started, health check failed, etc.) and can react to them — logging,
// metrics, tracing, auditing, etc.
//
// Each lifecycle hook is a separate interface so plugins opt in only
// to the events they care about.
package plugin

import (
	"context"

	"github.com/xraph/ctrlplane/event"
)

// ──────────────────────────────────────────────────
// Base plugin interface
// ──────────────────────────────────────────────────

// Extension is the base interface all CtrlPlane plugins must implement.
type Extension interface {
	// Name returns a unique human-readable name for the plugin.
	Name() string
}

// ──────────────────────────────────────────────────
// Instance lifecycle hooks
// ──────────────────────────────────────────────────

// InstanceCreated is called when an instance is provisioned.
type InstanceCreated interface {
	OnInstanceCreated(ctx context.Context, evt *event.Event) error
}

// InstanceStarted is called when an instance transitions to running.
type InstanceStarted interface {
	OnInstanceStarted(ctx context.Context, evt *event.Event) error
}

// InstanceStopped is called when an instance is stopped.
type InstanceStopped interface {
	OnInstanceStopped(ctx context.Context, evt *event.Event) error
}

// InstanceFailed is called when an instance enters a failed state.
type InstanceFailed interface {
	OnInstanceFailed(ctx context.Context, evt *event.Event) error
}

// InstanceDeleted is called when an instance is deprovisioned and removed.
type InstanceDeleted interface {
	OnInstanceDeleted(ctx context.Context, evt *event.Event) error
}

// InstanceScaled is called when instance resources are adjusted.
type InstanceScaled interface {
	OnInstanceScaled(ctx context.Context, evt *event.Event) error
}

// InstanceSuspended is called when an instance is suspended.
type InstanceSuspended interface {
	OnInstanceSuspended(ctx context.Context, evt *event.Event) error
}

// InstanceUnsuspended is called when a suspended instance is restored.
type InstanceUnsuspended interface {
	OnInstanceUnsuspended(ctx context.Context, evt *event.Event) error
}

// ──────────────────────────────────────────────────
// Deploy lifecycle hooks
// ──────────────────────────────────────────────────

// DeployStarted is called when a deployment begins.
type DeployStarted interface {
	OnDeployStarted(ctx context.Context, evt *event.Event) error
}

// DeploySucceeded is called when a deployment completes successfully.
type DeploySucceeded interface {
	OnDeploySucceeded(ctx context.Context, evt *event.Event) error
}

// DeployFailed is called when a deployment fails.
type DeployFailed interface {
	OnDeployFailed(ctx context.Context, evt *event.Event) error
}

// DeployRolledBack is called when a deployment is rolled back.
type DeployRolledBack interface {
	OnDeployRolledBack(ctx context.Context, evt *event.Event) error
}

// ──────────────────────────────────────────────────
// Health lifecycle hooks
// ──────────────────────────────────────────────────

// HealthCheckPassed is called when a health check passes.
type HealthCheckPassed interface {
	OnHealthCheckPassed(ctx context.Context, evt *event.Event) error
}

// HealthCheckFailed is called when a health check fails.
type HealthCheckFailed interface {
	OnHealthCheckFailed(ctx context.Context, evt *event.Event) error
}

// HealthDegraded is called when an instance enters a degraded state.
type HealthDegraded interface {
	OnHealthDegraded(ctx context.Context, evt *event.Event) error
}

// HealthRecovered is called when an instance recovers from a degraded state.
type HealthRecovered interface {
	OnHealthRecovered(ctx context.Context, evt *event.Event) error
}

// ──────────────────────────────────────────────────
// Network lifecycle hooks
// ──────────────────────────────────────────────────

// DomainAdded is called when a domain is added to an instance.
type DomainAdded interface {
	OnDomainAdded(ctx context.Context, evt *event.Event) error
}

// DomainVerified is called when a domain passes verification.
type DomainVerified interface {
	OnDomainVerified(ctx context.Context, evt *event.Event) error
}

// DomainRemoved is called when a domain is removed.
type DomainRemoved interface {
	OnDomainRemoved(ctx context.Context, evt *event.Event) error
}

// CertProvisioned is called when a TLS certificate is provisioned.
type CertProvisioned interface {
	OnCertProvisioned(ctx context.Context, evt *event.Event) error
}

// CertExpiring is called when a TLS certificate is approaching expiry.
type CertExpiring interface {
	OnCertExpiring(ctx context.Context, evt *event.Event) error
}

// ──────────────────────────────────────────────────
// Admin lifecycle hooks
// ──────────────────────────────────────────────────

// TenantCreated is called when a new tenant is created.
type TenantCreated interface {
	OnTenantCreated(ctx context.Context, evt *event.Event) error
}

// TenantSuspended is called when a tenant is suspended.
type TenantSuspended interface {
	OnTenantSuspended(ctx context.Context, evt *event.Event) error
}

// TenantDeleted is called when a tenant is deleted.
type TenantDeleted interface {
	OnTenantDeleted(ctx context.Context, evt *event.Event) error
}

// QuotaExceeded is called when a tenant exceeds their quota.
type QuotaExceeded interface {
	OnQuotaExceeded(ctx context.Context, evt *event.Event) error
}

// ──────────────────────────────────────────────────
// Shutdown hook
// ──────────────────────────────────────────────────

// Shutdown is called during graceful shutdown.
type Shutdown interface {
	OnShutdown(ctx context.Context) error
}

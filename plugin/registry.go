package plugin

import (
	"context"
	"log/slog"

	"github.com/xraph/ctrlplane/event"
)

// Named entry types pair a hook implementation with the plugin name
// captured at registration time.
type instanceCreatedEntry struct {
	name string
	hook InstanceCreated
}

type instanceStartedEntry struct {
	name string
	hook InstanceStarted
}

type instanceStoppedEntry struct {
	name string
	hook InstanceStopped
}

type instanceFailedEntry struct {
	name string
	hook InstanceFailed
}

type instanceDeletedEntry struct {
	name string
	hook InstanceDeleted
}

type instanceScaledEntry struct {
	name string
	hook InstanceScaled
}

type instanceSuspendedEntry struct {
	name string
	hook InstanceSuspended
}

type instanceUnsuspendedEntry struct {
	name string
	hook InstanceUnsuspended
}

type deployStartedEntry struct {
	name string
	hook DeployStarted
}

type deploySucceededEntry struct {
	name string
	hook DeploySucceeded
}

type deployFailedEntry struct {
	name string
	hook DeployFailed
}

type deployRolledBackEntry struct {
	name string
	hook DeployRolledBack
}

type healthCheckPassedEntry struct {
	name string
	hook HealthCheckPassed
}

type healthCheckFailedEntry struct {
	name string
	hook HealthCheckFailed
}

type healthDegradedEntry struct {
	name string
	hook HealthDegraded
}

type healthRecoveredEntry struct {
	name string
	hook HealthRecovered
}

type domainAddedEntry struct {
	name string
	hook DomainAdded
}

type domainVerifiedEntry struct {
	name string
	hook DomainVerified
}

type domainRemovedEntry struct {
	name string
	hook DomainRemoved
}

type certProvisionedEntry struct {
	name string
	hook CertProvisioned
}

type certExpiringEntry struct {
	name string
	hook CertExpiring
}

type tenantCreatedEntry struct {
	name string
	hook TenantCreated
}

type tenantSuspendedEntry struct {
	name string
	hook TenantSuspended
}

type tenantDeletedEntry struct {
	name string
	hook TenantDeleted
}

type quotaExceededEntry struct {
	name string
	hook QuotaExceeded
}

type shutdownEntry struct {
	name string
	hook Shutdown
}

// Registry holds registered plugins and dispatches lifecycle events
// to them. It type-caches plugins at registration time so emit calls
// iterate only over plugins that implement the relevant hook.
type Registry struct {
	extensions []Extension
	logger     *slog.Logger

	// Type-cached slices for each lifecycle hook.
	instanceCreated     []instanceCreatedEntry
	instanceStarted     []instanceStartedEntry
	instanceStopped     []instanceStoppedEntry
	instanceFailed      []instanceFailedEntry
	instanceDeleted     []instanceDeletedEntry
	instanceScaled      []instanceScaledEntry
	instanceSuspended   []instanceSuspendedEntry
	instanceUnsuspended []instanceUnsuspendedEntry
	deployStarted       []deployStartedEntry
	deploySucceeded     []deploySucceededEntry
	deployFailed        []deployFailedEntry
	deployRolledBack    []deployRolledBackEntry
	healthCheckPassed   []healthCheckPassedEntry
	healthCheckFailed   []healthCheckFailedEntry
	healthDegraded      []healthDegradedEntry
	healthRecovered     []healthRecoveredEntry
	domainAdded         []domainAddedEntry
	domainVerified      []domainVerifiedEntry
	domainRemoved       []domainRemovedEntry
	certProvisioned     []certProvisionedEntry
	certExpiring        []certExpiringEntry
	tenantCreated       []tenantCreatedEntry
	tenantSuspended     []tenantSuspendedEntry
	tenantDeleted       []tenantDeletedEntry
	quotaExceeded       []quotaExceededEntry
	shutdown            []shutdownEntry
}

// NewRegistry creates a plugin registry with the given logger.
func NewRegistry(logger *slog.Logger) *Registry {
	return &Registry{logger: logger}
}

// Register adds a plugin and type-asserts it into all applicable
// hook caches. Plugins are notified in registration order.
func (r *Registry) Register(e Extension) {
	r.extensions = append(r.extensions, e)
	name := e.Name()

	if h, ok := e.(InstanceCreated); ok {
		r.instanceCreated = append(r.instanceCreated, instanceCreatedEntry{name, h})
	}

	if h, ok := e.(InstanceStarted); ok {
		r.instanceStarted = append(r.instanceStarted, instanceStartedEntry{name, h})
	}

	if h, ok := e.(InstanceStopped); ok {
		r.instanceStopped = append(r.instanceStopped, instanceStoppedEntry{name, h})
	}

	if h, ok := e.(InstanceFailed); ok {
		r.instanceFailed = append(r.instanceFailed, instanceFailedEntry{name, h})
	}

	if h, ok := e.(InstanceDeleted); ok {
		r.instanceDeleted = append(r.instanceDeleted, instanceDeletedEntry{name, h})
	}

	if h, ok := e.(InstanceScaled); ok {
		r.instanceScaled = append(r.instanceScaled, instanceScaledEntry{name, h})
	}

	if h, ok := e.(InstanceSuspended); ok {
		r.instanceSuspended = append(r.instanceSuspended, instanceSuspendedEntry{name, h})
	}

	if h, ok := e.(InstanceUnsuspended); ok {
		r.instanceUnsuspended = append(r.instanceUnsuspended, instanceUnsuspendedEntry{name, h})
	}

	if h, ok := e.(DeployStarted); ok {
		r.deployStarted = append(r.deployStarted, deployStartedEntry{name, h})
	}

	if h, ok := e.(DeploySucceeded); ok {
		r.deploySucceeded = append(r.deploySucceeded, deploySucceededEntry{name, h})
	}

	if h, ok := e.(DeployFailed); ok {
		r.deployFailed = append(r.deployFailed, deployFailedEntry{name, h})
	}

	if h, ok := e.(DeployRolledBack); ok {
		r.deployRolledBack = append(r.deployRolledBack, deployRolledBackEntry{name, h})
	}

	if h, ok := e.(HealthCheckPassed); ok {
		r.healthCheckPassed = append(r.healthCheckPassed, healthCheckPassedEntry{name, h})
	}

	if h, ok := e.(HealthCheckFailed); ok {
		r.healthCheckFailed = append(r.healthCheckFailed, healthCheckFailedEntry{name, h})
	}

	if h, ok := e.(HealthDegraded); ok {
		r.healthDegraded = append(r.healthDegraded, healthDegradedEntry{name, h})
	}

	if h, ok := e.(HealthRecovered); ok {
		r.healthRecovered = append(r.healthRecovered, healthRecoveredEntry{name, h})
	}

	if h, ok := e.(DomainAdded); ok {
		r.domainAdded = append(r.domainAdded, domainAddedEntry{name, h})
	}

	if h, ok := e.(DomainVerified); ok {
		r.domainVerified = append(r.domainVerified, domainVerifiedEntry{name, h})
	}

	if h, ok := e.(DomainRemoved); ok {
		r.domainRemoved = append(r.domainRemoved, domainRemovedEntry{name, h})
	}

	if h, ok := e.(CertProvisioned); ok {
		r.certProvisioned = append(r.certProvisioned, certProvisionedEntry{name, h})
	}

	if h, ok := e.(CertExpiring); ok {
		r.certExpiring = append(r.certExpiring, certExpiringEntry{name, h})
	}

	if h, ok := e.(TenantCreated); ok {
		r.tenantCreated = append(r.tenantCreated, tenantCreatedEntry{name, h})
	}

	if h, ok := e.(TenantSuspended); ok {
		r.tenantSuspended = append(r.tenantSuspended, tenantSuspendedEntry{name, h})
	}

	if h, ok := e.(TenantDeleted); ok {
		r.tenantDeleted = append(r.tenantDeleted, tenantDeletedEntry{name, h})
	}

	if h, ok := e.(QuotaExceeded); ok {
		r.quotaExceeded = append(r.quotaExceeded, quotaExceededEntry{name, h})
	}

	if h, ok := e.(Shutdown); ok {
		r.shutdown = append(r.shutdown, shutdownEntry{name, h})
	}
}

// Extensions returns all registered plugins.
func (r *Registry) Extensions() []Extension { return r.extensions }

// ──────────────────────────────────────────────────
// Instance lifecycle emitters
// ──────────────────────────────────────────────────

// EmitInstanceCreated notifies all plugins that implement InstanceCreated.
func (r *Registry) EmitInstanceCreated(ctx context.Context, evt *event.Event) {
	for _, e := range r.instanceCreated {
		if err := e.hook.OnInstanceCreated(ctx, evt); err != nil {
			r.logHookError("OnInstanceCreated", e.name, err)
		}
	}
}

// EmitInstanceStarted notifies all plugins that implement InstanceStarted.
func (r *Registry) EmitInstanceStarted(ctx context.Context, evt *event.Event) {
	for _, e := range r.instanceStarted {
		if err := e.hook.OnInstanceStarted(ctx, evt); err != nil {
			r.logHookError("OnInstanceStarted", e.name, err)
		}
	}
}

// EmitInstanceStopped notifies all plugins that implement InstanceStopped.
func (r *Registry) EmitInstanceStopped(ctx context.Context, evt *event.Event) {
	for _, e := range r.instanceStopped {
		if err := e.hook.OnInstanceStopped(ctx, evt); err != nil {
			r.logHookError("OnInstanceStopped", e.name, err)
		}
	}
}

// EmitInstanceFailed notifies all plugins that implement InstanceFailed.
func (r *Registry) EmitInstanceFailed(ctx context.Context, evt *event.Event) {
	for _, e := range r.instanceFailed {
		if err := e.hook.OnInstanceFailed(ctx, evt); err != nil {
			r.logHookError("OnInstanceFailed", e.name, err)
		}
	}
}

// EmitInstanceDeleted notifies all plugins that implement InstanceDeleted.
func (r *Registry) EmitInstanceDeleted(ctx context.Context, evt *event.Event) {
	for _, e := range r.instanceDeleted {
		if err := e.hook.OnInstanceDeleted(ctx, evt); err != nil {
			r.logHookError("OnInstanceDeleted", e.name, err)
		}
	}
}

// EmitInstanceScaled notifies all plugins that implement InstanceScaled.
func (r *Registry) EmitInstanceScaled(ctx context.Context, evt *event.Event) {
	for _, e := range r.instanceScaled {
		if err := e.hook.OnInstanceScaled(ctx, evt); err != nil {
			r.logHookError("OnInstanceScaled", e.name, err)
		}
	}
}

// EmitInstanceSuspended notifies all plugins that implement InstanceSuspended.
func (r *Registry) EmitInstanceSuspended(ctx context.Context, evt *event.Event) {
	for _, e := range r.instanceSuspended {
		if err := e.hook.OnInstanceSuspended(ctx, evt); err != nil {
			r.logHookError("OnInstanceSuspended", e.name, err)
		}
	}
}

// EmitInstanceUnsuspended notifies all plugins that implement InstanceUnsuspended.
func (r *Registry) EmitInstanceUnsuspended(ctx context.Context, evt *event.Event) {
	for _, e := range r.instanceUnsuspended {
		if err := e.hook.OnInstanceUnsuspended(ctx, evt); err != nil {
			r.logHookError("OnInstanceUnsuspended", e.name, err)
		}
	}
}

// ──────────────────────────────────────────────────
// Deploy lifecycle emitters
// ──────────────────────────────────────────────────

// EmitDeployStarted notifies all plugins that implement DeployStarted.
func (r *Registry) EmitDeployStarted(ctx context.Context, evt *event.Event) {
	for _, e := range r.deployStarted {
		if err := e.hook.OnDeployStarted(ctx, evt); err != nil {
			r.logHookError("OnDeployStarted", e.name, err)
		}
	}
}

// EmitDeploySucceeded notifies all plugins that implement DeploySucceeded.
func (r *Registry) EmitDeploySucceeded(ctx context.Context, evt *event.Event) {
	for _, e := range r.deploySucceeded {
		if err := e.hook.OnDeploySucceeded(ctx, evt); err != nil {
			r.logHookError("OnDeploySucceeded", e.name, err)
		}
	}
}

// EmitDeployFailed notifies all plugins that implement DeployFailed.
func (r *Registry) EmitDeployFailed(ctx context.Context, evt *event.Event) {
	for _, e := range r.deployFailed {
		if err := e.hook.OnDeployFailed(ctx, evt); err != nil {
			r.logHookError("OnDeployFailed", e.name, err)
		}
	}
}

// EmitDeployRolledBack notifies all plugins that implement DeployRolledBack.
func (r *Registry) EmitDeployRolledBack(ctx context.Context, evt *event.Event) {
	for _, e := range r.deployRolledBack {
		if err := e.hook.OnDeployRolledBack(ctx, evt); err != nil {
			r.logHookError("OnDeployRolledBack", e.name, err)
		}
	}
}

// ──────────────────────────────────────────────────
// Health lifecycle emitters
// ──────────────────────────────────────────────────

// EmitHealthCheckPassed notifies all plugins that implement HealthCheckPassed.
func (r *Registry) EmitHealthCheckPassed(ctx context.Context, evt *event.Event) {
	for _, e := range r.healthCheckPassed {
		if err := e.hook.OnHealthCheckPassed(ctx, evt); err != nil {
			r.logHookError("OnHealthCheckPassed", e.name, err)
		}
	}
}

// EmitHealthCheckFailed notifies all plugins that implement HealthCheckFailed.
func (r *Registry) EmitHealthCheckFailed(ctx context.Context, evt *event.Event) {
	for _, e := range r.healthCheckFailed {
		if err := e.hook.OnHealthCheckFailed(ctx, evt); err != nil {
			r.logHookError("OnHealthCheckFailed", e.name, err)
		}
	}
}

// EmitHealthDegraded notifies all plugins that implement HealthDegraded.
func (r *Registry) EmitHealthDegraded(ctx context.Context, evt *event.Event) {
	for _, e := range r.healthDegraded {
		if err := e.hook.OnHealthDegraded(ctx, evt); err != nil {
			r.logHookError("OnHealthDegraded", e.name, err)
		}
	}
}

// EmitHealthRecovered notifies all plugins that implement HealthRecovered.
func (r *Registry) EmitHealthRecovered(ctx context.Context, evt *event.Event) {
	for _, e := range r.healthRecovered {
		if err := e.hook.OnHealthRecovered(ctx, evt); err != nil {
			r.logHookError("OnHealthRecovered", e.name, err)
		}
	}
}

// ──────────────────────────────────────────────────
// Network lifecycle emitters
// ──────────────────────────────────────────────────

// EmitDomainAdded notifies all plugins that implement DomainAdded.
func (r *Registry) EmitDomainAdded(ctx context.Context, evt *event.Event) {
	for _, e := range r.domainAdded {
		if err := e.hook.OnDomainAdded(ctx, evt); err != nil {
			r.logHookError("OnDomainAdded", e.name, err)
		}
	}
}

// EmitDomainVerified notifies all plugins that implement DomainVerified.
func (r *Registry) EmitDomainVerified(ctx context.Context, evt *event.Event) {
	for _, e := range r.domainVerified {
		if err := e.hook.OnDomainVerified(ctx, evt); err != nil {
			r.logHookError("OnDomainVerified", e.name, err)
		}
	}
}

// EmitDomainRemoved notifies all plugins that implement DomainRemoved.
func (r *Registry) EmitDomainRemoved(ctx context.Context, evt *event.Event) {
	for _, e := range r.domainRemoved {
		if err := e.hook.OnDomainRemoved(ctx, evt); err != nil {
			r.logHookError("OnDomainRemoved", e.name, err)
		}
	}
}

// EmitCertProvisioned notifies all plugins that implement CertProvisioned.
func (r *Registry) EmitCertProvisioned(ctx context.Context, evt *event.Event) {
	for _, e := range r.certProvisioned {
		if err := e.hook.OnCertProvisioned(ctx, evt); err != nil {
			r.logHookError("OnCertProvisioned", e.name, err)
		}
	}
}

// EmitCertExpiring notifies all plugins that implement CertExpiring.
func (r *Registry) EmitCertExpiring(ctx context.Context, evt *event.Event) {
	for _, e := range r.certExpiring {
		if err := e.hook.OnCertExpiring(ctx, evt); err != nil {
			r.logHookError("OnCertExpiring", e.name, err)
		}
	}
}

// ──────────────────────────────────────────────────
// Admin lifecycle emitters
// ──────────────────────────────────────────────────

// EmitTenantCreated notifies all plugins that implement TenantCreated.
func (r *Registry) EmitTenantCreated(ctx context.Context, evt *event.Event) {
	for _, e := range r.tenantCreated {
		if err := e.hook.OnTenantCreated(ctx, evt); err != nil {
			r.logHookError("OnTenantCreated", e.name, err)
		}
	}
}

// EmitTenantSuspended notifies all plugins that implement TenantSuspended.
func (r *Registry) EmitTenantSuspended(ctx context.Context, evt *event.Event) {
	for _, e := range r.tenantSuspended {
		if err := e.hook.OnTenantSuspended(ctx, evt); err != nil {
			r.logHookError("OnTenantSuspended", e.name, err)
		}
	}
}

// EmitTenantDeleted notifies all plugins that implement TenantDeleted.
func (r *Registry) EmitTenantDeleted(ctx context.Context, evt *event.Event) {
	for _, e := range r.tenantDeleted {
		if err := e.hook.OnTenantDeleted(ctx, evt); err != nil {
			r.logHookError("OnTenantDeleted", e.name, err)
		}
	}
}

// EmitQuotaExceeded notifies all plugins that implement QuotaExceeded.
func (r *Registry) EmitQuotaExceeded(ctx context.Context, evt *event.Event) {
	for _, e := range r.quotaExceeded {
		if err := e.hook.OnQuotaExceeded(ctx, evt); err != nil {
			r.logHookError("OnQuotaExceeded", e.name, err)
		}
	}
}

// ──────────────────────────────────────────────────
// Shutdown emitter
// ──────────────────────────────────────────────────

// EmitShutdown notifies all plugins that implement Shutdown.
func (r *Registry) EmitShutdown(ctx context.Context) {
	for _, e := range r.shutdown {
		if err := e.hook.OnShutdown(ctx); err != nil {
			r.logHookError("OnShutdown", e.name, err)
		}
	}
}

// ──────────────────────────────────────────────────
// Event bus bridge
// ──────────────────────────────────────────────────

// HandleEvent is the event bus handler. It dispatches an event.Event
// to the appropriate plugin hook(s) based on the event type.
// This method is subscribed to the event bus to bridge events to plugins.
func (r *Registry) HandleEvent(ctx context.Context, evt *event.Event) error {
	switch evt.Type {
	case event.InstanceCreated:
		r.EmitInstanceCreated(ctx, evt)
	case event.InstanceStarted:
		r.EmitInstanceStarted(ctx, evt)
	case event.InstanceStopped:
		r.EmitInstanceStopped(ctx, evt)
	case event.InstanceFailed:
		r.EmitInstanceFailed(ctx, evt)
	case event.InstanceDeleted:
		r.EmitInstanceDeleted(ctx, evt)
	case event.InstanceScaled:
		r.EmitInstanceScaled(ctx, evt)
	case event.InstanceSuspended:
		r.EmitInstanceSuspended(ctx, evt)
	case event.InstanceUnsuspended:
		r.EmitInstanceUnsuspended(ctx, evt)
	case event.DeployStarted:
		r.EmitDeployStarted(ctx, evt)
	case event.DeploySucceeded:
		r.EmitDeploySucceeded(ctx, evt)
	case event.DeployFailed:
		r.EmitDeployFailed(ctx, evt)
	case event.DeployRolledBack:
		r.EmitDeployRolledBack(ctx, evt)
	case event.HealthCheckPassed:
		r.EmitHealthCheckPassed(ctx, evt)
	case event.HealthCheckFailed:
		r.EmitHealthCheckFailed(ctx, evt)
	case event.HealthDegraded:
		r.EmitHealthDegraded(ctx, evt)
	case event.HealthRecovered:
		r.EmitHealthRecovered(ctx, evt)
	case event.DomainAdded:
		r.EmitDomainAdded(ctx, evt)
	case event.DomainVerified:
		r.EmitDomainVerified(ctx, evt)
	case event.DomainRemoved:
		r.EmitDomainRemoved(ctx, evt)
	case event.CertProvisioned:
		r.EmitCertProvisioned(ctx, evt)
	case event.CertExpiring:
		r.EmitCertExpiring(ctx, evt)
	case event.TenantCreated:
		r.EmitTenantCreated(ctx, evt)
	case event.TenantSuspended:
		r.EmitTenantSuspended(ctx, evt)
	case event.TenantDeleted:
		r.EmitTenantDeleted(ctx, evt)
	case event.QuotaExceeded:
		r.EmitQuotaExceeded(ctx, evt)
	}

	// Always return nil — plugin errors are logged but never propagated.
	return nil
}

// logHookError logs a warning when a lifecycle hook returns an error.
// Errors from hooks are never propagated — they must not block the event pipeline.
func (r *Registry) logHookError(hook, extName string, err error) {
	r.logger.Warn("plugin hook error",
		slog.String("hook", hook),
		slog.String("plugin", extName),
		slog.String("error", err.Error()),
	)
}

// Package observability provides the lifecycle event metrics plugin for
// Ctrl Plane.
//
// MetricsExtension counts every lifecycle event the plugin registry
// delivers in one labelled counter, ctrlplane_lifecycle_events_total, whose
// event label is the event type (instance.created, deploy.failed, and so
// on). A Forge app scrapes it as forge_ctrlplane_lifecycle_events_total.
//
// You rarely build it yourself: app.WithMetrics, which the ctrlplane Forge
// extension sets from app.Metrics(), registers one for you.
package observability

import (
	"context"

	gu "github.com/xraph/go-utils/metrics"

	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/internal/obs"
	"github.com/xraph/ctrlplane/plugin"
)

// Compile-time interface checks.
var (
	_ plugin.Extension           = (*MetricsExtension)(nil)
	_ plugin.InstanceCreated     = (*MetricsExtension)(nil)
	_ plugin.InstanceStarted     = (*MetricsExtension)(nil)
	_ plugin.InstanceStopped     = (*MetricsExtension)(nil)
	_ plugin.InstanceFailed      = (*MetricsExtension)(nil)
	_ plugin.InstanceDeleted     = (*MetricsExtension)(nil)
	_ plugin.InstanceScaled      = (*MetricsExtension)(nil)
	_ plugin.InstanceSuspended   = (*MetricsExtension)(nil)
	_ plugin.InstanceUnsuspended = (*MetricsExtension)(nil)
	_ plugin.WorkloadCreated     = (*MetricsExtension)(nil)
	_ plugin.WorkloadUpdated     = (*MetricsExtension)(nil)
	_ plugin.WorkloadScaled      = (*MetricsExtension)(nil)
	_ plugin.WorkloadDeployed    = (*MetricsExtension)(nil)
	_ plugin.WorkloadPaused      = (*MetricsExtension)(nil)
	_ plugin.WorkloadResumed     = (*MetricsExtension)(nil)
	_ plugin.WorkloadRestarted   = (*MetricsExtension)(nil)
	_ plugin.WorkloadDeleted     = (*MetricsExtension)(nil)
	_ plugin.WorkloadFailed      = (*MetricsExtension)(nil)
	_ plugin.TemplateCreated     = (*MetricsExtension)(nil)
	_ plugin.TemplateUpdated     = (*MetricsExtension)(nil)
	_ plugin.TemplateDeleted     = (*MetricsExtension)(nil)
	_ plugin.DeployStarted       = (*MetricsExtension)(nil)
	_ plugin.DeploySucceeded     = (*MetricsExtension)(nil)
	_ plugin.DeployFailed        = (*MetricsExtension)(nil)
	_ plugin.DeployRolledBack    = (*MetricsExtension)(nil)
	_ plugin.HealthCheckPassed   = (*MetricsExtension)(nil)
	_ plugin.HealthCheckFailed   = (*MetricsExtension)(nil)
	_ plugin.HealthDegraded      = (*MetricsExtension)(nil)
	_ plugin.HealthRecovered     = (*MetricsExtension)(nil)
	_ plugin.DomainAdded         = (*MetricsExtension)(nil)
	_ plugin.DomainVerified      = (*MetricsExtension)(nil)
	_ plugin.DomainRemoved       = (*MetricsExtension)(nil)
	_ plugin.CertProvisioned     = (*MetricsExtension)(nil)
	_ plugin.CertExpiring        = (*MetricsExtension)(nil)
	_ plugin.TenantCreated       = (*MetricsExtension)(nil)
	_ plugin.TenantSuspended     = (*MetricsExtension)(nil)
	_ plugin.TenantDeleted       = (*MetricsExtension)(nil)
	_ plugin.QuotaExceeded       = (*MetricsExtension)(nil)
)

// MetricsExtension counts lifecycle events by type.
type MetricsExtension struct {
	metrics *obs.Metrics
}

// NewMetricsExtension returns a MetricsExtension that records on m. In a
// Forge app pass app.Metrics(). A nil m records nothing.
func NewMetricsExtension(m gu.MetricFactory) *MetricsExtension {
	return &MetricsExtension{metrics: obs.NewMetrics(m)}
}

// Name implements plugin.Extension.
func (m *MetricsExtension) Name() string { return "observability-metrics" }

// count records one event. The label is the constant for the hook that
// fired, never the event's own Type field, so the label set stays the
// fixed list of hooked event types.
func (m *MetricsExtension) count(t event.Type) error {
	m.metrics.LifecycleEvent(string(t))

	return nil
}

// ──────────────────────────────────────────────────
// Instance hooks
// ──────────────────────────────────────────────────

// OnInstanceCreated counts an event.InstanceCreated event.
func (m *MetricsExtension) OnInstanceCreated(_ context.Context, _ *event.Event) error {
	return m.count(event.InstanceCreated)
}

// OnInstanceStarted counts an event.InstanceStarted event.
func (m *MetricsExtension) OnInstanceStarted(_ context.Context, _ *event.Event) error {
	return m.count(event.InstanceStarted)
}

// OnInstanceStopped counts an event.InstanceStopped event.
func (m *MetricsExtension) OnInstanceStopped(_ context.Context, _ *event.Event) error {
	return m.count(event.InstanceStopped)
}

// OnInstanceFailed counts an event.InstanceFailed event.
func (m *MetricsExtension) OnInstanceFailed(_ context.Context, _ *event.Event) error {
	return m.count(event.InstanceFailed)
}

// OnInstanceDeleted counts an event.InstanceDeleted event.
func (m *MetricsExtension) OnInstanceDeleted(_ context.Context, _ *event.Event) error {
	return m.count(event.InstanceDeleted)
}

// OnInstanceScaled counts an event.InstanceScaled event.
func (m *MetricsExtension) OnInstanceScaled(_ context.Context, _ *event.Event) error {
	return m.count(event.InstanceScaled)
}

// OnInstanceSuspended counts an event.InstanceSuspended event.
func (m *MetricsExtension) OnInstanceSuspended(_ context.Context, _ *event.Event) error {
	return m.count(event.InstanceSuspended)
}

// OnInstanceUnsuspended counts an event.InstanceUnsuspended event.
func (m *MetricsExtension) OnInstanceUnsuspended(_ context.Context, _ *event.Event) error {
	return m.count(event.InstanceUnsuspended)
}

// ──────────────────────────────────────────────────
// Workload hooks
// ──────────────────────────────────────────────────

// OnWorkloadCreated counts an event.WorkloadCreated event.
func (m *MetricsExtension) OnWorkloadCreated(_ context.Context, _ *event.Event) error {
	return m.count(event.WorkloadCreated)
}

// OnWorkloadUpdated counts an event.WorkloadUpdated event.
func (m *MetricsExtension) OnWorkloadUpdated(_ context.Context, _ *event.Event) error {
	return m.count(event.WorkloadUpdated)
}

// OnWorkloadScaled counts an event.WorkloadScaled event.
func (m *MetricsExtension) OnWorkloadScaled(_ context.Context, _ *event.Event) error {
	return m.count(event.WorkloadScaled)
}

// OnWorkloadDeployed counts an event.WorkloadDeployed event.
func (m *MetricsExtension) OnWorkloadDeployed(_ context.Context, _ *event.Event) error {
	return m.count(event.WorkloadDeployed)
}

// OnWorkloadPaused counts an event.WorkloadPaused event.
func (m *MetricsExtension) OnWorkloadPaused(_ context.Context, _ *event.Event) error {
	return m.count(event.WorkloadPaused)
}

// OnWorkloadResumed counts an event.WorkloadResumed event.
func (m *MetricsExtension) OnWorkloadResumed(_ context.Context, _ *event.Event) error {
	return m.count(event.WorkloadResumed)
}

// OnWorkloadRestarted counts an event.WorkloadRestarted event.
func (m *MetricsExtension) OnWorkloadRestarted(_ context.Context, _ *event.Event) error {
	return m.count(event.WorkloadRestarted)
}

// OnWorkloadDeleted counts an event.WorkloadDeleted event.
func (m *MetricsExtension) OnWorkloadDeleted(_ context.Context, _ *event.Event) error {
	return m.count(event.WorkloadDeleted)
}

// OnWorkloadFailed counts an event.WorkloadFailed event.
func (m *MetricsExtension) OnWorkloadFailed(_ context.Context, _ *event.Event) error {
	return m.count(event.WorkloadFailed)
}

// ──────────────────────────────────────────────────
// Template hooks
// ──────────────────────────────────────────────────

// OnTemplateCreated counts an event.TemplateCreated event.
func (m *MetricsExtension) OnTemplateCreated(_ context.Context, _ *event.Event) error {
	return m.count(event.TemplateCreated)
}

// OnTemplateUpdated counts an event.TemplateUpdated event.
func (m *MetricsExtension) OnTemplateUpdated(_ context.Context, _ *event.Event) error {
	return m.count(event.TemplateUpdated)
}

// OnTemplateDeleted counts an event.TemplateDeleted event.
func (m *MetricsExtension) OnTemplateDeleted(_ context.Context, _ *event.Event) error {
	return m.count(event.TemplateDeleted)
}

// ──────────────────────────────────────────────────
// Deploy hooks
// ──────────────────────────────────────────────────

// OnDeployStarted counts an event.DeployStarted event.
func (m *MetricsExtension) OnDeployStarted(_ context.Context, _ *event.Event) error {
	return m.count(event.DeployStarted)
}

// OnDeploySucceeded counts an event.DeploySucceeded event.
func (m *MetricsExtension) OnDeploySucceeded(_ context.Context, _ *event.Event) error {
	return m.count(event.DeploySucceeded)
}

// OnDeployFailed counts an event.DeployFailed event.
func (m *MetricsExtension) OnDeployFailed(_ context.Context, _ *event.Event) error {
	return m.count(event.DeployFailed)
}

// OnDeployRolledBack counts an event.DeployRolledBack event.
func (m *MetricsExtension) OnDeployRolledBack(_ context.Context, _ *event.Event) error {
	return m.count(event.DeployRolledBack)
}

// ──────────────────────────────────────────────────
// Health hooks
// ──────────────────────────────────────────────────

// OnHealthCheckPassed counts an event.HealthCheckPassed event.
func (m *MetricsExtension) OnHealthCheckPassed(_ context.Context, _ *event.Event) error {
	return m.count(event.HealthCheckPassed)
}

// OnHealthCheckFailed counts an event.HealthCheckFailed event.
func (m *MetricsExtension) OnHealthCheckFailed(_ context.Context, _ *event.Event) error {
	return m.count(event.HealthCheckFailed)
}

// OnHealthDegraded counts an event.HealthDegraded event.
func (m *MetricsExtension) OnHealthDegraded(_ context.Context, _ *event.Event) error {
	return m.count(event.HealthDegraded)
}

// OnHealthRecovered counts an event.HealthRecovered event.
func (m *MetricsExtension) OnHealthRecovered(_ context.Context, _ *event.Event) error {
	return m.count(event.HealthRecovered)
}

// ──────────────────────────────────────────────────
// Network hooks
// ──────────────────────────────────────────────────

// OnDomainAdded counts an event.DomainAdded event.
func (m *MetricsExtension) OnDomainAdded(_ context.Context, _ *event.Event) error {
	return m.count(event.DomainAdded)
}

// OnDomainVerified counts an event.DomainVerified event.
func (m *MetricsExtension) OnDomainVerified(_ context.Context, _ *event.Event) error {
	return m.count(event.DomainVerified)
}

// OnDomainRemoved counts an event.DomainRemoved event.
func (m *MetricsExtension) OnDomainRemoved(_ context.Context, _ *event.Event) error {
	return m.count(event.DomainRemoved)
}

// OnCertProvisioned counts an event.CertProvisioned event.
func (m *MetricsExtension) OnCertProvisioned(_ context.Context, _ *event.Event) error {
	return m.count(event.CertProvisioned)
}

// OnCertExpiring counts an event.CertExpiring event.
func (m *MetricsExtension) OnCertExpiring(_ context.Context, _ *event.Event) error {
	return m.count(event.CertExpiring)
}

// ──────────────────────────────────────────────────
// Admin hooks
// ──────────────────────────────────────────────────

// OnTenantCreated counts an event.TenantCreated event.
func (m *MetricsExtension) OnTenantCreated(_ context.Context, _ *event.Event) error {
	return m.count(event.TenantCreated)
}

// OnTenantSuspended counts an event.TenantSuspended event.
func (m *MetricsExtension) OnTenantSuspended(_ context.Context, _ *event.Event) error {
	return m.count(event.TenantSuspended)
}

// OnTenantDeleted counts an event.TenantDeleted event.
func (m *MetricsExtension) OnTenantDeleted(_ context.Context, _ *event.Event) error {
	return m.count(event.TenantDeleted)
}

// OnQuotaExceeded counts an event.QuotaExceeded event.
func (m *MetricsExtension) OnQuotaExceeded(_ context.Context, _ *event.Event) error {
	return m.count(event.QuotaExceeded)
}

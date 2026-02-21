// Package observability provides a metrics extension for CtrlPlane that records
// lifecycle event counts via go-utils MetricFactory.
package observability

import (
	"context"

	gu "github.com/xraph/go-utils/metrics"

	"github.com/xraph/ctrlplane/event"
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

// MetricsExtension records lifecycle metrics via go-utils MetricFactory.
type MetricsExtension struct {
	// Instance counters.
	InstanceCreatedCount     gu.Counter
	InstanceStartedCount     gu.Counter
	InstanceStoppedCount     gu.Counter
	InstanceFailedCount      gu.Counter
	InstanceDeletedCount     gu.Counter
	InstanceScaledCount      gu.Counter
	InstanceSuspendedCount   gu.Counter
	InstanceUnsuspendedCount gu.Counter

	// Deploy counters.
	DeployStartedCount    gu.Counter
	DeploySucceededCount  gu.Counter
	DeployFailedCount     gu.Counter
	DeployRolledBackCount gu.Counter

	// Health counters.
	HealthCheckPassedCount gu.Counter
	HealthCheckFailedCount gu.Counter
	HealthDegradedCount    gu.Counter
	HealthRecoveredCount   gu.Counter

	// Network counters.
	DomainAddedCount     gu.Counter
	DomainVerifiedCount  gu.Counter
	DomainRemovedCount   gu.Counter
	CertProvisionedCount gu.Counter
	CertExpiringCount    gu.Counter

	// Admin counters.
	TenantCreatedCount   gu.Counter
	TenantSuspendedCount gu.Counter
	TenantDeletedCount   gu.Counter
	QuotaExceededCount   gu.Counter
}

// NewMetricsExtension creates a MetricsExtension with a default metrics collector.
func NewMetricsExtension() *MetricsExtension {
	return NewMetricsExtensionWithFactory(gu.NewMetricsCollector("ctrlplane/observability"))
}

// NewMetricsExtensionWithFactory creates a MetricsExtension with the provided MetricFactory.
func NewMetricsExtensionWithFactory(factory gu.MetricFactory) *MetricsExtension {
	return &MetricsExtension{
		InstanceCreatedCount:     factory.Counter("ctrlplane.instance.created"),
		InstanceStartedCount:     factory.Counter("ctrlplane.instance.started"),
		InstanceStoppedCount:     factory.Counter("ctrlplane.instance.stopped"),
		InstanceFailedCount:      factory.Counter("ctrlplane.instance.failed"),
		InstanceDeletedCount:     factory.Counter("ctrlplane.instance.deleted"),
		InstanceScaledCount:      factory.Counter("ctrlplane.instance.scaled"),
		InstanceSuspendedCount:   factory.Counter("ctrlplane.instance.suspended"),
		InstanceUnsuspendedCount: factory.Counter("ctrlplane.instance.unsuspended"),

		DeployStartedCount:    factory.Counter("ctrlplane.deploy.started"),
		DeploySucceededCount:  factory.Counter("ctrlplane.deploy.succeeded"),
		DeployFailedCount:     factory.Counter("ctrlplane.deploy.failed"),
		DeployRolledBackCount: factory.Counter("ctrlplane.deploy.rolled_back"),

		HealthCheckPassedCount: factory.Counter("ctrlplane.health.passed"),
		HealthCheckFailedCount: factory.Counter("ctrlplane.health.failed"),
		HealthDegradedCount:    factory.Counter("ctrlplane.health.degraded"),
		HealthRecoveredCount:   factory.Counter("ctrlplane.health.recovered"),

		DomainAddedCount:     factory.Counter("ctrlplane.domain.added"),
		DomainVerifiedCount:  factory.Counter("ctrlplane.domain.verified"),
		DomainRemovedCount:   factory.Counter("ctrlplane.domain.removed"),
		CertProvisionedCount: factory.Counter("ctrlplane.cert.provisioned"),
		CertExpiringCount:    factory.Counter("ctrlplane.cert.expiring"),

		TenantCreatedCount:   factory.Counter("ctrlplane.tenant.created"),
		TenantSuspendedCount: factory.Counter("ctrlplane.tenant.suspended"),
		TenantDeletedCount:   factory.Counter("ctrlplane.tenant.deleted"),
		QuotaExceededCount:   factory.Counter("ctrlplane.quota.exceeded"),
	}
}

// Name implements plugin.Extension.
func (m *MetricsExtension) Name() string { return "observability-metrics" }

// ──────────────────────────────────────────────────
// Instance hooks
// ──────────────────────────────────────────────────

func (m *MetricsExtension) OnInstanceCreated(_ context.Context, _ *event.Event) error {
	m.InstanceCreatedCount.Inc()

	return nil
}

func (m *MetricsExtension) OnInstanceStarted(_ context.Context, _ *event.Event) error {
	m.InstanceStartedCount.Inc()

	return nil
}

func (m *MetricsExtension) OnInstanceStopped(_ context.Context, _ *event.Event) error {
	m.InstanceStoppedCount.Inc()

	return nil
}

func (m *MetricsExtension) OnInstanceFailed(_ context.Context, _ *event.Event) error {
	m.InstanceFailedCount.Inc()

	return nil
}

func (m *MetricsExtension) OnInstanceDeleted(_ context.Context, _ *event.Event) error {
	m.InstanceDeletedCount.Inc()

	return nil
}

func (m *MetricsExtension) OnInstanceScaled(_ context.Context, _ *event.Event) error {
	m.InstanceScaledCount.Inc()

	return nil
}

func (m *MetricsExtension) OnInstanceSuspended(_ context.Context, _ *event.Event) error {
	m.InstanceSuspendedCount.Inc()

	return nil
}

func (m *MetricsExtension) OnInstanceUnsuspended(_ context.Context, _ *event.Event) error {
	m.InstanceUnsuspendedCount.Inc()

	return nil
}

// ──────────────────────────────────────────────────
// Deploy hooks
// ──────────────────────────────────────────────────

func (m *MetricsExtension) OnDeployStarted(_ context.Context, _ *event.Event) error {
	m.DeployStartedCount.Inc()

	return nil
}

func (m *MetricsExtension) OnDeploySucceeded(_ context.Context, _ *event.Event) error {
	m.DeploySucceededCount.Inc()

	return nil
}

func (m *MetricsExtension) OnDeployFailed(_ context.Context, _ *event.Event) error {
	m.DeployFailedCount.Inc()

	return nil
}

func (m *MetricsExtension) OnDeployRolledBack(_ context.Context, _ *event.Event) error {
	m.DeployRolledBackCount.Inc()

	return nil
}

// ──────────────────────────────────────────────────
// Health hooks
// ──────────────────────────────────────────────────

func (m *MetricsExtension) OnHealthCheckPassed(_ context.Context, _ *event.Event) error {
	m.HealthCheckPassedCount.Inc()

	return nil
}

func (m *MetricsExtension) OnHealthCheckFailed(_ context.Context, _ *event.Event) error {
	m.HealthCheckFailedCount.Inc()

	return nil
}

func (m *MetricsExtension) OnHealthDegraded(_ context.Context, _ *event.Event) error {
	m.HealthDegradedCount.Inc()

	return nil
}

func (m *MetricsExtension) OnHealthRecovered(_ context.Context, _ *event.Event) error {
	m.HealthRecoveredCount.Inc()

	return nil
}

// ──────────────────────────────────────────────────
// Network hooks
// ──────────────────────────────────────────────────

func (m *MetricsExtension) OnDomainAdded(_ context.Context, _ *event.Event) error {
	m.DomainAddedCount.Inc()

	return nil
}

func (m *MetricsExtension) OnDomainVerified(_ context.Context, _ *event.Event) error {
	m.DomainVerifiedCount.Inc()

	return nil
}

func (m *MetricsExtension) OnDomainRemoved(_ context.Context, _ *event.Event) error {
	m.DomainRemovedCount.Inc()

	return nil
}

func (m *MetricsExtension) OnCertProvisioned(_ context.Context, _ *event.Event) error {
	m.CertProvisionedCount.Inc()

	return nil
}

func (m *MetricsExtension) OnCertExpiring(_ context.Context, _ *event.Event) error {
	m.CertExpiringCount.Inc()

	return nil
}

// ──────────────────────────────────────────────────
// Admin hooks
// ──────────────────────────────────────────────────

func (m *MetricsExtension) OnTenantCreated(_ context.Context, _ *event.Event) error {
	m.TenantCreatedCount.Inc()

	return nil
}

func (m *MetricsExtension) OnTenantSuspended(_ context.Context, _ *event.Event) error {
	m.TenantSuspendedCount.Inc()

	return nil
}

func (m *MetricsExtension) OnTenantDeleted(_ context.Context, _ *event.Event) error {
	m.TenantDeletedCount.Inc()

	return nil
}

func (m *MetricsExtension) OnQuotaExceeded(_ context.Context, _ *event.Event) error {
	m.QuotaExceededCount.Inc()

	return nil
}

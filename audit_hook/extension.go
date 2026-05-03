package audithook

import (
	"context"
	"fmt"
	"log/slog"
	"maps"

	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/plugin"
)

// Compile-time interface checks.
var (
	_ plugin.Extension           = (*Extension)(nil)
	_ plugin.InstanceCreated     = (*Extension)(nil)
	_ plugin.InstanceStarted     = (*Extension)(nil)
	_ plugin.InstanceStopped     = (*Extension)(nil)
	_ plugin.InstanceFailed      = (*Extension)(nil)
	_ plugin.InstanceDeleted     = (*Extension)(nil)
	_ plugin.InstanceScaled      = (*Extension)(nil)
	_ plugin.InstanceSuspended   = (*Extension)(nil)
	_ plugin.InstanceUnsuspended = (*Extension)(nil)
	_ plugin.WorkloadCreated     = (*Extension)(nil)
	_ plugin.WorkloadUpdated     = (*Extension)(nil)
	_ plugin.WorkloadScaled      = (*Extension)(nil)
	_ plugin.WorkloadDeployed    = (*Extension)(nil)
	_ plugin.WorkloadPaused      = (*Extension)(nil)
	_ plugin.WorkloadResumed     = (*Extension)(nil)
	_ plugin.WorkloadRestarted   = (*Extension)(nil)
	_ plugin.WorkloadDeleted     = (*Extension)(nil)
	_ plugin.WorkloadFailed      = (*Extension)(nil)
	_ plugin.TemplateCreated     = (*Extension)(nil)
	_ plugin.TemplateUpdated     = (*Extension)(nil)
	_ plugin.TemplateDeleted     = (*Extension)(nil)
	_ plugin.DeployStarted       = (*Extension)(nil)
	_ plugin.DeploySucceeded     = (*Extension)(nil)
	_ plugin.DeployFailed        = (*Extension)(nil)
	_ plugin.DeployRolledBack    = (*Extension)(nil)
	_ plugin.HealthCheckPassed   = (*Extension)(nil)
	_ plugin.HealthCheckFailed   = (*Extension)(nil)
	_ plugin.HealthDegraded      = (*Extension)(nil)
	_ plugin.HealthRecovered     = (*Extension)(nil)
	_ plugin.DomainAdded         = (*Extension)(nil)
	_ plugin.DomainVerified      = (*Extension)(nil)
	_ plugin.DomainRemoved       = (*Extension)(nil)
	_ plugin.CertProvisioned     = (*Extension)(nil)
	_ plugin.CertExpiring        = (*Extension)(nil)
	_ plugin.TenantCreated       = (*Extension)(nil)
	_ plugin.TenantSuspended     = (*Extension)(nil)
	_ plugin.TenantDeleted       = (*Extension)(nil)
	_ plugin.QuotaExceeded       = (*Extension)(nil)
)

// Recorder is the interface that audit backends must implement.
// Matches chronicle.Emitter but defined locally to avoid the import.
type Recorder interface {
	Record(ctx context.Context, event *AuditEvent) error
}

// AuditEvent mirrors chronicle/audit.Event without a module dependency.
type AuditEvent struct {
	Action     string         `json:"action"`
	Resource   string         `json:"resource"`
	Category   string         `json:"category"`
	ResourceID string         `json:"resource_id,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	Outcome    string         `json:"outcome"`
	Severity   string         `json:"severity"`
	Reason     string         `json:"reason,omitempty"`
}

// RecorderFunc is an adapter to use a plain function as a Recorder.
type RecorderFunc func(ctx context.Context, event *AuditEvent) error

func (f RecorderFunc) Record(ctx context.Context, event *AuditEvent) error {
	return f(ctx, event)
}

// Extension bridges CtrlPlane lifecycle events to an audit trail backend.
type Extension struct {
	recorder Recorder
	enabled  map[string]bool
	logger   *slog.Logger
}

// New creates an Extension that emits audit events through the provided Recorder.
func New(r Recorder, opts ...Option) *Extension {
	e := &Extension{
		recorder: r,
		logger:   slog.Default(),
	}
	for _, opt := range opts {
		opt(e)
	}

	return e
}

// Name implements plugin.Extension.
func (e *Extension) Name() string { return "audit-hook" }

// SetRecorder swaps the active audit recorder at runtime. Called
// by the parent application when a richer Recorder (e.g. chronicle
// via DI) becomes available after construction. Safe to call from
// any goroutine: a single pointer swap, no in-flight events lost.
//
// Pass nil to disable recording entirely (events will silently
// drop). The pre-existing default-store recorder is replaced — to
// chain multiple recorders, wrap them in a composite before passing.
func (e *Extension) SetRecorder(r Recorder) {
	e.recorder = r
}

// ──────────────────────────────────────────────────
// Instance hooks
// ──────────────────────────────────────────────────

func (e *Extension) OnInstanceCreated(ctx context.Context, evt *event.Event) error {
	return e.recordEvent(ctx, ActionInstanceCreated, SeverityInfo, OutcomeSuccess,
		ResourceInstance, CategoryInstance, evt)
}

func (e *Extension) OnInstanceStarted(ctx context.Context, evt *event.Event) error {
	return e.recordEvent(ctx, ActionInstanceStarted, SeverityInfo, OutcomeSuccess,
		ResourceInstance, CategoryInstance, evt)
}

func (e *Extension) OnInstanceStopped(ctx context.Context, evt *event.Event) error {
	return e.recordEvent(ctx, ActionInstanceStopped, SeverityInfo, OutcomeSuccess,
		ResourceInstance, CategoryInstance, evt)
}

func (e *Extension) OnInstanceFailed(ctx context.Context, evt *event.Event) error {
	return e.recordEvent(ctx, ActionInstanceFailed, SeverityCritical, OutcomeFailure,
		ResourceInstance, CategoryInstance, evt)
}

func (e *Extension) OnInstanceDeleted(ctx context.Context, evt *event.Event) error {
	return e.recordEvent(ctx, ActionInstanceDeleted, SeverityInfo, OutcomeSuccess,
		ResourceInstance, CategoryInstance, evt)
}

func (e *Extension) OnInstanceScaled(ctx context.Context, evt *event.Event) error {
	return e.recordEvent(ctx, ActionInstanceScaled, SeverityInfo, OutcomeSuccess,
		ResourceInstance, CategoryInstance, evt)
}

func (e *Extension) OnInstanceSuspended(ctx context.Context, evt *event.Event) error {
	return e.recordEvent(ctx, ActionInstanceSuspended, SeverityWarning, OutcomeSuccess,
		ResourceInstance, CategoryInstance, evt)
}

func (e *Extension) OnInstanceUnsuspended(ctx context.Context, evt *event.Event) error {
	return e.recordEvent(ctx, ActionInstanceUnsuspended, SeverityInfo, OutcomeSuccess,
		ResourceInstance, CategoryInstance, evt)
}

// ──────────────────────────────────────────────────
// Workload hooks
// ──────────────────────────────────────────────────

func (e *Extension) OnWorkloadCreated(ctx context.Context, evt *event.Event) error {
	return e.recordEvent(ctx, ActionWorkloadCreated, SeverityInfo, OutcomeSuccess,
		ResourceWorkload, CategoryWorkload, evt)
}

func (e *Extension) OnWorkloadUpdated(ctx context.Context, evt *event.Event) error {
	return e.recordEvent(ctx, ActionWorkloadUpdated, SeverityInfo, OutcomeSuccess,
		ResourceWorkload, CategoryWorkload, evt)
}

func (e *Extension) OnWorkloadScaled(ctx context.Context, evt *event.Event) error {
	return e.recordEvent(ctx, ActionWorkloadScaled, SeverityInfo, OutcomeSuccess,
		ResourceWorkload, CategoryWorkload, evt)
}

func (e *Extension) OnWorkloadDeployed(ctx context.Context, evt *event.Event) error {
	return e.recordEvent(ctx, ActionWorkloadDeployed, SeverityInfo, OutcomeSuccess,
		ResourceWorkload, CategoryWorkload, evt)
}

func (e *Extension) OnWorkloadPaused(ctx context.Context, evt *event.Event) error {
	return e.recordEvent(ctx, ActionWorkloadPaused, SeverityWarning, OutcomeSuccess,
		ResourceWorkload, CategoryWorkload, evt)
}

func (e *Extension) OnWorkloadResumed(ctx context.Context, evt *event.Event) error {
	return e.recordEvent(ctx, ActionWorkloadResumed, SeverityInfo, OutcomeSuccess,
		ResourceWorkload, CategoryWorkload, evt)
}

func (e *Extension) OnWorkloadRestarted(ctx context.Context, evt *event.Event) error {
	return e.recordEvent(ctx, ActionWorkloadRestarted, SeverityInfo, OutcomeSuccess,
		ResourceWorkload, CategoryWorkload, evt)
}

func (e *Extension) OnWorkloadDeleted(ctx context.Context, evt *event.Event) error {
	return e.recordEvent(ctx, ActionWorkloadDeleted, SeverityWarning, OutcomeSuccess,
		ResourceWorkload, CategoryWorkload, evt)
}

func (e *Extension) OnWorkloadFailed(ctx context.Context, evt *event.Event) error {
	return e.recordEvent(ctx, ActionWorkloadFailed, SeverityCritical, OutcomeFailure,
		ResourceWorkload, CategoryWorkload, evt)
}

// ──────────────────────────────────────────────────
// Template hooks
// ──────────────────────────────────────────────────

func (e *Extension) OnTemplateCreated(ctx context.Context, evt *event.Event) error {
	return e.recordEvent(ctx, ActionTemplateCreated, SeverityInfo, OutcomeSuccess,
		ResourceTemplate, CategoryTemplate, evt)
}

func (e *Extension) OnTemplateUpdated(ctx context.Context, evt *event.Event) error {
	return e.recordEvent(ctx, ActionTemplateUpdated, SeverityInfo, OutcomeSuccess,
		ResourceTemplate, CategoryTemplate, evt)
}

func (e *Extension) OnTemplateDeleted(ctx context.Context, evt *event.Event) error {
	return e.recordEvent(ctx, ActionTemplateDeleted, SeverityWarning, OutcomeSuccess,
		ResourceTemplate, CategoryTemplate, evt)
}

// ──────────────────────────────────────────────────
// Deploy hooks
// ──────────────────────────────────────────────────

func (e *Extension) OnDeployStarted(ctx context.Context, evt *event.Event) error {
	return e.recordEvent(ctx, ActionDeployStarted, SeverityInfo, OutcomeSuccess,
		ResourceDeployment, CategoryDeploy, evt)
}

func (e *Extension) OnDeploySucceeded(ctx context.Context, evt *event.Event) error {
	return e.recordEvent(ctx, ActionDeploySucceeded, SeverityInfo, OutcomeSuccess,
		ResourceDeployment, CategoryDeploy, evt)
}

func (e *Extension) OnDeployFailed(ctx context.Context, evt *event.Event) error {
	return e.recordEvent(ctx, ActionDeployFailed, SeverityCritical, OutcomeFailure,
		ResourceDeployment, CategoryDeploy, evt)
}

func (e *Extension) OnDeployRolledBack(ctx context.Context, evt *event.Event) error {
	return e.recordEvent(ctx, ActionDeployRolledBack, SeverityWarning, OutcomeSuccess,
		ResourceDeployment, CategoryDeploy, evt)
}

// ──────────────────────────────────────────────────
// Health hooks
// ──────────────────────────────────────────────────

func (e *Extension) OnHealthCheckPassed(ctx context.Context, evt *event.Event) error {
	return e.recordEvent(ctx, ActionHealthCheckPassed, SeverityInfo, OutcomeSuccess,
		ResourceHealthCheck, CategoryHealth, evt)
}

func (e *Extension) OnHealthCheckFailed(ctx context.Context, evt *event.Event) error {
	return e.recordEvent(ctx, ActionHealthCheckFailed, SeverityCritical, OutcomeFailure,
		ResourceHealthCheck, CategoryHealth, evt)
}

func (e *Extension) OnHealthDegraded(ctx context.Context, evt *event.Event) error {
	return e.recordEvent(ctx, ActionHealthDegraded, SeverityWarning, OutcomeFailure,
		ResourceHealthCheck, CategoryHealth, evt)
}

func (e *Extension) OnHealthRecovered(ctx context.Context, evt *event.Event) error {
	return e.recordEvent(ctx, ActionHealthRecovered, SeverityInfo, OutcomeSuccess,
		ResourceHealthCheck, CategoryHealth, evt)
}

// ──────────────────────────────────────────────────
// Network hooks
// ──────────────────────────────────────────────────

func (e *Extension) OnDomainAdded(ctx context.Context, evt *event.Event) error {
	return e.recordEvent(ctx, ActionDomainAdded, SeverityInfo, OutcomeSuccess,
		ResourceDomain, CategoryNetwork, evt)
}

func (e *Extension) OnDomainVerified(ctx context.Context, evt *event.Event) error {
	return e.recordEvent(ctx, ActionDomainVerified, SeverityInfo, OutcomeSuccess,
		ResourceDomain, CategoryNetwork, evt)
}

func (e *Extension) OnDomainRemoved(ctx context.Context, evt *event.Event) error {
	return e.recordEvent(ctx, ActionDomainRemoved, SeverityInfo, OutcomeSuccess,
		ResourceDomain, CategoryNetwork, evt)
}

func (e *Extension) OnCertProvisioned(ctx context.Context, evt *event.Event) error {
	return e.recordEvent(ctx, ActionCertProvisioned, SeverityInfo, OutcomeSuccess,
		ResourceCertificate, CategoryNetwork, evt)
}

func (e *Extension) OnCertExpiring(ctx context.Context, evt *event.Event) error {
	return e.recordEvent(ctx, ActionCertExpiring, SeverityWarning, OutcomeFailure,
		ResourceCertificate, CategoryNetwork, evt)
}

// ──────────────────────────────────────────────────
// Admin hooks
// ──────────────────────────────────────────────────

func (e *Extension) OnTenantCreated(ctx context.Context, evt *event.Event) error {
	return e.recordEvent(ctx, ActionTenantCreated, SeverityInfo, OutcomeSuccess,
		ResourceTenant, CategoryAdmin, evt)
}

func (e *Extension) OnTenantSuspended(ctx context.Context, evt *event.Event) error {
	return e.recordEvent(ctx, ActionTenantSuspended, SeverityWarning, OutcomeSuccess,
		ResourceTenant, CategoryAdmin, evt)
}

func (e *Extension) OnTenantDeleted(ctx context.Context, evt *event.Event) error {
	return e.recordEvent(ctx, ActionTenantDeleted, SeverityInfo, OutcomeSuccess,
		ResourceTenant, CategoryAdmin, evt)
}

func (e *Extension) OnQuotaExceeded(ctx context.Context, evt *event.Event) error {
	return e.recordEvent(ctx, ActionQuotaExceeded, SeverityWarning, OutcomeFailure,
		ResourceQuota, CategoryAdmin, evt)
}

// recordEvent builds an AuditEvent from the event.Event and delegates to the recorder.
func (e *Extension) recordEvent(
	ctx context.Context,
	action, severity, outcome string,
	resource, category string,
	evt *event.Event,
) error {
	if e.enabled != nil && !e.enabled[action] {
		return nil
	}

	resourceID := evt.InstanceID.String()
	if resourceID == "" {
		resourceID = evt.ID.String()
	}

	meta := make(map[string]any, len(evt.Payload)+3)
	meta["tenant_id"] = evt.TenantID

	if evt.ActorID != "" {
		meta["actor_id"] = evt.ActorID
	}

	if !evt.InstanceID.IsNil() {
		meta["instance_id"] = evt.InstanceID.String()
	}

	maps.Copy(meta, evt.Payload)

	var reason string
	if errVal, ok := evt.Payload["error"]; ok {
		reason = fmt.Sprintf("%v", errVal)
	}

	auditEvt := &AuditEvent{
		Action:     action,
		Resource:   resource,
		Category:   category,
		ResourceID: resourceID,
		Metadata:   meta,
		Outcome:    outcome,
		Severity:   severity,
		Reason:     reason,
	}

	if e.recorder == nil {
		return nil
	}

	if recErr := e.recorder.Record(ctx, auditEvt); recErr != nil {
		e.logger.Warn("audit_hook: failed to record audit event",
			"action", action,
			"resource_id", resourceID,
			"error", recErr,
		)
	}

	return nil
}

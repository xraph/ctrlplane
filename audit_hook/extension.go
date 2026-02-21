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

	if recErr := e.recorder.Record(ctx, auditEvt); recErr != nil {
		e.logger.Warn("audit_hook: failed to record audit event",
			"action", action,
			"resource_id", resourceID,
			"error", recErr,
		)
	}

	return nil
}

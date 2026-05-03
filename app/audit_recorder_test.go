package app

import (
	"context"
	"testing"

	"github.com/xraph/ctrlplane/admin"
	audithook "github.com/xraph/ctrlplane/audit_hook"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/store/memory"
)

// TestStoreAuditRecorder_RoundTrip asserts that an audit event
// dispatched via the recorder lands in the store and is readable
// back via QueryAuditLog. Regression for the empty-Audit-Log bug
// — until this turn nothing called InsertAuditEntry.
func TestStoreAuditRecorder_RoundTrip(t *testing.T) {
	t.Parallel()

	s := memory.New()
	rec := newStoreAuditRecorder(s)

	evt := &audithook.AuditEvent{
		Action:     audithook.ActionInstanceCreated,
		Resource:   audithook.ResourceInstance,
		Category:   audithook.CategoryInstance,
		ResourceID: "inst_test_123",
		Outcome:    audithook.OutcomeSuccess,
		Severity:   audithook.SeverityInfo,
		Metadata: map[string]any{
			"tenant_id":   "tenant-x",
			"actor_id":    "user_42",
			"instance_id": "inst_test_123",
			"image":       "nginx:alpine",
		},
	}

	ctx := auth.WithClaims(context.Background(), &auth.Claims{
		SubjectID: "user_42",
		TenantID:  "tenant-x",
		Roles:     []string{"system:admin"},
	})

	if err := rec.Record(ctx, evt); err != nil {
		t.Fatalf("Record: %v", err)
	}

	res, err := s.QueryAuditLog(ctx, admin.AuditQuery{TenantID: "tenant-x", Limit: 10})
	if err != nil {
		t.Fatalf("QueryAuditLog: %v", err)
	}

	if len(res.Items) != 1 {
		t.Fatalf("items: want 1 entry, got %d", len(res.Items))
	}

	got := res.Items[0]
	if got.Action != audithook.ActionInstanceCreated {
		t.Fatalf("action: want %q, got %q", audithook.ActionInstanceCreated, got.Action)
	}

	if got.Resource != audithook.ResourceInstance {
		t.Fatalf("resource: want %q, got %q", audithook.ResourceInstance, got.Resource)
	}

	if got.ResourceID != "inst_test_123" {
		t.Fatalf("resource_id: want inst_test_123, got %q", got.ResourceID)
	}

	if got.ActorID != "user_42" {
		t.Fatalf("actor_id: want user_42, got %q", got.ActorID)
	}

	if got.TenantID != "tenant-x" {
		t.Fatalf("tenant_id: want tenant-x, got %q", got.TenantID)
	}
	// First-class fields should be lifted out of details so they
	// aren't double-stored. Outcome/severity/category should be
	// surfaced under details for forensics.
	if _, found := got.Details["tenant_id"]; found {
		t.Errorf("details should not duplicate tenant_id")
	}

	if got.Details["outcome"] != audithook.OutcomeSuccess {
		t.Errorf("details.outcome: want %q, got %v", audithook.OutcomeSuccess, got.Details["outcome"])
	}

	if got.Details["severity"] != audithook.SeverityInfo {
		t.Errorf("details.severity: want %q, got %v", audithook.SeverityInfo, got.Details["severity"])
	}

	if got.Details["image"] != "nginx:alpine" {
		t.Errorf("details.image: want nginx:alpine, got %v", got.Details["image"])
	}
}

// TestStoreAuditRecorder_NilSafetyDoesNotPanic asserts the
// recorder degrades gracefully when its store or the event is
// nil. Defensive — keeps a misconfigured recorder from crashing
// every event publish.
func TestStoreAuditRecorder_NilSafetyDoesNotPanic(t *testing.T) {
	t.Parallel()

	rec := newStoreAuditRecorder(nil)
	if err := rec.Record(context.Background(), &audithook.AuditEvent{}); err != nil {
		t.Fatalf("nil-store Record should not error: %v", err)
	}

	rec = newStoreAuditRecorder(memory.New())
	if err := rec.Record(context.Background(), nil); err != nil {
		t.Fatalf("nil-event Record should not error: %v", err)
	}
}

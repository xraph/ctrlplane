package managedstate

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"time"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/managed"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/workload"
)

// ControllerBackend opens a single authority transaction for retained controller rows.
// Implementations lock the serial key and all target IDs in stable order before Now.
type ControllerBackend interface {
	ControllerAtomic(ctx context.Context, serial string, keys []managed.Key, change func(ControllerTransaction) error) error
}

// ControllerRecord is an internal retained row, separate from A's target document.
type ControllerRecord struct {
	Key  string
	Data []byte
}

// ControllerTransaction supplies A's backend without opening or committing another transaction.
// It stages all writes and exposes no remote callbacks or caller-controlled row patches.
type ControllerTransaction interface {
	Backend
	Now() time.Time
	ReadRecord(ctx context.Context, kind, scope, key string) ([]byte, error)
	WriteRecord(ctx context.Context, kind, scope, key string, raw []byte, insert bool) error
	ListRecords(ctx context.Context, kind, scope, after string, limit int) ([]ControllerRecord, error)
	ReserveRows(ctx context.Context, target managed.Identity, previous *managed.Identity) error
	ProjectRows(ctx context.Context, target managed.Identity, receipt managed.ActionReceipt, replay bool) error
}

func (r *Repository) controller(ctx context.Context, serial string, keys []managed.Key, fn func(ControllerTransaction) error) error {
	b, ok := r.backend.(ControllerBackend)
	if !ok {
		return managed.ErrUnsupported
	}

	return b.ControllerAtomic(ctx, serial, keys, fn)
}

func controllerRead[T any](ctx context.Context, tx ControllerTransaction, kind, scope, key string) (T, error) {
	var value T

	b, err := tx.ReadRecord(ctx, kind, scope, key)
	if err != nil {
		return value, err
	}

	if err = json.Unmarshal(b, &value); err != nil {
		return value, managed.ErrUnavailable
	}

	return value, nil
}

func controllerWrite(ctx context.Context, tx ControllerTransaction, kind, scope, key string, value any, insert bool, maximum int) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}

	if len(b) > maximum {
		return managed.ErrCapacity
	}

	return tx.WriteRecord(ctx, kind, scope, key, b, insert)
}

func controllerFingerprint(value any) (string, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return "", err
	}

	return managed.Digest(b), nil
}

func controllerScope(installation, authority string, scope managed.AuditScope) (string, error) {
	if !token(installation) || !token(authority) || !validAuditScope(scope) {
		return "", managed.ErrInvalid
	}

	return controllerFingerprint(struct {
		Installation string             `json:"installation"`
		Authority    string             `json:"authority"`
		Scope        managed.AuditScope `json:"scope"`
	}{installation, authority, scope})
}

func validAuditScope(scope managed.AuditScope) bool {
	switch scope.Kind {
	case "tenant":
		return token(scope.TenantID) && scope.DenialScope == ""
	case "global":
		return scope.TenantID == "" && scope.DenialScope == ""
	case "denial":
		return scope.TenantID == "" && token(scope.DenialScope)
	default:
		return false
	}
}

func validAuditActor(actor managed.AuditActor) bool {
	return actor.Unauthenticated && actor.Subject == "" || !actor.Unauthenticated && token(actor.Subject)
}

func controllerParent(ctx context.Context, tx ControllerTransaction, c managed.Command) (*managed.Target, error) {
	if !key(c.Key) || c.ID.Prefix() != managed.PrefixOperation || !token(c.Owner) || c.Revision == 0 {
		return nil, managed.ErrInvalid
	}

	t, err := tx.Read(ctx, c.Key)
	if err != nil {
		return nil, err
	}

	if t.Phase == managed.Deleted {
		return nil, managed.ErrBlocked
	}

	if t.Revision != c.Revision || t.LeaseEpoch != c.LeaseEpoch || t.LeaseOwner != c.Owner || !tx.Now().Before(t.LeaseUntil) {
		return nil, managed.ErrConflict
	}

	return t, nil
}

func controllerAbsent(err error) bool { return errors.Is(err, managed.ErrNotFound) }

func validAccessAction(action string) bool {
	switch action {
	case "managed.deploy", "managed.pause", "managed.retire", "managed.rollback", "managed.get", "managed.reconcile",
		"instances.list", "instances.detail", "instances.create", "instances.update", "instances.delete", "instances.start", "instances.stop", "instances.restart", "instances.scale", "instances.suspend", "instances.unsuspend",
		"workloads.list", "workloads.detail", "workloads.instances", "workloads.health", "workloads.create", "workloads.update", "workloads.delete", "workloads.pause", "workloads.resume", "workloads.scale", "workloads.restart", "workloads.deploy",
		"releases.list", "releases.detail", "deployments.recent", "deployments.list", "deployments.detail", "deployments.create", "deployments.cancel", "deployments.rollback",
		"health.summary", "health.checks", "datacenters.instances", "datacenters.list", "datacenters.detail", "providers.list", "providers.detail", "providers.purge", "tenants.delete", "transport.authenticate":
		return true
	default:
		return false
	}
}

func validAccessResource(kind string) bool {
	switch kind {
	case "instance", "workload", "release", "deployment", "health", "datacenter", "provider", "tenant", "controller", "transport":
		return true
	default:
		return false
	}
}

func validAccessReason(reason string) bool {
	switch reason {
	case "authorized", "policy_denied", "unauthenticated", "scope_mismatch", "policy_unavailable", "resource_unavailable", "not_found", "invalid_request", "service_unavailable":
		return true
	default:
		return false
	}
}

func intentFromAccess(q managed.AccessAuditRequest, digest string, now time.Time) managed.AuditIntent {
	return managed.AuditIntent{ID: q.AttemptID, MappingVersion: "1", SourceKey: q.AttemptID.String(), Installation: q.Installation, Authority: q.Authority, ApplicationID: q.ApplicationID, OrganizationID: q.OrganizationID, Actor: q.Actor, Scope: q.Scope, Action: q.Action, ResourceKind: q.ResourceKind, ResourceID: q.ResourceID, Outcome: q.Outcome, Reason: q.Reason, SelectorDigest: q.SelectorDigest, DecisionID: q.DecisionID, CorrelationID: q.CorrelationID, SourceDigest: digest, OccurredAt: q.OccurredAt, AcceptedAt: now}
}

// RecordManagedAccessAudit atomically retains one exact attempt receipt and audit intent.
func (r *Repository) RecordManagedAccessAudit(ctx context.Context, q managed.AccessAuditRequest) (managed.AccessAuditReceipt, error) {
	var out managed.AccessAuditReceipt

	scope, err := controllerScope(q.Installation, q.Authority, q.Scope)
	if err != nil {
		return out, err
	}

	if q.AttemptID.Prefix() != managed.PrefixOperation || !token(q.ApplicationID) || !token(q.OrganizationID) || !validAuditActor(q.Actor) || !validAccessAction(q.Action) || !validAccessResource(q.ResourceKind) || !token(q.ResourceID) || !validAccessReason(q.Reason) || q.OccurredAt.IsZero() || q.SelectorDigest != "" && !digest(q.SelectorDigest) || q.DecisionID != "" && !token(q.DecisionID) || q.CorrelationID != "" && !token(q.CorrelationID) {
		return out, managed.ErrInvalid
	}

	if q.Outcome != "allowed" && q.Outcome != "denied" && q.Outcome != "unavailable" {
		return out, managed.ErrInvalid
	}

	if q.Actor.Unauthenticated && q.Scope.Kind != "denial" {
		return out, managed.ErrInvalid
	}

	if q.Outcome == "allowed" && (q.Actor.Unauthenticated || q.Scope.Kind == "denial" || q.Reason != "authorized") {
		return out, managed.ErrInvalid
	}

	fingerprint, err := controllerFingerprint(q)
	if err != nil {
		return out, err
	}

	err = r.controller(ctx, "access:"+q.AttemptID.String(), nil, func(tx ControllerTransaction) error {
		saved, readErr := controllerRead[managed.AccessAuditReceipt](ctx, tx, "access", "", q.AttemptID.String())
		if readErr == nil {
			if saved.Digest != fingerprint {
				return managed.ErrConflict
			}

			out = saved

			return nil
		}

		if !controllerAbsent(readErr) {
			return readErr
		}

		if _, collision := tx.ReadRecord(ctx, "action", "", q.AttemptID.String()); !controllerAbsent(collision) {
			if collision == nil {
				return managed.ErrConflict
			}

			return collision
		}

		if q.OccurredAt.After(tx.Now()) {
			return managed.ErrInvalid
		}

		intent := intentFromAccess(q, fingerprint, tx.Now())

		out = managed.AccessAuditReceipt{ID: q.AttemptID, Digest: fingerprint, AuditID: q.AttemptID, AcceptedAt: tx.Now()}
		if err := controllerWrite(ctx, tx, "access", "", q.AttemptID.String(), out, true, managed.MaxControllerRecord); err != nil {
			return err
		}

		return controllerWrite(ctx, tx, "audit", scope, q.AttemptID.String(), intent, true, managed.MaxAuditIntent)
	})
	if err != nil {
		return managed.AccessAuditReceipt{}, err
	}

	return out, nil
}

// ReadManagedOperation recovers accepted commands independently of current desired state.
func (r *Repository) ReadManagedOperation(ctx context.Context, q managed.OperationKey) (managed.OperationState, error) {
	var out managed.OperationState
	if !key(q.Key) || q.ActionID.Prefix() != managed.PrefixOperation {
		return out, managed.ErrInvalid
	}

	err := r.controller(ctx, "action:"+q.ActionID.String(), []managed.Key{q.Key}, func(tx ControllerTransaction) error {
		saved, err := controllerRead[managed.OperationState](ctx, tx, "operation", q.TenantID, q.ActionID.String())
		if err != nil {
			return err
		}

		if saved.Key != q {
			return managed.ErrAuthority
		}

		out = saved

		return nil
	})

	return out, err
}

// SubmitManagedAction accepts the desired decision, reserved identities and audit together.
func (r *Repository) SubmitManagedAction(ctx context.Context, q managed.ActionSubmission) (managed.ActionReceipt, error) {
	var out managed.ActionReceipt

	fingerprint, err := managed.ActionDigest(q)
	if err != nil {
		return out, err
	}

	if !key(q.Key) || q.ID.Prefix() != managed.PrefixOperation || !token(q.Installation) || !token(q.Authority) || !token(q.ApplicationID) || !token(q.OrganizationID) || !validAuditActor(q.Actor) || q.Actor.Unauthenticated || !evidence(q.OriginalCommand) || managed.Digest(q.OriginalCommand) != q.OriginalDigest || fingerprint != q.CanonicalDigest || q.OccurredAt.IsZero() {
		return out, managed.ErrInvalid
	}

	keys := []managed.Key{q.Key}
	if q.NewTarget != nil {
		keys = append(keys, q.NewTarget.Key)
	}

	err = r.controller(ctx, "action:"+q.ID.String(), keys, func(tx ControllerTransaction) error {
		saved, readErr := controllerRead[managed.ActionReceipt](ctx, tx, "action", "", q.ID.String())
		if readErr == nil {
			if saved.CanonicalDigest != fingerprint {
				return managed.ErrConflict
			}

			out = saved

			return nil
		}

		if !controllerAbsent(readErr) {
			return readErr
		}

		if _, collision := tx.ReadRecord(ctx, "access", "", q.ID.String()); !controllerAbsent(collision) {
			if collision == nil {
				return managed.ErrConflict
			}

			return collision
		}

		if q.OccurredAt.After(tx.Now()) {
			return managed.ErrInvalid
		}

		repo := New(tx)

		var target *managed.Target

		switch q.Kind {
		case managed.ActionDeploy, managed.ActionRollback:
			if q.NewTarget == nil || q.ReleaseID.Prefix() != id.PrefixRelease {
				return managed.ErrInvalid
			}

			if _, err := managed.ParseProvisionSpec(q.NewTarget.Manifest); err != nil {
				return err
			}

			if err := identity(*q.NewTarget); err != nil {
				return err
			}

			var previous *managed.Identity

			if q.Revision == 0 {
				if q.Kind != managed.ActionDeploy || q.Owner != "" || !q.RollbackReleaseID.IsNil() || q.ExpectedDesiredRevision != 0 || q.LeaseEpoch != 0 || q.NewTarget.Key != q.Key {
					return managed.ErrInvalid
				}

				if _, err := tx.Read(ctx, q.Key); err == nil {
					return managed.ErrConflict
				} else if !controllerAbsent(err) {
					return err
				}
			} else {
				parent, err := controllerParent(ctx, tx, q.Command)
				if err != nil {
					return err
				}

				if parent.DesiredRevision != q.ExpectedDesiredRevision {
					return managed.ErrConflict
				}

				if q.Kind == managed.ActionRollback {
					if q.RollbackReleaseID.Prefix() != id.PrefixRelease {
						return managed.ErrInvalid
					}

					binding, err := controllerRead[managed.ReleaseBinding](ctx, tx, "release", q.TenantID, q.RollbackReleaseID.String())
					if err != nil {
						return err
					}

					if binding.Target != parent.Key || !reflect.DeepEqual(binding.Manifest, q.NewTarget.Manifest) {
						return managed.ErrAuthority
					}

					oldAction, err := controllerRead[managed.ActionReceipt](ctx, tx, "action", "", binding.ActionID.String())
					if err != nil {
						return err
					}

					if err = tx.ProjectRows(ctx, parent.Identity, oldAction, true); err != nil {
						return err
					}
				} else if !q.RollbackReleaseID.IsNil() {
					return managed.ErrInvalid
				}

				previous = &parent.Identity

				if _, err = tx.Read(ctx, q.NewTarget.Key); err == nil {
					return managed.ErrConflict
				} else if !controllerAbsent(err) {
					return err
				}

				if _, err = repo.AllocateManagedReplacement(ctx, managed.ReplacementRequest{Command: q.Command, Replacement: *q.NewTarget}); err != nil {
					return err
				}
			}

			if err := tx.ReserveRows(ctx, *q.NewTarget, previous); err != nil {
				return err
			}

			var err error

			target, err = repo.CreateManagedTarget(ctx, *q.NewTarget)
			if err != nil {
				return err
			}

			if err = controllerWrite(ctx, tx, "release_reservation", "", q.ReleaseID.String(), q.NewTarget.Key, true, managed.MaxControllerRecord); err != nil {
				return err
			}
		case managed.ActionPause, managed.ActionRetire:
			if q.NewTarget != nil || !q.ReleaseID.IsNil() {
				return managed.ErrInvalid
			}

			parent, err := controllerParent(ctx, tx, q.Command)
			if err != nil {
				return err
			}

			if parent.DesiredRevision != q.ExpectedDesiredRevision {
				return managed.ErrConflict
			}

			if _, err = managed.ParseManagedSpec(parent.Manifest); err != nil {
				return err
			}

			mode := managed.Paused
			if q.Kind == managed.ActionRetire {
				mode = managed.Retiring
			}

			if _, err = repo.SetManagedDesired(ctx, managed.DesiredRequest{Command: q.Command, Mode: mode}); err != nil {
				return err
			}

			target, err = tx.Read(ctx, q.Key)
			if err != nil {
				return err
			}
		default:
			return managed.ErrUnsupported
		}

		out = managed.ActionReceipt{ID: q.ID, Key: q.Key, Target: target.Key, CanonicalDigest: fingerprint, OriginalDigest: q.OriginalDigest, Revision: target.Revision, DesiredRevision: target.DesiredRevision, AuditID: q.ID, ReleaseID: q.ReleaseID, AcceptedAt: tx.Now()}
		access := managed.AccessAuditRequest{AttemptID: q.ID, Installation: q.Installation, Authority: q.Authority, ApplicationID: q.ApplicationID, OrganizationID: q.OrganizationID, Actor: q.Actor, Scope: managed.AuditScope{Kind: "tenant", TenantID: target.TenantID}, Action: "managed." + string(q.Kind), ResourceKind: "instance", ResourceID: target.InstanceID.String(), Outcome: "allowed", Reason: "authorized", OccurredAt: q.OccurredAt}

		scope, err := controllerScope(q.Installation, q.Authority, access.Scope)
		if err != nil {
			return err
		}

		operation := managed.OperationState{Key: managed.OperationKey{Key: target.Key, ActionID: q.ID}, Submission: q, Receipt: out, Revision: 1, Observation: managed.OperationObservation{Kind: "pending", ObservedAt: tx.Now()}}
		if err = controllerWrite(ctx, tx, "operation", target.TenantID, q.ID.String(), operation, true, managed.MaxControllerRecord); err != nil {
			return err
		}

		if err = controllerWrite(ctx, tx, "action", "", q.ID.String(), out, true, managed.MaxControllerRecord); err != nil {
			return err
		}

		return controllerWrite(ctx, tx, "audit", scope, q.ID.String(), intentFromAccess(access, fingerprint, tx.Now()), true, managed.MaxAuditIntent)
	})
	if err != nil {
		return managed.ActionReceipt{}, err
	}

	return out, nil
}

func acceptedOperation(ctx context.Context, tx ControllerTransaction, k managed.OperationKey) (managed.OperationState, error) {
	state, err := controllerRead[managed.OperationState](ctx, tx, "operation", k.TenantID, k.ActionID.String())
	if err != nil {
		return state, err
	}

	if state.Key != k || state.Receipt.Target != k.Key {
		return state, managed.ErrAuthority
	}

	receipt, err := controllerRead[managed.ActionReceipt](ctx, tx, "action", "", k.ActionID.String())
	if err != nil {
		return state, err
	}

	if !reflect.DeepEqual(receipt, state.Receipt) {
		return state, managed.ErrConflict
	}

	scope, err := controllerScope(state.Submission.Installation, state.Submission.Authority, managed.AuditScope{Kind: "tenant", TenantID: k.TenantID})
	if err != nil {
		return state, err
	}

	intent, err := controllerRead[managed.AuditIntent](ctx, tx, "audit", scope, receipt.AuditID.String())
	if err != nil {
		return state, err
	}

	if intent.SourceDigest != receipt.CanonicalDigest || intent.ResourceID != k.InstanceID.String() {
		return state, managed.ErrConflict
	}

	return state, nil
}

// ProjectManagedRows derives reserved pending rows under the same authority transaction.
func (r *Repository) ProjectManagedRows(ctx context.Context, q managed.ProjectionRequest) (managed.ProjectionReceipt, error) {
	var out managed.ProjectionReceipt
	if !key(q.Key) || q.ActionID.Prefix() != managed.PrefixOperation || q.Stage != managed.ProjectionPending {
		return out, managed.ErrInvalid
	}

	fingerprint, err := controllerFingerprint(q)
	if err != nil {
		return out, err
	}

	err = r.controller(ctx, "projection:"+q.ActionID.String(), []managed.Key{q.Key}, func(tx ControllerTransaction) error {
		operation, err := acceptedOperation(ctx, tx, managed.OperationKey{Key: q.Key, ActionID: q.ActionID})
		if err != nil {
			return err
		}

		target, err := tx.Read(ctx, q.Key)
		if err != nil {
			return err
		}

		if target.Phase == managed.Deleted || operation.Receipt.ReleaseID.IsNil() {
			return managed.ErrBlocked
		}

		saved, readErr := controllerRead[managed.ProjectionReceipt](ctx, tx, "projection", q.TenantID, q.ActionID.String())
		if readErr == nil {
			if saved.ID != q.ID || saved.Digest != fingerprint {
				return managed.ErrConflict
			}

			if err = projectionAssociations(ctx, tx, target.Identity, operation, saved); err != nil {
				return err
			}

			if err = tx.ProjectRows(ctx, target.Identity, operation.Receipt, true); err != nil {
				return err
			}

			out = saved

			return nil
		}

		if !controllerAbsent(readErr) {
			return readErr
		}

		if _, err = controllerParent(ctx, tx, q.Command); err != nil {
			return err
		}

		if target.Phase != managed.Open || target.Provision.Issued {
			return managed.ErrBlocked
		}

		if operation.Submission.NewTarget == nil || !EqualIdentity(*operation.Submission.NewTarget, target.Identity) {
			return managed.ErrAuthority
		}

		if err = tx.ProjectRows(ctx, target.Identity, operation.Receipt, false); err != nil {
			return err
		}

		owner, err := managed.ProjectionOwner(target.Identity)
		if err != nil {
			return err
		}

		binding := managed.ReleaseBinding{Key: managed.ReleaseKey{TenantID: q.TenantID, ReleaseID: operation.Receipt.ReleaseID}, Target: q.Key, ActionID: q.ActionID, Manifest: target.Manifest, Version: 1, AcceptedAt: operation.Receipt.AcceptedAt}
		if err = controllerWrite(ctx, tx, "release", q.TenantID, binding.Key.ReleaseID.String(), binding, true, managed.MaxControllerRecord); err != nil {
			return err
		}

		out = managed.ProjectionReceipt{Receipt: managed.Receipt{ID: q.ID, Digest: fingerprint, Revision: target.Revision, LeaseEpoch: target.LeaseEpoch, AcceptedAt: tx.Now(), LeaseUntil: target.LeaseUntil}, ActionID: q.ActionID, Target: q.Key, OwnerID: owner, ReleaseID: binding.Key.ReleaseID, SpecDigest: target.Manifest.SpecDigest, Stage: q.Stage}
		if err = controllerWrite(ctx, tx, "projection", q.TenantID, q.ActionID.String(), out, true, managed.MaxControllerRecord); err != nil {
			return err
		}

		return controllerWrite(ctx, tx, "target_projection", q.TenantID, q.InstanceID.String(), out, true, managed.MaxControllerRecord)
	})
	if err != nil {
		return managed.ProjectionReceipt{}, err
	}

	return out, nil
}

// projectionAssociations recovers every retained association without repairing history.
func projectionAssociations(ctx context.Context, tx ControllerTransaction, target managed.Identity, operation managed.OperationState, receipt managed.ProjectionReceipt) error {
	owner, err := managed.ProjectionOwner(target)
	if err != nil {
		return err
	}

	if operation.Submission.NewTarget == nil || !EqualIdentity(*operation.Submission.NewTarget, target) || receipt.Target != target.Key || receipt.ActionID != operation.Key.ActionID || receipt.ReleaseID != operation.Receipt.ReleaseID || receipt.OwnerID != owner || receipt.SpecDigest != target.Manifest.SpecDigest || receipt.Stage != managed.ProjectionPending {
		return managed.ErrConflict
	}

	binding, err := controllerRead[managed.ReleaseBinding](ctx, tx, "release", target.TenantID, receipt.ReleaseID.String())
	if err != nil {
		return err
	}

	expected := managed.ReleaseBinding{Key: managed.ReleaseKey{TenantID: target.TenantID, ReleaseID: receipt.ReleaseID}, Target: target.Key, ActionID: receipt.ActionID, Manifest: target.Manifest, Version: 1, AcceptedAt: operation.Receipt.AcceptedAt}
	if !reflect.DeepEqual(binding, expected) {
		return managed.ErrConflict
	}

	saved, err := controllerRead[managed.ProjectionReceipt](ctx, tx, "target_projection", target.TenantID, target.InstanceID.String())
	if err != nil {
		return err
	}

	if !reflect.DeepEqual(saved, receipt) {
		return managed.ErrConflict
	}

	return nil
}

// ReadManagedRelease reads an immutable accepted manifest association in tenant scope.
func (r *Repository) ReadManagedRelease(ctx context.Context, q managed.ReleaseKey) (managed.ReleaseBinding, error) {
	var out managed.ReleaseBinding
	if !token(q.TenantID) || q.ReleaseID.Prefix() != id.PrefixRelease {
		return out, managed.ErrInvalid
	}

	err := r.controller(ctx, "release:"+q.ReleaseID.String(), nil, func(tx ControllerTransaction) error {
		var err error

		out, err = controllerRead[managed.ReleaseBinding](ctx, tx, "release", q.TenantID, q.ReleaseID.String())

		return err
	})

	return out, err
}

// BindManagedRelease verifies the association created by atomic reserved projection.
// It cannot adopt or attach an ordinary generic release to a managed target.
func (r *Repository) BindManagedRelease(ctx context.Context, q managed.ReleaseBindingRequest) (managed.ReleaseBinding, error) {
	var out managed.ReleaseBinding

	err := r.controller(ctx, "release:"+q.ReleaseID.String(), []managed.Key{q.Key}, func(tx ControllerTransaction) error {
		operation, err := acceptedOperation(ctx, tx, managed.OperationKey{Key: q.Key, ActionID: q.ActionID})
		if err != nil {
			return err
		}

		if operation.Receipt.ReleaseID != q.ReleaseID {
			return managed.ErrAuthority
		}

		target, err := tx.Read(ctx, q.Key)
		if err != nil {
			return err
		}

		if target.Phase == managed.Deleted {
			return managed.ErrBlocked
		}

		if err = tx.ProjectRows(ctx, target.Identity, operation.Receipt, true); err != nil {
			return err
		}

		out, err = controllerRead[managed.ReleaseBinding](ctx, tx, "release", q.TenantID, q.ReleaseID.String())

		return err
	})

	return out, err
}

// ProjectionEntities derives ordinary snapshots exclusively from accepted manifest facts.
func ProjectionEntities(target managed.Identity, receipt managed.ActionReceipt, ownerOrigin managed.Identity) (*instance.Instance, *workload.Workload, *deploy.Release, error) {
	spec, err := managed.ParseProvisionSpec(target.Manifest)
	if err != nil {
		return nil, nil, nil, err
	}

	services := make([]provider.ServiceSpec, 0, len(spec.Services))

	snapshots := make([]provider.ServiceSnapshot, 0, len(spec.Services))
	for _, s := range spec.Services {
		cpu, err := strconv.Atoi(string(s.CPUMillis))
		if err != nil {
			return nil, nil, nil, err
		}

		memory, err := strconv.Atoi(string(s.MemoryMiB))
		if err != nil {
			return nil, nil, nil, err
		}

		services = append(services, provider.ServiceSpec{Name: s.Name, Image: s.Image, Role: provider.ServiceRole(s.Role), Resources: provider.ResourceSpec{CPUMillis: cpu, MemoryMB: memory, Replicas: 1}})
		snapshots = append(snapshots, provider.ServiceSnapshot{Name: s.Name, Image: s.Image})
	}

	owner, err := managed.ProjectionOwner(target)
	if err != nil {
		return nil, nil, nil, err
	}

	labels := map[string]string{"ctrlplane.managed_target": target.InstanceID.String(), "ctrlplane.managed_spec": target.Manifest.SpecDigest}
	if !owner.IsNil() {
		labels["ctrlplane.workload"] = owner.String()
	}

	inst := &instance.Instance{Entity: ctrlplane.Entity{ID: target.InstanceID, CreatedAt: receipt.AcceptedAt, UpdatedAt: receipt.AcceptedAt}, TenantID: target.TenantID, Name: target.Physical.Project, Slug: target.InstanceID.String(), ProviderName: target.Physical.Provider, Region: target.Physical.Placement, State: provider.StateProvisioning, Kind: provider.KindDeployment, Services: services, Labels: labels}
	release := &deploy.Release{Entity: ctrlplane.Entity{ID: receipt.ReleaseID, CreatedAt: receipt.AcceptedAt, UpdatedAt: receipt.AcceptedAt}, TenantID: target.TenantID, InstanceID: target.InstanceID, Version: 1, Services: snapshots}

	var w *workload.Workload

	if !owner.IsNil() {
		ownerSpec, err := managed.ParseProvisionSpec(ownerOrigin.Manifest)
		if err != nil {
			return nil, nil, nil, err
		}

		ownerServices := make([]provider.ServiceSpec, 0, len(ownerSpec.Services))
		for _, s := range ownerSpec.Services {
			cpu, err := strconv.Atoi(string(s.CPUMillis))
			if err != nil {
				return nil, nil, nil, err
			}

			memory, err := strconv.Atoi(string(s.MemoryMiB))
			if err != nil {
				return nil, nil, nil, err
			}

			ownerServices = append(ownerServices, provider.ServiceSpec{Name: s.Name, Image: s.Image, Role: provider.ServiceRole(s.Role), Resources: provider.ResourceSpec{CPUMillis: cpu, MemoryMB: memory, Replicas: 1}})
		}

		w = &workload.Workload{Entity: ctrlplane.Entity{ID: owner, CreatedAt: receipt.AcceptedAt, UpdatedAt: receipt.AcceptedAt}, TenantID: target.TenantID, Name: owner.String(), Slug: owner.String(), ProviderName: ownerOrigin.Physical.Provider, Region: ownerOrigin.Physical.Placement, Kind: provider.KindDeployment, Services: ownerServices, ReplicaCount: 1, State: workload.StateProvisioning, Labels: map[string]string{"ctrlplane.managed_origin": ownerOrigin.InstanceID.String()}}
	}

	return inst, w, release, nil
}

// EqualProjectedInstance checks immutable identity without overwriting later visible state.
func EqualProjectedInstance(a, b *instance.Instance) bool {
	return a != nil && b != nil && a.ID == b.ID && a.TenantID == b.TenantID && a.ProviderName == b.ProviderName && a.Region == b.Region && a.Kind == b.Kind && a.Name == b.Name && a.Slug == b.Slug && reflect.DeepEqual(a.Services, b.Services) && a.State != provider.StateDestroyed
}

// EqualProjectedOwner checks immutable owner provenance and original service snapshot.
func EqualProjectedOwner(a, b *workload.Workload) bool {
	return a != nil && b != nil && a.ID == b.ID && a.TenantID == b.TenantID && a.ProviderName == b.ProviderName && a.Region == b.Region && a.Kind == b.Kind && reflect.DeepEqual(a.Services, b.Services) && a.State != workload.StateDestroyed
}

// EqualProjectedRelease checks the immutable ordinary release snapshot and tenant/instance.
func EqualProjectedRelease(a, b *deploy.Release) bool {
	return a != nil && b != nil && a.ID == b.ID && a.TenantID == b.TenantID && a.InstanceID == b.InstanceID && a.Version == b.Version && reflect.DeepEqual(a.Services, b.Services)
}

// ReserveProjectionOwner verifies the permanent origin when a replacement reuses an owner.
func ReserveProjectionOwner(ctx context.Context, tx ControllerTransaction, target managed.Identity, previous *managed.Identity, ordinaryOwnerExists bool) error {
	owner, err := managed.ProjectionOwner(target)
	if err != nil {
		return err
	}

	if owner.IsNil() {
		return nil
	}

	saved, readErr := controllerRead[managed.OwnerReservation](ctx, tx, "owner", "", owner.String())
	if readErr == nil {
		if previous == nil || saved.Origin.TenantID != target.TenantID {
			return managed.ErrConflict
		}

		oldOwner, err := managed.ProjectionOwner(*previous)
		if err != nil {
			return err
		}

		if oldOwner != owner {
			return managed.ErrAuthority
		}

		origin, err := tx.Read(ctx, saved.Origin)
		if err != nil {
			return err
		}

		originOwner, err := managed.ProjectionOwner(origin.Identity)
		if err != nil {
			return err
		}

		if originOwner != owner {
			return managed.ErrAuthority
		}

		return nil
	}

	if !controllerAbsent(readErr) {
		return readErr
	}

	if ordinaryOwnerExists {
		return managed.ErrConflict
	}

	return controllerWrite(ctx, tx, "owner", "", owner.String(), managed.OwnerReservation{OwnerID: owner, Origin: target.Key}, true, managed.MaxControllerRecord)
}

// ProjectionOwnerOrigin reads provenance from retained authority, never mutable labels.
func ProjectionOwnerOrigin(ctx context.Context, tx ControllerTransaction, target managed.Identity) (managed.Identity, error) {
	owner, err := managed.ProjectionOwner(target)
	if err != nil {
		return managed.Identity{}, err
	}

	if owner.IsNil() {
		return target, nil
	}

	saved, err := controllerRead[managed.OwnerReservation](ctx, tx, "owner", "", owner.String())
	if err != nil {
		return managed.Identity{}, err
	}

	if saved.Origin.TenantID != target.TenantID {
		return managed.Identity{}, managed.ErrAuthority
	}

	origin, err := tx.Read(ctx, saved.Origin)
	if err != nil {
		return managed.Identity{}, err
	}

	originOwner, err := managed.ProjectionOwner(origin.Identity)
	if err != nil {
		return managed.Identity{}, err
	}

	if saved.OwnerID != owner || originOwner != owner {
		return managed.Identity{}, managed.ErrAuthority
	}

	if origin.Key != target.Key {
		projection, err := controllerRead[managed.ProjectionReceipt](ctx, tx, "target_projection", origin.TenantID, origin.InstanceID.String())
		if err != nil {
			return managed.Identity{}, err
		}

		operation, err := acceptedOperation(ctx, tx, managed.OperationKey{Key: origin.Key, ActionID: projection.ActionID})
		if err != nil {
			return managed.Identity{}, err
		}

		savedProjection, err := controllerRead[managed.ProjectionReceipt](ctx, tx, "projection", origin.TenantID, projection.ActionID.String())
		if err != nil {
			return managed.Identity{}, err
		}

		if !reflect.DeepEqual(projection, savedProjection) {
			return managed.Identity{}, managed.ErrConflict
		}

		if err = projectionAssociations(ctx, tx, origin.Identity, operation, projection); err != nil {
			return managed.Identity{}, err
		}
	}

	return origin.Identity, nil
}

func validPortCommand(c *managed.PortCommand) bool { return c == nil || c.Validate() == nil }

// AdvanceManagedOperation persists explicit bounded observations under the parent lease.
func (r *Repository) AdvanceManagedOperation(ctx context.Context, q managed.OperationAdvance) (managed.OperationState, error) {
	var out managed.OperationState

	o := q.Observation
	switch o.Kind {
	case "pending", "retry", "blocked", "remote_receipt", "completed":
	default:
		return out, managed.ErrInvalid
	}

	if !validPortCommand(o.Command) || len(o.Evidence) > managed.MaxEvidence || o.Diagnostic != "" && !token(o.Diagnostic) || o.ObservedAt.IsZero() {
		return out, managed.ErrInvalid
	}

	fingerprint, err := controllerFingerprint(q)
	if err != nil {
		return out, err
	}

	err = r.controller(ctx, "action:"+q.ActionID.String(), []managed.Key{q.Key}, func(tx ControllerTransaction) error {
		type history struct {
			Digest string                 `json:"digest"`
			State  managed.OperationState `json:"state"`
		}

		saved, readErr := controllerRead[history](ctx, tx, "operation_history", q.TenantID, q.ID.String())
		if readErr == nil {
			if saved.Digest != fingerprint {
				return managed.ErrConflict
			}

			out = saved.State

			return nil
		}

		if !controllerAbsent(readErr) {
			return readErr
		}

		if _, err := controllerParent(ctx, tx, q.Command); err != nil {
			return err
		}

		state, err := acceptedOperation(ctx, tx, managed.OperationKey{Key: q.Key, ActionID: q.ActionID})
		if err != nil {
			return err
		}

		if state.Revision != q.ExpectedOperationRevision {
			return managed.ErrConflict
		}

		if state.Revision >= 1<<63-1 {
			return managed.ErrCapacity
		}

		if o.ObservedAt.After(tx.Now()) {
			return managed.ErrInvalid
		}

		state.Revision++

		state.Observation = o
		if err = controllerWrite(ctx, tx, "operation_history", q.TenantID, q.ID.String(), history{fingerprint, state}, true, managed.MaxControllerRecord); err != nil {
			return err
		}

		if err = controllerWrite(ctx, tx, "operation", q.TenantID, q.ActionID.String(), state, false, managed.MaxControllerRecord); err != nil {
			return err
		}

		out = state

		return nil
	})

	return out, err
}

func controllerKey(q managed.ControllerKey) (string, error) {
	if !token(q.Name) || q.Scope.Kind == "denial" {
		return "", managed.ErrInvalid
	}

	if _, err := controllerScope(q.Installation, q.Authority, q.Scope); err != nil {
		return "", err
	}

	return controllerFingerprint(q)
}

// ReadManagedContinuation recovers the exact persisted sweep without advancing it.
func (r *Repository) ReadManagedContinuation(ctx context.Context, q managed.ControllerKey) (managed.Continuation, error) {
	var out managed.Continuation

	scope, err := controllerKey(q)
	if err != nil {
		return out, err
	}

	err = r.controller(ctx, "cursor:"+scope, nil, func(tx ControllerTransaction) error {
		var err error

		out, err = controllerRead[managed.Continuation](ctx, tx, "continuation", scope, "current")

		return err
	})

	return out, err
}

// AdvanceManagedContinuation retains a bounded pending page and independent in-flight slot.
func (r *Repository) AdvanceManagedContinuation(ctx context.Context, q managed.ContinuationAdvance) (managed.Continuation, error) {
	var out managed.Continuation

	scope, err := controllerKey(q.Key)
	if err != nil {
		return out, err
	}

	if q.ID.Prefix() != managed.PrefixOperation || !token(q.Owner) || q.Duration <= 0 || q.Duration > managed.MaxLease || len(q.Cursor) > managed.MaxControllerCursor || len(q.Pending) > managed.MaxControllerPage {
		return out, managed.ErrInvalid
	}

	seen := map[managed.Key]bool{}

	entries := append([]managed.Key{}, q.Pending...)
	if q.InFlight != nil {
		entries = append(entries, *q.InFlight)
	}

	for _, entry := range entries {
		if !key(entry) || seen[entry] || q.Key.Scope.Kind == "tenant" && entry.TenantID != q.Key.Scope.TenantID {
			return out, managed.ErrInvalid
		}

		seen[entry] = true
	}

	if q.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(q.Cursor)

		var cursor []string
		if err != nil || json.Unmarshal(raw, &cursor) != nil || len(cursor) != 2 || cursor[0] != q.Key.Scope.TenantID {
			return out, managed.ErrInvalid
		}

		if _, err = id.ParseWithPrefix(cursor[1], id.PrefixInstance); err != nil {
			return out, managed.ErrInvalid
		}
	}

	fingerprint, err := controllerFingerprint(q)
	if err != nil {
		return out, err
	}

	err = r.controller(ctx, "cursor:"+scope, nil, func(tx ControllerTransaction) error {
		type history struct {
			Digest string               `json:"digest"`
			State  managed.Continuation `json:"state"`
		}

		saved, readErr := controllerRead[history](ctx, tx, "continuation_history", scope, q.ID.String())
		if readErr == nil {
			if saved.Digest != fingerprint {
				return managed.ErrConflict
			}

			out = saved.State

			return nil
		}

		if !controllerAbsent(readErr) {
			return readErr
		}

		current, readErr := controllerRead[managed.Continuation](ctx, tx, "continuation", scope, "current")
		if readErr != nil && !controllerAbsent(readErr) {
			return readErr
		}

		if current.Revision != q.ExpectedRevision || current.LeaseEpoch != q.LeaseEpoch || current.LeaseOwner != "" && current.LeaseOwner != q.Owner && tx.Now().Before(current.LeaseUntil) {
			return managed.ErrConflict
		}

		if current.Revision >= 1<<63-1 || current.LeaseEpoch >= 1<<63-1 {
			return managed.ErrCapacity
		}

		out = managed.Continuation{Key: q.Key, Revision: current.Revision + 1, LeaseEpoch: current.LeaseEpoch + 1, LeaseOwner: q.Owner, LeaseUntil: tx.Now().Add(q.Duration), Cursor: q.Cursor, Pending: q.Pending, InFlight: q.InFlight}
		if err := controllerWrite(ctx, tx, "continuation_history", scope, q.ID.String(), history{fingerprint, out}, true, managed.MaxContinuation); err != nil {
			return err
		}

		return controllerWrite(ctx, tx, "continuation", scope, "current", out, controllerAbsent(readErr), managed.MaxContinuation)
	})

	return out, err
}

// ListManagedAuditIntents enumerates pending intents with a scope-bound stable cursor.
func (r *Repository) ListManagedAuditIntents(ctx context.Context, q managed.AuditPageRequest) (managed.AuditPage, error) {
	out := managed.AuditPage{Items: []managed.AuditIntent{}}

	scope, err := controllerScope(q.Installation, q.Authority, q.Scope)
	if err != nil {
		return out, err
	}

	if q.Limit < 0 || q.Limit > managed.MaxControllerPage || len(q.Cursor) > managed.MaxControllerCursor {
		return out, managed.ErrInvalid
	}

	if q.Limit == 0 {
		q.Limit = managed.MaxControllerPage
	}

	after := ""

	if q.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(q.Cursor)

		var cursor []string
		if err != nil || json.Unmarshal(raw, &cursor) != nil || len(cursor) != 2 || cursor[0] != scope {
			return out, managed.ErrInvalid
		}

		if _, err = id.ParseWithPrefix(cursor[1], managed.PrefixOperation); err != nil {
			return out, managed.ErrInvalid
		}

		after = cursor[1]
	}

	err = r.controller(ctx, "audit:"+scope, nil, func(tx ControllerTransaction) error {
		// Bound scanned rows as well as returned rows. A delivered page may be empty
		// with a continuation, so acknowledged history cannot starve later intents.
		rows, err := tx.ListRecords(ctx, "audit", scope, after, q.Limit+1)
		if err != nil {
			return err
		}

		more := len(rows) > q.Limit
		if more {
			rows = rows[:q.Limit]
		}

		for _, row := range rows {
			if _, err = tx.ReadRecord(ctx, "audit_ack", scope, row.Key); err == nil {
				continue
			} else if !controllerAbsent(err) {
				return err
			}

			var intent managed.AuditIntent
			if json.Unmarshal(row.Data, &intent) != nil {
				return managed.ErrUnavailable
			}

			out.Items = append(out.Items, intent)
		}

		if more {
			raw, err := json.Marshal([2]string{scope, rows[len(rows)-1].Key})
			if err != nil {
				return err
			}

			out.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
		}

		return nil
	})

	return out, err
}

// AcknowledgeManagedAudit accepts only exact intent-bound complete sink receipts.
func (r *Repository) AcknowledgeManagedAudit(ctx context.Context, q managed.AuditAcknowledgment) error {
	scope, err := controllerScope(q.Intent.Installation, q.Intent.Authority, q.Intent.Scope)
	if err != nil {
		return err
	}

	receipt := q.Receipt

	bound, err := controllerFingerprint(q.Intent)
	if err != nil {
		return err
	}

	if receipt.IntentID != q.Intent.ID || receipt.IntentDigest != bound || receipt.MappingVersion != q.Intent.MappingVersion || receipt.SourceKey != q.Intent.SourceKey || receipt.SourceDigest != q.Intent.SourceDigest || receipt.Installation != q.Intent.Installation || receipt.Authority != q.Intent.Authority || receipt.ApplicationID != q.Intent.ApplicationID || receipt.OrganizationID != q.Intent.OrganizationID || !digest(receipt.NormalizedFingerprint) || !token(receipt.SinkReceiptID) || !evidence(receipt.Receipt) || receipt.AcceptedAt.IsZero() {
		return managed.ErrInvalid
	}

	return r.controller(ctx, "audit:"+scope, nil, func(tx ControllerTransaction) error {
		intent, err := controllerRead[managed.AuditIntent](ctx, tx, "audit", scope, q.Intent.ID.String())
		if err != nil {
			return err
		}

		if !reflect.DeepEqual(intent, q.Intent) {
			return managed.ErrConflict
		}

		old, err := controllerRead[managed.AuditSinkReceipt](ctx, tx, "audit_ack", scope, q.Intent.ID.String())
		if err == nil {
			if !reflect.DeepEqual(old, receipt) {
				return managed.ErrConflict
			}

			return nil
		}

		if !controllerAbsent(err) {
			return err
		}

		return controllerWrite(ctx, tx, "audit_ack", scope, q.Intent.ID.String(), receipt, true, managed.MaxAuditIntent)
	})
}

func providerStepID(k managed.ProviderStepKey) (string, error) {
	if !key(k.Key) || k.ActionID.Prefix() != managed.PrefixOperation || k.ProviderOperationID.Prefix() != managed.PrefixOperation {
		return "", managed.ErrInvalid
	}

	return controllerFingerprint(struct {
		Key       managed.Key              `json:"key"`
		Operation id.ID                    `json:"operation"`
		Kind      managed.ProviderStepKind `json:"kind"`
		Service   string                   `json:"service"`
	}{k.Key, k.ProviderOperationID, k.Kind, k.Service})
}

func expectedSteps(target *managed.Target, action id.ID, removal bool) ([]managed.ProviderStepKey, error) {
	spec, err := managed.ParseProvisionSpec(target.Manifest)
	if err != nil {
		return nil, err
	}

	operation := target.ProvisionOperationID
	if removal {
		if target.Deletion == nil {
			return nil, managed.ErrBlocked
		}

		operation = target.Deletion.OperationID
	}

	steps := []managed.ProviderStepKey{}
	add := func(kind managed.ProviderStepKind, service string) {
		steps = append(steps, managed.ProviderStepKey{OperationKey: managed.OperationKey{Key: target.Key, ActionID: action}, ProviderOperationID: operation, Kind: kind, Service: service})
	}

	if removal {
		if len(target.Resources) != len(spec.Services)+1 || !token(target.Resources["network"]) {
			return nil, managed.ErrBlocked
		}

		for _, s := range spec.Services {
			if !token(target.Resources[s.Name]) {
				return nil, managed.ErrBlocked
			}

			add(managed.StepRemoveContainer, s.Name)
		}

		add(managed.StepRemoveNetwork, "")
	} else {
		add(managed.StepCreateNetwork, "")

		for _, s := range spec.Services {
			add(managed.StepCreateContainer, s.Name)
		}

		for _, s := range spec.Services {
			add(managed.StepStartContainer, s.Name)
		}
	}

	return steps, nil
}

// AuthorizeManagedProviderStep persists may-have-issued only after complete parent validation.
// A recovered permission is inspect-only; a provider must never resend it after replay.
func (r *Repository) AuthorizeManagedProviderStep(ctx context.Context, q managed.ProviderStepAuthorization) (managed.ProviderStepGrant, error) {
	var out managed.ProviderStepPermission

	newlyIssued := false

	stepID, err := providerStepID(q.Step)
	if err != nil {
		return managed.ProviderStepGrant{Permission: out, NewlyIssued: newlyIssued}, err
	}

	if q.Key != q.Step.Key {
		return managed.ProviderStepGrant{}, managed.ErrAuthority
	}

	fingerprint, err := controllerFingerprint(q)
	if err != nil {
		return managed.ProviderStepGrant{Permission: out, NewlyIssued: newlyIssued}, err
	}

	err = r.controller(ctx, "step:"+stepID, []managed.Key{q.Key}, func(tx ControllerTransaction) error {
		existing, readErr := controllerRead[managed.ProviderStepRecord](ctx, tx, "step", q.TenantID, stepID)
		if readErr == nil {
			replayedDigest, digestErr := providerPermissionDigest(q, existing.Permission.SpecDigest, existing.Permission.Physical)
			if digestErr != nil {
				return digestErr
			}

			if existing.Permission.Digest != replayedDigest {
				return managed.ErrConflict
			}

			out = existing.Permission

			return nil
		}

		if !controllerAbsent(readErr) {
			return readErr
		}

		target, err := controllerParent(ctx, tx, q.Command)
		if err != nil {
			return err
		}

		operation, err := acceptedOperation(ctx, tx, q.Step.OperationKey)
		if err != nil {
			return err
		}

		projectionAction := q.Step.ActionID
		if operation.Receipt.ReleaseID.IsNil() {
			// A desired-state action retains its original immutable projection identity.
			original, err := controllerRead[managed.ProjectionReceipt](ctx, tx, "target_projection", q.TenantID, q.InstanceID.String())
			if err != nil {
				return err
			}

			projectionAction = original.ActionID
		}

		projection, err := controllerRead[managed.ProjectionReceipt](ctx, tx, "projection", q.TenantID, projectionAction.String())
		if err != nil {
			return err
		}

		projected, err := acceptedOperation(ctx, tx, managed.OperationKey{Key: q.Key, ActionID: projectionAction})
		if err != nil {
			return err
		}

		if projection.Target != q.Key || projection.SpecDigest != target.Manifest.SpecDigest {
			return managed.ErrAuthority
		}

		binding, err := controllerRead[managed.ReleaseBinding](ctx, tx, "release", q.TenantID, projected.Receipt.ReleaseID.String())
		if err != nil {
			return err
		}

		if binding.Target != q.Key || binding.ActionID != projectionAction || !reflect.DeepEqual(binding.Manifest, target.Manifest) {
			return managed.ErrAuthority
		}

		if err = tx.ProjectRows(ctx, target.Identity, projected.Receipt, true); err != nil {
			return err
		}

		removal := q.Step.Kind == managed.StepRemoveContainer || q.Step.Kind == managed.StepRemoveNetwork

		steps, err := expectedSteps(target, q.Step.ActionID, removal)
		if err != nil {
			return err
		}

		index := -1

		for n, step := range steps {
			if step == q.Step {
				index = n

				break
			}
		}

		if index < 0 {
			return managed.ErrInvalid
		}

		if index > 0 {
			previousID, err := providerStepID(steps[index-1])
			if err != nil {
				return err
			}

			previous, err := controllerRead[managed.ProviderStepRecord](ctx, tx, "step", q.TenantID, previousID)
			if err != nil {
				return err
			}

			if previous.Result == nil || previous.Result.Outcome != managed.PortTerminal {
				return managed.ErrBlocked
			}
		}

		if removal {
			if target.Deletion == nil || target.Deletion.OperationID != q.Step.ProviderOperationID || target.Deletion.Settlement != nil || target.Seal == 0 {
				return managed.ErrBlocked
			}

			fence, err := closure(target, tx.Now())
			if err != nil {
				return err
			}

			if fence != target.Deletion.FenceDigest {
				return managed.ErrConflict
			}

			if target.Phase != managed.Admitted && target.Phase != managed.DeleteIssued {
				return managed.ErrBlocked
			}
		} else if target.Phase != managed.Open || target.Desired != managed.Active || target.Provision.Revoked || target.Provision.Complete || q.Step.ProviderOperationID != target.ProvisionOperationID {
			return managed.ErrBlocked
		}

		var receipt managed.Receipt

		repo := New(tx)

		if index == 0 {
			if removal {
				receipt, err = repo.IssueManagedDeletion(ctx, q.Command)
			} else {
				receipt, err = repo.IssueManagedProvision(ctx, q.Command)
			}
		} else {
			receipt, err = repo.apply(ctx, q.Command, "provider_step", q, true, func(*managed.Target, time.Time) error { return nil })
		}

		if err != nil {
			return err
		}

		resource := ""

		if removal {
			slot := q.Step.Service
			if slot == "" {
				slot = "network"
			}

			resource = target.Resources[slot]
		}

		if q.Step.Kind == managed.StepStartContainer {
			create := q.Step
			create.Kind = managed.StepCreateContainer

			createID, err := providerStepID(create)
			if err != nil {
				return err
			}

			created, err := controllerRead[managed.ProviderStepRecord](ctx, tx, "step", q.TenantID, createID)
			if err != nil {
				return err
			}

			if created.Result == nil || created.Result.Outcome != managed.PortTerminal || !token(created.Result.ResourceID) {
				return managed.ErrBlocked
			}

			resource = created.Result.ResourceID
		}

		fingerprint, err = providerPermissionDigest(q, target.Manifest.SpecDigest, target.Physical)
		if err != nil {
			return err
		}

		out = managed.ProviderStepPermission{CreateProfile: "ctrlplane.managed.create.v1", Step: q.Step, CommandID: q.ID, Digest: fingerprint, SpecDigest: target.Manifest.SpecDigest, Physical: target.Physical, Seal: target.Seal, ResourceID: resource, IssuedAt: tx.Now(), Revision: receipt.Revision}
		if removal {
			out.FenceDigest = target.Deletion.FenceDigest
		}

		if err = controllerWrite(ctx, tx, "step", q.TenantID, stepID, managed.ProviderStepRecord{Permission: out}, true, managed.MaxProviderStep); err != nil {
			return err
		}

		newlyIssued = true

		return nil
	})
	if err != nil {
		return managed.ProviderStepGrant{}, err
	}

	return managed.ProviderStepGrant{Permission: out, NewlyIssued: newlyIssued}, nil
}

// RecordManagedProviderStepResult retains late exact results without granting another send.
func (r *Repository) RecordManagedProviderStepResult(ctx context.Context, q managed.ProviderStepResult) error {
	stepID, err := providerStepID(q.Permission.Step)
	if err != nil {
		return err
	}

	if q.Outcome != managed.PortTerminal && q.Outcome != managed.PortUnavailable {
		return managed.ErrInvalid
	}

	if !evidence(q.Evidence) || q.ObservedAt.IsZero() || q.ObservedAt.Before(q.Permission.IssuedAt) || q.Diagnostic != "" && !token(q.Diagnostic) || q.ResourceID != "" && !token(q.ResourceID) {
		return managed.ErrInvalid
	}

	return r.controller(ctx, "step:"+stepID, []managed.Key{q.Permission.Step.Key}, func(tx ControllerTransaction) error {
		record, err := controllerRead[managed.ProviderStepRecord](ctx, tx, "step", q.Permission.Step.TenantID, stepID)
		if err != nil {
			return err
		}

		if !reflect.DeepEqual(record.Permission, q.Permission) {
			return managed.ErrConflict
		}

		if q.ObservedAt.After(tx.Now()) {
			return managed.ErrInvalid
		}

		if record.Result != nil && record.Result.Outcome == managed.PortTerminal {
			if !reflect.DeepEqual(*record.Result, q) {
				return managed.ErrConflict
			}

			return nil
		}

		if q.Outcome == managed.PortTerminal {
			if !token(q.ResourceID) {
				return managed.ErrInvalid
			}

			if q.Permission.ResourceID != "" && q.ResourceID != q.Permission.ResourceID {
				return managed.ErrAuthority
			}
		}
		// Retain each uncertainty observation separately; replacing the current view
		// cannot discard original command, receipt or retry evidence.
		observationID, err := controllerFingerprint(q)
		if err != nil {
			return err
		}

		if _, readErr := tx.ReadRecord(ctx, "step_history", q.Permission.Step.TenantID, observationID); controllerAbsent(readErr) {
			if err = controllerWrite(ctx, tx, "step_history", q.Permission.Step.TenantID, observationID, q, true, managed.MaxProviderStep); err != nil {
				return err
			}
		} else if readErr != nil {
			return readErr
		}

		record.Result = &q

		return controllerWrite(ctx, tx, "step", q.Permission.Step.TenantID, stepID, record, false, managed.MaxProviderStep)
	})
}

// ReadManagedProviderStep returns the original permission and retained current result.
func (r *Repository) ReadManagedProviderStep(ctx context.Context, q managed.ProviderStepKey) (managed.ProviderStepRecord, error) {
	var out managed.ProviderStepRecord

	stepID, err := providerStepID(q)
	if err != nil {
		return out, err
	}

	err = r.controller(ctx, "step:"+stepID, []managed.Key{q.Key}, func(tx ControllerTransaction) error {
		var err error

		out, err = controllerRead[managed.ProviderStepRecord](ctx, tx, "step", q.TenantID, stepID)

		return err
	})

	return out, err
}

var _ managed.ControllerStore = (*Repository)(nil)

func providerPermissionDigest(q managed.ProviderStepAuthorization, spec string, physical managed.Physical) (string, error) {
	return controllerFingerprint(struct {
		Request       managed.ProviderStepAuthorization `json:"request"`
		SpecDigest    string                            `json:"spec_digest"`
		Physical      managed.Physical                  `json:"physical"`
		CreateProfile string                            `json:"create_profile"`
	}{q, spec, physical, "ctrlplane.managed.create.v1"})
}

package managedstate

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"

	"github.com/xraph/ctrlplane/managed"
)

// DiscoveryWorkRow reports the stored size independently of the bounded payload.
type DiscoveryWorkRow struct {
	Key  string
	Size int
	Data []byte
}

// DiscoveryPageTransaction is a bounded indexed read on the existing held transaction.
// It is not a second authority/backend and cannot issue or settle work.
type DiscoveryPageTransaction interface {
	ListDiscoveryWork(ctx context.Context, scope string, cursor string, limit int) ([]DiscoveryWorkRow, error)
}

type workCursor struct {
	Schema  string          `json:"schema"`
	Scope   string          `json:"scope"`
	After   managed.Decimal `json:"after"`
	Ceiling managed.Decimal `json:"ceiling"`
}

func workOrdinal(n uint64) string                 { return fmt.Sprintf("%020d", n) }
func workLookup(ref managed.WorkReference) string { return ref.Kind + ":" + ref.ID.String() }

func readWorkHeadHeld(ctx context.Context, tx ControllerTransaction, k managed.Key) (managed.WorkHead, error) {
	scope, err := originalScope(k)
	if err != nil {
		return managed.WorkHead{}, err
	}

	raw, err := tx.ReadRecord(ctx, "work_head", scope, "current")
	if err != nil {
		return managed.WorkHead{}, err
	}

	head, err := managed.DecodeWorkHead(raw)
	if err != nil || head.Key != k {
		return managed.WorkHead{}, managed.ErrUnavailable
	}

	return head, nil
}

func validateWorkHeld(ctx context.Context, tx ControllerTransaction, ref managed.WorkReference) error {
	return validateWorkLinkHeld(ctx, tx, ref, true)
}

func validateWorkLinkHeld(ctx context.Context, tx ControllerTransaction, ref managed.WorkReference, full bool) error {
	if err := ref.Validate(); err != nil {
		return err
	}

	scope, err := originalScope(ref.Key)
	if err != nil {
		return err
	}

	ordinal, _ := ref.Ordinal.Uint64()

	raw, err := tx.ReadRecord(ctx, "work_index", scope, workOrdinal(ordinal))
	if err != nil {
		return err
	}

	stored, err := managed.DecodeWorkReference(raw)
	if err != nil || stored != ref {
		return managed.ErrUnavailable
	}

	lookup, err := tx.ReadRecord(ctx, "work_lookup", scope, workLookup(ref))
	if err != nil {
		return err
	}

	if !bytes.Equal(lookup, raw) {
		return managed.ErrUnavailable
	}

	operation, err := acceptedOperationBase(ctx, tx, ref.Operation)
	if err != nil {
		return err
	}

	if full {
		operation, err = acceptedOperation(ctx, tx, ref.Operation)
		if err != nil {
			return err
		}
	}

	fingerprint, err := managed.ActionDigest(operation.Submission)
	if operation.Submission.Kind == managed.ActionContinue {
		operationScope, scopeErr := attemptScope(ref.Operation.Key)
		if scopeErr != nil {
			return scopeErr
		}

		material, materialErr := attemptRead[managed.ContinuationMaterial](ctx, tx, "removal_continuation", operationScope, ref.Operation.ActionID.String(), managed.MaxRemovalContinuationMaterial)
		if materialErr != nil {
			return materialErr
		}

		if !reflect.DeepEqual(material.Request.Action, operation.Submission) {
			return managed.ErrUnavailable
		}

		fingerprint, err = managed.RemovalContinuationDigest(material.Request)
	}

	if err != nil || operation.Submission.ID != ref.Operation.ActionID || operation.Receipt.ID != ref.Operation.ActionID || operation.Receipt.CanonicalDigest != fingerprint || operation.Submission.CanonicalDigest != fingerprint || operation.Receipt.Key != operation.Submission.Key {
		return managed.ErrUnavailable
	}

	action := operation.Submission

	access := managed.AccessAuditRequest{AttemptID: action.ID, Installation: action.Installation, Authority: action.Authority, ApplicationID: action.ApplicationID, OrganizationID: action.OrganizationID, Actor: action.Actor, Scope: managed.AuditScope{Kind: "tenant", TenantID: operation.Receipt.Target.TenantID}, Action: "managed." + string(action.Kind), ResourceKind: "instance", ResourceID: operation.Receipt.Target.InstanceID.String(), Outcome: "allowed", Reason: "authorized", OccurredAt: action.OccurredAt}
	if action.Kind == managed.ActionContinue {
		access = continuationAccess(action)
	}

	auditScope, auditErr := controllerScope(access.Installation, access.Authority, access.Scope)
	if auditErr != nil {
		return auditErr
	}

	intent, auditErr := controllerRead[managed.AuditIntent](ctx, tx, "audit", auditScope, operation.Receipt.AuditID.String())
	if auditErr != nil {
		return auditErr
	}

	if operation.Receipt.AuditID != action.ID || !reflect.DeepEqual(intent, intentFromAccess(access, fingerprint, operation.Receipt.AcceptedAt)) {
		return managed.ErrUnavailable
	}

	if ref.Key != operation.Receipt.Target && ref.Key != operation.Receipt.Key {
		return managed.ErrAuthority
	}

	if ref.Kind == "action" {
		if ref.Digest != fingerprint {
			return managed.ErrUnavailable
		}

		return nil
	}

	return validatePhaseWorkHeld(ctx, tx, ref)
}

// discoveryAcceptAction is called only inside the already validated action+audit transaction.
// Initial head creation is limited to a newly accepted target; missing legacy links refuse.
func discoveryAcceptAction(ctx context.Context, tx ControllerTransaction, operation managed.OperationState) error {
	q, receipt := operation.Submission, operation.Receipt

	keys := []managed.Key{receipt.Target}
	if receipt.Key != receipt.Target {
		keys = append(keys, receipt.Key)
	}

	for _, k := range keys {
		head, err := readWorkHeadHeld(ctx, tx, k)

		initial := errors.Is(err, managed.ErrNotFound)
		if err != nil && !initial {
			return err
		}

		if initial {
			if q.NewTarget == nil || q.NewTarget.Key != k || receipt.Target != k || (q.Kind != managed.ActionDeploy && q.Kind != managed.ActionRollback) {
				return managed.ErrUnavailable
			}

			head = managed.WorkHead{Schema: managed.DiscoverySchema, Key: k, LastOrdinal: "0"}
		} else {
			if err = validateWorkLinkHeld(ctx, tx, head.Provisioning, false); err != nil {
				return err
			}

			if err = validateWorkLinkHeld(ctx, tx, head.Desired, false); err != nil {
				return err
			}
		}

		ref := managed.WorkReference{Schema: managed.DiscoverySchema, Key: k, Operation: operation.Key, Kind: "action", ID: q.ID, Digest: receipt.CanonicalDigest}
		if _, err = appendWorkHeld(ctx, tx, &head, ref, initial, true); err != nil {
			return err
		}
	}

	return nil
}

func appendWorkHeld(ctx context.Context, tx ControllerTransaction, head *managed.WorkHead, ref managed.WorkReference, initial, desired bool) (managed.WorkReference, error) {
	n, err := head.LastOrdinal.Uint64()
	if err != nil || n >= math.MaxInt64 {
		return managed.WorkReference{}, managed.ErrCapacity
	}

	ref.Ordinal = managed.Decimal(strconv.FormatUint(n+1, 10))

	raw, err := managed.EncodeWorkReference(ref)
	if err != nil {
		return managed.WorkReference{}, err
	}

	scope, err := originalScope(head.Key)
	if err != nil {
		return managed.WorkReference{}, err
	}

	if err = tx.WriteRecord(ctx, "work_index", scope, workOrdinal(n+1), raw, true); err != nil {
		return managed.WorkReference{}, err
	}

	if err = tx.WriteRecord(ctx, "work_lookup", scope, workLookup(ref), raw, true); err != nil {
		return managed.WorkReference{}, err
	}

	head.LastOrdinal = ref.Ordinal
	if initial {
		head.Provisioning = ref
	}

	if desired {
		head.Desired = ref
	}

	body, err := managed.EncodeWorkHead(*head)
	if err != nil {
		return managed.WorkReference{}, err
	}

	if err = tx.WriteRecord(ctx, "work_head", scope, "current", body, initial); err != nil {
		return managed.WorkReference{}, err
	}

	return ref, nil
}

func discoveryActionReplay(ctx context.Context, tx ControllerTransaction, receipt managed.ActionReceipt) error {
	keys := []managed.Key{receipt.Target}
	if receipt.Key != receipt.Target {
		keys = append(keys, receipt.Key)
	}

	for _, k := range keys {
		scope, err := originalScope(k)
		if err != nil {
			return err
		}

		raw, err := tx.ReadRecord(ctx, "work_lookup", scope, "action:"+receipt.ID.String())
		if err != nil {
			return err
		}

		ref, err := managed.DecodeWorkReference(raw)
		if err != nil || ref.Key != k || ref.Operation.Key != receipt.Target || ref.Operation.ActionID != receipt.ID || ref.Kind != "action" || ref.Digest != receipt.CanonicalDigest {
			return managed.ErrUnavailable
		}

		if err = validateWorkHeld(ctx, tx, ref); err != nil {
			return err
		}
	}

	return nil
}

// ReadManagedWorkHead resolves both desired/provisioning references against retained acceptance.
func (r *Repository) ReadManagedWorkHead(ctx context.Context, k managed.Key) (managed.WorkHead, error) {
	var out managed.WorkHead
	if !key(k) {
		return out, managed.ErrInvalid
	}

	err := r.controller(ctx, "work:"+k.InstanceID.String(), []managed.Key{k}, func(tx ControllerTransaction) error {
		var err error

		out, err = readWorkHeadHeld(ctx, tx, k)
		if err != nil {
			return err
		}

		if err = validateWorkHeld(ctx, tx, out.Provisioning); err != nil {
			return err
		}

		return validateWorkHeld(ctx, tx, out.Desired)
	})
	if err != nil {
		return managed.WorkHead{}, err
	}

	return out, nil
}

// ReadManagedWork validates the exact immutable index and acceptance/audit associations.
func (r *Repository) ReadManagedWork(ctx context.Context, ref managed.WorkReference) (managed.WorkReference, error) {
	if err := ref.Validate(); err != nil {
		return managed.WorkReference{}, err
	}

	err := r.controller(ctx, "work:"+ref.Key.InstanceID.String(), []managed.Key{ref.Key, ref.Operation.Key}, func(tx ControllerTransaction) error { return validateWorkHeld(ctx, tx, ref) })
	if err != nil {
		return managed.WorkReference{}, err
	}

	return ref, nil
}

// ListManagedUnresolvedWork uses stable authority ordinals with a frozen window ceiling.
// Every item is retained until an independently reviewed closure transition exists.
// Completion of a window proves neither physical closure nor absence of later acceptance.
func (r *Repository) ListManagedUnresolvedWork(ctx context.Context, q managed.WorkPageRequest) (managed.WorkPage, error) {
	out := managed.WorkPage{Schema: managed.DiscoverySchema, Key: q.Key, Items: []managed.WorkReference{}}
	if !key(q.Key) || q.Limit < 1 || q.Limit > managed.MaxWorkPageItems || len(q.Cursor) > managed.MaxWorkCursor {
		return managed.WorkPage{}, managed.ErrInvalid
	}

	err := r.controller(ctx, "work:"+q.Key.InstanceID.String(), []managed.Key{q.Key}, func(tx ControllerTransaction) error {
		head, err := readWorkHeadHeld(ctx, tx, q.Key)
		if err != nil {
			return err
		}

		if err = validateWorkHeld(ctx, tx, head.Provisioning); err != nil {
			return err
		}

		if err = validateWorkHeld(ctx, tx, head.Desired); err != nil {
			return err
		}

		scope, err := originalScope(q.Key)
		if err != nil {
			return err
		}

		cursor := workCursor{Schema: managed.DiscoverySchema, Scope: scope, After: "0", Ceiling: head.LastOrdinal}

		if q.Cursor != "" {
			raw, decodeErr := base64.RawURLEncoding.DecodeString(q.Cursor)
			if decodeErr != nil {
				return managed.ErrInvalid
			}

			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.DisallowUnknownFields()

			if err = decoder.Decode(&cursor); err != nil {
				return managed.ErrInvalid
			}

			canonical, encodeErr := json.Marshal(cursor)
			if encodeErr != nil || !bytes.Equal(raw, canonical) || base64.RawURLEncoding.EncodeToString(raw) != q.Cursor || cursor.Schema != managed.DiscoverySchema || cursor.Scope != scope {
				return managed.ErrInvalid
			}
		}

		after, e1 := cursor.After.Uint64()
		ceiling, e2 := cursor.Ceiling.Uint64()

		last, e3 := head.LastOrdinal.Uint64()
		if e1 != nil || e2 != nil || e3 != nil || after >= ceiling || ceiling > last || last > math.MaxInt64 {
			return managed.ErrInvalid
		}

		pager, ok := tx.(DiscoveryPageTransaction)
		if !ok {
			return managed.ErrUnsupported
		}

		rows, err := pager.ListDiscoveryWork(ctx, scope, workOrdinal(after), q.Limit+1)
		if err != nil {
			return err
		}

		if len(rows) > q.Limit+1 {
			return managed.ErrUnavailable
		}

		for _, row := range rows {
			if row.Size < 1 || row.Size > managed.MaxWorkReference || len(row.Data) != row.Size {
				return managed.ErrUnavailable
			}

			ref, decodeErr := managed.DecodeWorkReference(row.Data)
			if decodeErr != nil {
				return managed.ErrUnavailable
			}

			n, _ := ref.Ordinal.Uint64()
			if n > ceiling {
				break
			}

			if n != after+1 || row.Key != workOrdinal(n) || ref.Key != q.Key {
				return managed.ErrUnavailable
			}

			if len(out.Items) == q.Limit {
				break
			}

			if err = validateWorkHeld(ctx, tx, ref); err != nil {
				return err
			}

			out.Items = append(out.Items, ref)
			after = n
		}

		if after < ceiling {
			if len(out.Items) != q.Limit {
				return managed.ErrUnavailable
			}

			cursor.After = managed.Decimal(strconv.FormatUint(after, 10))

			raw, encodeErr := json.Marshal(cursor)
			if encodeErr != nil {
				return encodeErr
			}

			out.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
		}

		_, err = managed.EncodeWorkPage(out)

		return err
	})
	if err != nil {
		return managed.WorkPage{}, err
	}

	return out, nil
}

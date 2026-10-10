// Package managedstate shares transition rules between the qualified stores.
package managedstate

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
)

// Backend executes callbacks against an isolated clone under the physical-instance lock.
// A failed callback or persistence write must commit no changes.
type Backend interface {
	Create(ctx context.Context, target *managed.Target) (*managed.Target, error)
	Read(ctx context.Context, key managed.Key) (*managed.Target, error)
	List(ctx context.Context, request managed.PageRequest, after string) ([]managed.Summary, error)
	Obligations(ctx context.Context, tenant string) (bool, error)
	Atomic(ctx context.Context, key managed.Key, operation id.ID, digest string, change func(*managed.Target, time.Time) (managed.Receipt, error)) (managed.Receipt, error)
}

// Repository exposes only typed consumer transitions through its store methods.
type Repository struct{ backend Backend }

// New binds the typed repository to one persistence authority.
func New(b Backend) *Repository { return &Repository{backend: b} }

var _ managed.Store = (*Repository)(nil)

// Clone makes a defensive copy and bounds serialized storage.
func Clone(t *managed.Target) (*managed.Target, error) {
	b, err := json.Marshal(t)
	if err != nil {
		return nil, err
	}

	if len(b) > managed.MaxDocument {
		return nil, managed.ErrCapacity
	}

	var cloned managed.Target
	if err = json.Unmarshal(b, &cloned); err != nil {
		return nil, err
	}

	return &cloned, nil
}

// PhysicalKey reserves a project permanently across all target generations.
func PhysicalKey(p managed.Physical) string {
	return managed.Digest([]byte(p.Provider + "\x00" + p.Placement + "\x00" + p.Project))
}

// EqualIdentity supports exact idempotent target creation.
func EqualIdentity(a, b managed.Identity) bool { return reflect.DeepEqual(a, b) }

func token(s string) bool {
	if len(s) == 0 || len(s) > 256 {
		return false
	}

	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && !strings.ContainsRune("-_.:/@+=", c) {
			return false
		}
	}

	return true
}
func evidence(b []byte) bool { return len(b) > 0 && len(b) <= managed.MaxEvidence }
func digest(s string) bool {
	if len(s) != 64 {
		return false
	}

	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}

	return true
}
func key(k managed.Key) bool { return token(k.TenantID) && k.InstanceID.Prefix() == id.PrefixInstance }
func identity(i managed.Identity) error {
	if i.ProvisionOperationID.Prefix() != managed.PrefixOperation || !key(i.Key) || !token(i.Physical.Provider) || !token(i.Physical.Placement) || !token(i.Physical.Project) || !token(i.Physical.Generation) || i.Manifest.Version == 0 || !evidence(i.Manifest.Spec) || managed.Digest(i.Manifest.Spec) != i.Manifest.SpecDigest || len(i.Manifest.Members) == 0 || len(i.Manifest.Members) > managed.MaxMembers {
		return managed.ErrInvalid
	}

	seen := map[string]bool{}
	bindings := map[string]bool{}

	for _, m := range i.Manifest.Members {
		if !token(m.Name) || !token(m.Service) || !token(m.Installation) || !token(m.Namespace) || !token(m.Queue) || !token(m.Build) || !digest(m.ArtifactDigest) || !digest(m.ConfigurationDigest) || seen[m.Name] {
			return managed.ErrInvalid
		}

		b := strings.Join([]string{m.Installation, m.Namespace, m.Queue, m.Build, m.Service}, "\x00")
		if bindings[b] {
			return managed.ErrInvalid
		}

		seen[m.Name] = true
		bindings[b] = true
	}

	return nil
}

// CreateManagedTarget persists immutable membership before any external operation.
func (r *Repository) CreateManagedTarget(ctx context.Context, i managed.Identity) (*managed.Target, error) {
	if err := identity(i); err != nil {
		return nil, err
	}

	t, err := Clone(&managed.Target{Identity: i, Revision: 1, Desired: managed.Active, DesiredRevision: 1, Phase: managed.Open, Reservations: []managed.Reservation{}})
	if err != nil {
		return nil, err
	}

	return r.backend.Create(ctx, t)
}

// ReadManagedTarget reads the authoritative complete record.
func (r *Repository) ReadManagedTarget(ctx context.Context, k managed.Key) (*managed.Target, error) {
	if !key(k) {
		return nil, managed.ErrInvalid
	}

	return r.backend.Read(ctx, k)
}

// InspectManagedProtection never treats a read error as absence.
func (r *Repository) InspectManagedProtection(ctx context.Context, k managed.Key) (managed.Protection, error) {
	t, err := r.ReadManagedTarget(ctx, k)
	if errors.Is(err, managed.ErrNotFound) {
		return managed.Protection{}, nil
	}

	if err != nil {
		return managed.Protection{}, fmt.Errorf("%w: %w", managed.ErrUnavailable, err)
	}

	return managed.Protection{Protected: true, Phase: t.Phase, Revision: t.Revision}, nil
}

// HasManagedObligations checks independently of tenant metadata and ordinary instance rows.
func (r *Repository) HasManagedObligations(ctx context.Context, tenant string) (bool, error) {
	if !token(tenant) {
		return false, managed.ErrInvalid
	}

	return r.backend.Obligations(ctx, tenant)
}

// ListManagedTargets binds continuation to tenant scope and uses permanent instance ordering.
func (r *Repository) ListManagedTargets(ctx context.Context, q managed.PageRequest) (managed.Page, error) {
	if !token(q.TenantID) {
		return managed.Page{}, managed.ErrInvalid
	}

	return r.list(ctx, q)
}

// ListManagedControllerTargets enumerates orphaned targets without relying on tenant metadata.
func (r *Repository) ListManagedControllerTargets(ctx context.Context, q managed.ControllerPageRequest) (managed.Page, error) {
	return r.list(ctx, managed.PageRequest{Cursor: q.Cursor, Limit: q.Limit})
}

func (r *Repository) list(ctx context.Context, q managed.PageRequest) (managed.Page, error) {
	if q.Limit < 0 || q.Limit > managed.MaxPage {
		return managed.Page{}, managed.ErrInvalid
	}

	if q.Limit == 0 {
		q.Limit = managed.DefaultPage
	}

	after := ""

	if q.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(q.Cursor)
		if err != nil || len(raw) > 512 {
			return managed.Page{}, managed.ErrInvalid
		}

		var c [2]string
		if json.Unmarshal(raw, &c) != nil || c[0] != q.TenantID {
			return managed.Page{}, managed.ErrInvalid
		}

		parsed, err := id.ParseWithPrefix(c[1], id.PrefixInstance)
		if err != nil {
			return managed.Page{}, managed.ErrInvalid
		}

		after = parsed.String()
	}

	rows, err := r.backend.List(ctx, q, after)
	if err != nil {
		return managed.Page{}, err
	}

	out := managed.Page{Items: rows}
	if len(rows) > q.Limit {
		out.Items = rows[:q.Limit]

		b, err := json.Marshal([2]string{q.TenantID, out.Items[len(out.Items)-1].InstanceID.String()})
		if err != nil {
			return managed.Page{}, err
		}

		out.NextCursor = base64.RawURLEncoding.EncodeToString(b)
	}

	return out, nil
}

func (r *Repository) apply(ctx context.Context, c managed.Command, kind string, input any, lease bool, fn func(*managed.Target, time.Time) error) (managed.Receipt, error) {
	if !key(c.Key) || c.ID.Prefix() != managed.PrefixOperation || !token(c.Owner) || c.Revision == 0 {
		return managed.Receipt{}, managed.ErrInvalid
	}

	b, err := json.Marshal(struct {
		Kind  string `json:"kind"`
		Input any    `json:"input"`
	}{kind, input})
	if err != nil {
		return managed.Receipt{}, err
	}

	if len(b) > managed.MaxDocument {
		return managed.Receipt{}, managed.ErrCapacity
	}

	fingerprint := managed.Digest(b)

	return r.backend.Atomic(ctx, c.Key, c.ID, fingerprint, func(t *managed.Target, now time.Time) (managed.Receipt, error) {
		if t.Revision != c.Revision || t.LeaseEpoch != c.LeaseEpoch || (lease && (t.LeaseOwner != c.Owner || !now.Before(t.LeaseUntil))) {
			return managed.Receipt{}, managed.ErrConflict
		}

		if t.Phase == managed.Deleted {
			return managed.Receipt{}, managed.ErrBlocked
		}

		if t.Revision >= 1<<63-1 {
			return managed.Receipt{}, managed.ErrCapacity
		}

		if err := fn(t, now); err != nil {
			return managed.Receipt{}, err
		}

		t.Revision++

		return managed.Receipt{ID: c.ID, Digest: fingerprint, Revision: t.Revision, LeaseEpoch: t.LeaseEpoch, AcceptedAt: now.UTC(), LeaseUntil: t.LeaseUntil}, nil
	})
}

// ClaimManagedLease fences stale local writers; it never settles outstanding calls.
func (r *Repository) ClaimManagedLease(ctx context.Context, q managed.LeaseRequest) (managed.Receipt, error) {
	return r.apply(ctx, q.Command, "lease", q, false, func(t *managed.Target, now time.Time) error {
		if q.Duration <= 0 || q.Duration > managed.MaxLease {
			return managed.ErrInvalid
		}

		if t.LeaseOwner != "" && t.LeaseOwner != q.Owner && now.Before(t.LeaseUntil) {
			return managed.ErrConflict
		}

		if t.LeaseEpoch >= 1<<63-1 {
			return managed.ErrCapacity
		}

		t.LeaseOwner = q.Owner
		t.LeaseEpoch++
		t.LeaseUntil = now.Add(q.Duration)

		return nil
	})
}

// SetManagedDesired preserves lifecycle obligations when an operator changes intent.
func (r *Repository) SetManagedDesired(ctx context.Context, q managed.DesiredRequest) (managed.Receipt, error) {
	return r.apply(ctx, q.Command, "desired", q, true, func(t *managed.Target, _ time.Time) error {
		if q.Mode != managed.Active && q.Mode != managed.Paused && q.Mode != managed.Retiring {
			return managed.ErrInvalid
		}

		t.Desired = q.Mode
		t.DesiredRevision++

		return nil
	})
}

// IssueManagedProvision records potential provider creation before the call.
func (r *Repository) IssueManagedProvision(ctx context.Context, q managed.Command) (managed.Receipt, error) {
	return r.apply(ctx, q, "issue_provision", q, true, func(t *managed.Target, _ time.Time) error {
		if t.Phase != managed.Open || t.Provision.Issued || t.Provision.Revoked {
			return managed.ErrBlocked
		}

		t.Provision.Issued = true

		return nil
	})
}

// BindManagedResources records exact resources only after the creation operation has settled.
// A lost acknowledgment remains issued; sealing cannot discard that late result.
func (r *Repository) BindManagedResources(ctx context.Context, q managed.ResourcesRequest) (managed.Receipt, error) {
	return r.apply(ctx, q.Command, "resources", q, true, func(t *managed.Target, _ time.Time) error {
		if (t.Phase != managed.Open && t.Phase != managed.Sealed) || !t.Provision.Issued || t.Provision.Revoked {
			return managed.ErrBlocked
		}

		if q.OperationID != t.ProvisionOperationID || !evidence(q.Receipt) || len(q.Resources) == 0 || len(q.Resources) > managed.MaxMembers+1 {
			return managed.ErrInvalid
		}

		for k, v := range q.Resources {
			if !token(k) || !token(v) {
				return managed.ErrInvalid
			}
		}

		if t.Provision.Complete && (!reflect.DeepEqual(t.Resources, q.Resources) || !reflect.DeepEqual(t.Provision.Receipt, q.Receipt)) {
			return managed.ErrConflict
		}

		t.Resources = q.Resources
		t.Provision.Complete = true
		t.Provision.Receipt = q.Receipt

		return nil
	})
}

// AllocateManagedReplacement persists a distinct identity before provisioning.
func (r *Repository) AllocateManagedReplacement(ctx context.Context, q managed.ReplacementRequest) (managed.Receipt, error) {
	return r.apply(ctx, q.Command, "replacement", q, true, func(t *managed.Target, _ time.Time) error {
		if err := identity(q.Replacement); err != nil {
			return err
		}

		if q.Replacement.TenantID != t.TenantID || q.Replacement.InstanceID == t.InstanceID || PhysicalKey(q.Replacement.Physical) == PhysicalKey(t.Physical) {
			return managed.ErrAuthority
		}

		if t.Desired != managed.Active {
			return managed.ErrBlocked
		}

		if t.Replacement != nil && !EqualIdentity(*t.Replacement, q.Replacement) {
			return managed.ErrConflict
		}

		if t.Replacement != nil {
			return nil
		}

		t.Replacement = &q.Replacement
		t.ReplacementOperationID = q.ID

		return nil
	})
}

// ReserveManagedRegistration accounts for each incarnation before permission to issue.
func (r *Repository) ReserveManagedRegistration(ctx context.Context, q managed.ReserveRequest) (managed.Receipt, error) {
	return r.apply(ctx, q.Command, "reserve", q, true, func(t *managed.Target, _ time.Time) error {
		v := q.Registration
		if v.ID.Prefix() != managed.PrefixReservation || !token(v.Member) || !token(v.RuntimeID) || !token(v.InstanceID) || !token(v.HostID) || !token(v.RequestID) || !evidence(v.Command) || managed.Digest(v.Command) != v.CommandDigest {
			return managed.ErrInvalid
		}

		for _, old := range t.Reservations {
			if old.ID == v.ID {
				if reflect.DeepEqual(old.Registration, v) {
					return nil
				}

				return managed.ErrConflict
			}

			if old.RequestID == v.RequestID || (old.RuntimeID == v.RuntimeID && old.Member == v.Member) {
				return managed.ErrConflict
			}

			if old.InstanceID != v.InstanceID || old.RuntimeID == v.RuntimeID && old.HostID != v.HostID {
				return managed.ErrAuthority
			}
		}

		if t.Phase != managed.Open || !t.Provision.Complete {
			return managed.ErrBlocked
		}

		if len(t.Reservations) >= managed.MaxReservations {
			return managed.ErrCapacity
		}

		found := false

		for _, m := range t.Manifest.Members {
			if m.Name == v.Member {
				found = true
			}
		}

		if !found {
			return managed.ErrAuthority
		}

		t.Reservations = append(t.Reservations, managed.Reservation{Registration: v, Phase: managed.Reserved})

		return nil
	})
}
func reservation(t *managed.Target, i id.ID) (*managed.Reservation, error) {
	for n := range t.Reservations {
		if t.Reservations[n].ID == i {
			return &t.Reservations[n], nil
		}
	}

	return nil, managed.ErrNotFound
}

// IssueManagedRegistration commits potential issuance before a caller may send.
func (r *Repository) IssueManagedRegistration(ctx context.Context, q managed.ReservationRequest) (managed.Receipt, error) {
	return r.apply(ctx, q.Command, "issue_registration", q, true, func(t *managed.Target, _ time.Time) error {
		v, err := reservation(t, q.ReservationID)
		if err != nil {
			return err
		}

		if t.Phase != managed.Open || v.Phase != managed.Reserved {
			return managed.ErrBlocked
		}

		v.Phase = managed.MayHaveIssued

		return nil
	})
}

// ObserveManagedUnknown retains the issued command without manufacturing refusal.
func (r *Repository) ObserveManagedUnknown(ctx context.Context, q managed.ReservationRequest) (managed.Receipt, error) {
	return r.apply(ctx, q.Command, "unknown", q, true, func(t *managed.Target, _ time.Time) error {
		v, err := reservation(t, q.ReservationID)
		if err != nil {
			return err
		}

		if v.Phase != managed.MayHaveIssued {
			return managed.ErrBlocked
		}

		v.Unknown = true

		return nil
	})
}

// AcceptManagedRegistration records an exact accepted receipt even after sealing.
func (r *Repository) AcceptManagedRegistration(ctx context.Context, q managed.AcceptanceRequest) (managed.Receipt, error) {
	return r.apply(ctx, q.Command, "accept", q, true, func(t *managed.Target, _ time.Time) error {
		v, err := reservation(t, q.ReservationID)
		if err != nil {
			return err
		}

		if !evidence(q.Receipt) {
			return managed.ErrInvalid
		}

		if v.Phase == managed.Accepted {
			if reflect.DeepEqual(v.Receipt, q.Receipt) {
				return nil
			}

			return managed.ErrConflict
		}

		if v.Phase != managed.MayHaveIssued {
			return managed.ErrBlocked
		}

		v.Phase = managed.Accepted
		v.Receipt = q.Receipt
		v.Unknown = false

		return nil
	})
}

// SealManagedTarget closes new reservations and atomically revokes unissued commands.
func (r *Repository) SealManagedTarget(ctx context.Context, q managed.Command) (managed.Receipt, error) {
	return r.apply(ctx, q, "seal", q, true, func(t *managed.Target, _ time.Time) error {
		if t.Phase != managed.Open {
			return managed.ErrBlocked
		}

		t.Phase = managed.Sealed

		t.Seal++
		if !t.Provision.Issued {
			t.Provision.Revoked = true
		}

		for n := range t.Reservations {
			if t.Reservations[n].Phase == managed.Reserved {
				t.Reservations[n].Phase = managed.Revoked
			}
		}

		return nil
	})
}

// RecordManagedFence freezes trusted query-retention evidence for one accepted binding.
func (r *Repository) RecordManagedFence(ctx context.Context, q managed.FenceRequest) (managed.Receipt, error) {
	return r.apply(ctx, q.Command, "fence", q, true, func(t *managed.Target, now time.Time) error {
		v, err := reservation(t, q.ReservationID)
		if err != nil {
			return err
		}

		f := q.Fence

		if t.Phase != managed.Sealed || v.Phase != managed.Accepted || v.Abort != nil {
			return managed.ErrBlocked
		}

		if !evidence(f.CompatibilityEvidence) || f.Epoch == 0 || !evidence(f.Evidence) || managed.Digest(f.Evidence) != f.Digest || !f.ValidUntil.After(now) || len(f.Survivors) > managed.MaxReservations {
			return managed.ErrInvalid
		}

		for _, s := range f.Survivors {
			if !token(s) {
				return managed.ErrInvalid
			}

			for _, other := range t.Reservations {
				if s == other.InstanceID {
					return managed.ErrAuthority
				}
			}
		}

		if v.Fence != nil && !reflect.DeepEqual(*v.Fence, f) {
			return managed.ErrConflict
		}

		v.Fence = &f

		return nil
	})
}

// RecordManagedDrain retains the original deadline and one immutable terminal result.
func (r *Repository) RecordManagedDrain(ctx context.Context, q managed.DrainRequest) (managed.Receipt, error) {
	return r.apply(ctx, q.Command, "drain", q, true, func(t *managed.Target, now time.Time) error {
		v, err := reservation(t, q.ReservationID)
		if err != nil {
			return err
		}

		d := q.Drain

		if v.Phase != managed.Accepted || v.Abort != nil || t.Phase == managed.DeleteIssued {
			return managed.ErrBlocked
		}

		if !token(d.OperationID) || d.Deadline.IsZero() || !evidence(d.Evidence) || d.Quiescent && !d.Complete || d.Complete && (d.CompletedAt.IsZero() || d.CompletedAt.After(now)) || !d.Complete && !d.CompletedAt.IsZero() {
			return managed.ErrInvalid
		}

		for n := range t.Reservations {
			other := &t.Reservations[n]
			if other.RuntimeID != v.RuntimeID {
				continue
			}

			if old := other.Drain; old != nil {
				if old.OperationID != d.OperationID || !old.Deadline.Equal(d.Deadline) {
					return managed.ErrConflict
				}

				if old.Complete && !reflect.DeepEqual(*old, d) {
					return managed.ErrConflict
				}
			} else if !d.Deadline.After(now) {
				return managed.ErrBlocked
			}
		}

		for n := range t.Reservations {
			if t.Reservations[n].RuntimeID == v.RuntimeID {
				cloned := d
				t.Reservations[n].Drain = &cloned
			}
		}

		return nil
	})
}
func closure(t *managed.Target, now time.Time) (string, error) {
	if !t.Provision.Complete && !t.Provision.Revoked {
		return "", managed.ErrBlocked
	}

	type bindingFence struct {
		ID    id.ID         `json:"id"`
		Fence managed.Fence `json:"fence"`
	}

	fences := []bindingFence{}

	for _, v := range t.Reservations {
		if v.Phase == managed.Revoked {
			continue
		}

		if v.Phase != managed.Accepted || v.Fence == nil || !v.Fence.ValidUntil.After(now) || v.Drain == nil || !v.Drain.Complete || !v.Drain.Quiescent || v.Abort != nil {
			return "", managed.ErrBlocked
		}

		fences = append(fences, bindingFence{v.ID, *v.Fence})
	}

	b, err := json.Marshal(fences)
	if err != nil {
		return "", err
	}

	return managed.Digest(b), nil
}

// AdmitManagedRemoval checks the entire frozen set under the parent lock.
func (r *Repository) AdmitManagedRemoval(ctx context.Context, q managed.AdmissionRequest) (managed.Receipt, error) {
	return r.apply(ctx, q.Command, "admit", q, true, func(t *managed.Target, now time.Time) error {
		if t.Phase != managed.Sealed || t.Deletion != nil {
			return managed.ErrBlocked
		}

		if q.OperationID.Prefix() != managed.PrefixOperation || !token(q.Verifier) {
			return managed.ErrInvalid
		}

		f, err := closure(t, now)
		if err != nil {
			return err
		}

		t.Deletion = &managed.Deletion{OperationID: q.OperationID, FenceDigest: f, Verifier: q.Verifier}
		t.Phase = managed.Admitted

		return nil
	})
}

// IssueManagedDeletion records potential issue; callers must also recheck remote current fences.
func (r *Repository) IssueManagedDeletion(ctx context.Context, q managed.Command) (managed.Receipt, error) {
	return r.apply(ctx, q, "issue_delete", q, true, func(t *managed.Target, now time.Time) error {
		if t.Phase != managed.Admitted || t.Deletion == nil || t.Deletion.Issued || t.Deletion.Settlement != nil {
			return managed.ErrBlocked
		}

		f, err := closure(t, now)
		if err != nil {
			return err
		}

		if f != t.Deletion.FenceDigest {
			return managed.ErrConflict
		}

		t.Deletion.Issued = true
		t.Deletion.IssuedAt = now
		t.Phase = managed.DeleteIssued

		return nil
	})
}

// RevokeManagedDeletion atomically prevents any later issuance of an unissued operation.
func (r *Repository) RevokeManagedDeletion(ctx context.Context, q managed.Command) (managed.Receipt, error) {
	return r.apply(ctx, q, "revoke_delete", q, true, func(t *managed.Target, now time.Time) error {
		if t.Phase != managed.Admitted || t.Deletion == nil || t.Deletion.Issued || t.Deletion.Settlement != nil {
			return managed.ErrBlocked
		}

		d := t.Deletion
		d.Settlement = &managed.Settlement{OperationID: d.OperationID, FenceDigest: d.FenceDigest, Disposition: managed.NotIssued, Verifier: d.Verifier, Evidence: []byte(q.ID.String()), SettledAt: now}
		t.Phase = managed.Sealed

		return nil
	})
}

// SettleManagedDeletion accepts only definitive evidence bound to this exact operation.
func (r *Repository) SettleManagedDeletion(ctx context.Context, q managed.SettlementRequest) (managed.Receipt, error) {
	return r.apply(ctx, q.Command, "settle", q, true, func(t *managed.Target, now time.Time) error {
		d := t.Deletion
		s := q.Settlement

		if t.Phase != managed.DeleteIssued || d == nil || !d.Issued {
			return managed.ErrBlocked
		}

		if s.OperationID != d.OperationID || s.FenceDigest != d.FenceDigest || s.Verifier != d.Verifier || s.SettledAt.IsZero() || s.SettledAt.Before(d.IssuedAt) || s.SettledAt.After(now) || !evidence(s.Evidence) {
			return managed.ErrInvalid
		}

		switch s.Disposition {
		case managed.Rejected, managed.Cancelled, managed.Fenced, managed.SettledWithoutDeletion, managed.Removed:
		default:
			return managed.ErrInvalid
		}

		if d.Settlement != nil && !reflect.DeepEqual(*d.Settlement, s) {
			return managed.ErrConflict
		}

		d.Settlement = &s

		return nil
	})
}

// AbortManagedBinding records fresh post-settlement proof; enrollment stays sealed.
func (r *Repository) AbortManagedBinding(ctx context.Context, q managed.AbortRequest) (managed.Receipt, error) {
	return r.apply(ctx, q.Command, "abort", q, true, func(t *managed.Target, now time.Time) error {
		v, err := reservation(t, q.ReservationID)
		if err != nil {
			return err
		}

		d := t.Deletion
		p := q.Proof

		if d == nil || d.Settlement == nil || d.Settlement.Disposition == managed.Removed || v.Phase != managed.Accepted || v.Fence == nil || len(v.Finished) > 0 {
			return managed.ErrBlocked
		}

		if p.FenceDigest != v.Fence.Digest || p.VerifiedAt.Before(d.Settlement.SettledAt) || p.VerifiedAt.After(now) || !p.ValidUntil.After(now) || !p.ValidUntil.After(p.VerifiedAt) || !evidence(p.Evidence) || !evidence(p.Receipt) {
			return managed.ErrInvalid
		}

		if v.Abort != nil && !reflect.DeepEqual(*v.Abort, p) {
			return managed.ErrConflict
		}

		v.Abort = &p
		t.Phase = managed.Sealed

		return nil
	})
}

// FinishManagedBinding saves the accepted removal receipt after definitive provider deletion.
func (r *Repository) FinishManagedBinding(ctx context.Context, q managed.AcceptanceRequest) (managed.Receipt, error) {
	return r.apply(ctx, q.Command, "finish", q, true, func(t *managed.Target, _ time.Time) error {
		v, err := reservation(t, q.ReservationID)
		if err != nil {
			return err
		}

		if t.Deletion == nil || t.Deletion.Settlement == nil || t.Deletion.Settlement.Disposition != managed.Removed || v.Phase != managed.Accepted || v.Fence == nil || v.Abort != nil {
			return managed.ErrBlocked
		}

		if !evidence(q.Receipt) {
			return managed.ErrInvalid
		}

		if len(v.Finished) > 0 && !reflect.DeepEqual(v.Finished, q.Receipt) {
			return managed.ErrConflict
		}

		v.Finished = q.Receipt

		return nil
	})
}

// TombstoneManagedTarget retains immutable ownership after all removal bookkeeping completes.
func (r *Repository) TombstoneManagedTarget(ctx context.Context, q managed.Command) (managed.Receipt, error) {
	return r.apply(ctx, q, "tombstone", q, true, func(t *managed.Target, _ time.Time) error {
		if t.Phase != managed.DeleteIssued || t.Deletion == nil || t.Deletion.Settlement == nil || t.Deletion.Settlement.Disposition != managed.Removed {
			return managed.ErrBlocked
		}

		for _, v := range t.Reservations {
			if v.Phase != managed.Revoked && len(v.Finished) == 0 {
				return managed.ErrBlocked
			}
		}

		t.Phase = managed.Deleted
		t.TerminalDisposition = managed.Removed

		return nil
	})
}

// TombstoneManagedUnissuedTarget closes only locally revoked work with no external obligations.
func (r *Repository) TombstoneManagedUnissuedTarget(ctx context.Context, q managed.Command) (managed.Receipt, error) {
	return r.apply(ctx, q, "tombstone_unissued", q, true, func(t *managed.Target, _ time.Time) error {
		if t.Phase != managed.Sealed || !t.Provision.Revoked || t.Provision.Issued || t.Provision.Complete || len(t.Provision.Receipt) != 0 || len(t.Resources) != 0 || t.Deletion != nil {
			return managed.ErrBlocked
		}

		for _, v := range t.Reservations {
			if v.Phase != managed.Revoked || len(v.Receipt) != 0 || v.Unknown || v.Fence != nil || v.Drain != nil || len(v.Finished) != 0 || v.Abort != nil {
				return managed.ErrBlocked
			}
		}

		t.Phase = managed.Deleted
		t.TerminalDisposition = managed.NoResourcesCreated

		return nil
	})
}

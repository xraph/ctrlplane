package managedstate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"slices"
	"strconv"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
)

const attemptScopeDomain = "ctrlplane.managed.attempt-scope.v1"
const attemptDescriptorDomain = "ctrlplane.managed.removal-attempt.v1"
const attemptFactDomain = "ctrlplane.managed.attempt-fact.v1"
const attemptCommandDomain = "ctrlplane.managed.attempt-command.v1"
const attemptCoverageDomain = "ctrlplane.managed.attempt-coverage.v1"
const attemptFailureDomain = "ctrlplane.managed.provider-step-failure.v1"

type attemptRoot struct {
	SchemaVersion        managed.Decimal      `json:"schema_version"`
	Key                  managed.Key          `json:"key"`
	AttemptRef           managed.AttemptRef   `json:"attempt_ref"`
	OriginalOperationKey managed.OperationKey `json:"original_operation_key"`
	InitialOperationID   id.ID                `json:"initial_operation_id"`
	AcceptedAt           time.Time            `json:"accepted_at"`
}
type attemptCurrent struct {
	SchemaVersion   managed.Decimal    `json:"schema_version"`
	Key             managed.Key        `json:"key"`
	AttemptRef      managed.AttemptRef `json:"attempt_ref"`
	PointerRevision managed.Decimal    `json:"pointer_revision"`
}
type attemptIdentity struct {
	SchemaVersion managed.Decimal    `json:"schema_version"`
	ID            id.ID              `json:"id"`
	Role          string             `json:"role"`
	Key           managed.Key        `json:"key"`
	AttemptRef    managed.AttemptRef `json:"attempt_ref"`
}
type attemptEvent struct {
	SchemaVersion managed.Decimal    `json:"schema_version"`
	AttemptRef    managed.AttemptRef `json:"attempt_ref"`
	Kind          string             `json:"kind"`
	RequestDigest string             `json:"request_digest"`
	FactKind      string             `json:"fact_kind"`
	FactKey       string             `json:"fact_key"`
	FactDigest    string             `json:"fact_digest"`
	Receipt       managed.Receipt    `json:"receipt"`
}
type attemptView struct {
	recoveryActivation       *managed.RecoveryAssociation
	recoveryPendingOriginals []string
	lineageRoot              *attemptView
	validated                bool
	originals                map[id.ID]managed.AttemptBindingReference
	commands                 map[managed.AttemptCommandKey]managed.AttemptCommandRecord
	fences                   map[managed.AttemptBindingKey]managed.AttemptFenceRecord
	scope                    string
	descriptor               managed.AttemptDescriptor
	state                    managed.AttemptState
	ref                      managed.AttemptRef
}

func attemptHash(domain string, value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}

	return managed.Digest(append([]byte(domain+"\n"), raw...)), nil
}
func attemptFact(kind string, value any) (string, error) {
	switch local := value.(type) {
	case managed.ProviderStepPermission:
		value = managed.ProviderPermissionWire(local)
	case managed.ProviderStepResult:
		value = managed.ProviderStepWire(managed.ProviderStepRecord{Permission: local.Permission, Result: &local}).Result
	}

	return attemptHash(attemptFactDomain, struct {
		Kind  string `json:"kind"`
		Value any    `json:"value"`
	}{kind, value})
}
func attemptScope(k managed.Key) (string, error) {
	if !key(k) {
		return "", managed.ErrInvalid
	}

	return attemptHash(attemptScopeDomain, struct {
		Key managed.Key `json:"key"`
	}{k})
}
func attemptSlot(k managed.AttemptBindingKey, kind string) (string, error) {
	return attemptFact("slot", struct {
		Key  managed.AttemptBindingKey `json:"key"`
		Kind string                    `json:"kind"`
	}{k, kind})
}
func attemptRead[T any](ctx context.Context, tx ControllerTransaction, kind, scope, k string, limit int) (T, error) {
	var out T

	raw, err := tx.ReadRecord(ctx, kind, scope, k)
	if err != nil {
		return out, err
	}

	return attemptDecode[T](raw, limit)
}

func attemptDecode[T any](raw []byte, limit int) (T, error) {
	var out T
	if len(raw) > limit {
		return out, managed.ErrCapacity
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()

	if err := dec.Decode(&out); err != nil {
		return out, managed.ErrUnavailable
	}

	canonical, err := json.Marshal(out)
	if err != nil {
		return out, err
	}

	if !bytes.Equal(raw, canonical) {
		return out, managed.ErrUnavailable
	}

	return out, nil
}
func attemptWrite(ctx context.Context, tx ControllerTransaction, kind, scope, k string, v any, insert bool, limit int) error {
	return controllerWrite(ctx, tx, kind, scope, k, v, insert, limit)
}
func attemptVersion(k managed.AttemptKey) error {
	generation, err := k.Generation.Uint64()
	if err != nil || generation == 0 || generation > math.MaxInt64 {
		return managed.ErrUnsupported
	}

	if !key(k.Key) || k.AttemptID.Prefix() != managed.PrefixOperation {
		return managed.ErrInvalid
	}

	return nil
}
func validAttemptCommand(c managed.Command) bool {
	return key(c.Key) && c.ID.Prefix() == managed.PrefixOperation && token(c.Owner) && c.Revision > 0 && c.Revision <= math.MaxInt64 && c.LeaseEpoch <= math.MaxInt64
}
func attemptTime(t time.Time) bool {
	return !t.IsZero() && t.Year() >= 1 && t.Year() <= 9999 && t.Location() == time.UTC
}
func fixedAttemptProfile(t *managed.Target) bool {
	var envelope struct {
		Profile string `json:"profile"`
	}
	if json.Unmarshal(t.Manifest.Spec, &envelope) != nil {
		return bytes.Contains(t.Manifest.Spec, []byte("ctrlplane.managed."))
	}

	return envelope.Profile == "ctrlplane.managed.v1"
}
func attemptKey(v attemptView) managed.AttemptKey {
	return managed.AttemptKey{Key: v.descriptor.Key, AttemptID: v.ref.AttemptID, Generation: v.ref.Generation}
}
func attemptLocal(q managed.AttemptRequest) managed.AttemptKey {
	return managed.AttemptKey{Key: q.Key, AttemptID: q.AttemptID, Generation: q.Generation}
}
func attemptBinding(v attemptView, rid id.ID) managed.AttemptBindingKey {
	return managed.AttemptBindingKey{AttemptKey: attemptKey(v), ReservationID: rid}
}
func attemptOriginal(ctx context.Context, tx ControllerTransaction, t *managed.Target, rid id.ID) (managed.AttemptBindingReference, error) {
	var out managed.AttemptBindingReference

	v, err := reservation(t, rid)
	if err != nil {
		return out, err
	}

	ref, err := managed.ParseOriginalReference(v.Receipt)
	if err != nil {
		return out, managed.ErrBlocked
	}

	record, _, err := readOriginal(ctx, tx, managed.OriginalLifecycleKey{Key: t.Key, Kind: managed.OriginalRegistration, OperationID: ref.OperationID}, t)
	if err != nil {
		return out, err
	}

	if record.Material.Registration == nil {
		return out, managed.ErrUnavailable
	}

	process, err := attemptFact("original_process", record.Material.Registration.Process)
	if err != nil {
		return out, err
	}

	return managed.AttemptBindingReference{ReservationID: rid, ReservationVersion: record.Material.Registration.BindingVersion, OriginalRegistrationOperationID: ref.OperationID, OriginalRegistrationDigest: ref.MaterialDigest, ProcessDigest: process}, nil
}
func attemptSets(ctx context.Context, tx ControllerTransaction, t *managed.Target, originals map[id.ID]managed.AttemptBindingReference) ([]id.ID, string, string, error) {
	type entry struct {
		Registration managed.Registration             `json:"registration"`
		Original     *managed.AttemptBindingReference `json:"original_registration_reference,omitempty"`
	}

	entries := []entry{}
	ids := []id.ID{}

	if len(t.Reservations) > managed.MaxReservations || !t.Provision.Complete || !t.Provision.Issued || t.Provision.Revoked {
		return nil, "", "", managed.ErrBlocked
	}

	for _, r := range t.Reservations {
		e := entry{Registration: r.Registration}
		switch r.Phase {
		case managed.Accepted:
			ref, err := attemptOriginal(ctx, tx, t, r.ID)
			if err != nil {
				return nil, "", "", err
			}

			e.Original = &ref
			if originals != nil {
				originals[r.ID] = ref
			}
		case managed.Revoked:
			if r.Unknown || len(r.Receipt) > 0 {
				return nil, "", "", managed.ErrBlocked
			}
		default:
			return nil, "", "", managed.ErrBlocked
		}

		entries = append(entries, e)
		ids = append(ids, r.ID)
	}

	registrations, err := attemptHash("ctrlplane.managed.attempt-registration-set.v1", entries)
	if err != nil {
		return nil, "", "", err
	}

	var spec managed.ProvisionSpec
	if checked, ok := tx.(*attemptCheckedReads); ok {
		spec, _, err = checked.parsedManifest(ctx, t)
	} else {
		spec, err = managed.ParseProvisionSpec(t.Manifest)
	}

	if err != nil {
		return nil, "", "", err
	}

	type resource struct {
		Service    string `json:"service"`
		ResourceID string `json:"resource_id"`
	}

	resources := []resource{}

	if len(t.Resources) != len(spec.Services)+1 {
		return nil, "", "", managed.ErrBlocked
	}

	for _, s := range spec.Services {
		if !token(t.Resources[s.Name]) {
			return nil, "", "", managed.ErrBlocked
		}

		resources = append(resources, resource{s.Name, t.Resources[s.Name]})
	}

	if !token(t.Resources["network"]) {
		return nil, "", "", managed.ErrBlocked
	}

	resources = append(resources, resource{"", t.Resources["network"]})
	rd, err := attemptHash("ctrlplane.managed.attempt-resources.v1", struct {
		Resources     []resource `json:"resources"`
		Operation     id.ID      `json:"provision_operation_id"`
		ReceiptDigest string     `json:"receipt_digest"`
	}{resources, t.ProvisionOperationID, managed.Digest(t.Provision.Receipt)})

	return ids, registrations, rd, err
}

// attemptCheckedReads lives only during one readAttempt validation. It retains
// successful immutable reads and exact native own-slot batch absence. Provider
// rows use separate held transport; missing original/event/receipt dependencies
// still reach the backend.
// The view is discarded before the caller performs any write or fresh callback.
type attemptCheckedReads struct {
	ControllerTransaction

	recoveryInspection bool

	heldRows            map[[3]string][]byte
	heldMissing         map[[3]string]bool
	rows                map[[3]string][]byte
	missing             map[[3]string]bool
	receipts            map[attemptReceiptKey]managed.Receipt
	events              map[attemptImmutableValueKey]attemptEvent
	originals           map[attemptImmutableValueKey]managed.OriginalLifecycleRecord
	originalBytes       int
	currentBindings     map[attemptImmutableValueKey]attemptParsedCurrentBinding
	currentBindingBytes int
	factDigests         map[attemptImmutableValueKey]string
	manifestIdentity    *managed.Identity
	manifestSpec        managed.ProvisionSpec
	manifestRoles       managed.ManagedSpec
	digestIdentity      *managed.Identity
	identityDigest      string
}

func newAttemptCheckedReads(tx ControllerTransaction) *attemptCheckedReads {
	return &attemptCheckedReads{ControllerTransaction: tx, rows: make(map[[3]string][]byte), missing: make(map[[3]string]bool), receipts: make(map[attemptReceiptKey]managed.Receipt), heldRows: make(map[[3]string][]byte), heldMissing: make(map[[3]string]bool)}
}

type attemptImmutableValueKey struct{ Kind, Scope, Key, BytesDigest string }

func (v *attemptCheckedReads) eventValue(ctx context.Context, scope, key string) (attemptEvent, error) {
	raw, err := v.ReadRecord(ctx, "removal_attempt_event", scope, key)
	if err != nil {
		return attemptEvent{}, err
	}

	if len(raw) > managed.MaxAttemptFact {
		return attemptEvent{}, managed.ErrCapacity
	}

	if err = ctx.Err(); err != nil {
		return attemptEvent{}, err
	}

	selected := attemptImmutableValueKey{"removal_attempt_event", scope, key, managed.Digest(raw)}

	if err = ctx.Err(); err != nil {
		return attemptEvent{}, err
	}

	if value, ok := v.events[selected]; ok {
		return value, nil
	}

	value, err := attemptDecode[attemptEvent](raw, managed.MaxAttemptFact)
	if err != nil {
		return value, err
	}

	if err = ctx.Err(); err != nil {
		return attemptEvent{}, err
	}

	if len(v.events) < 512 {
		if v.events == nil {
			v.events = make(map[attemptImmutableValueKey]attemptEvent)
		}

		v.events[selected] = value
	}

	return value, nil
}
func (v *attemptCheckedReads) storedFactDigest(ctx context.Context, kind, scope, key string, raw []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	switch kind {
	case "removal_command", "removal_fence", "removal_abort", "removal_finish", "removal_binding_settlement":
	default:
		return attemptStoredFactDigest(kind, raw)
	}

	selected := attemptImmutableValueKey{kind, scope, key, managed.Digest(raw)}

	if err := ctx.Err(); err != nil {
		return "", err
	}

	if value, ok := v.factDigests[selected]; ok {
		return value, nil
	}

	value, err := attemptStoredFactDigest(kind, raw)
	if err != nil {
		return "", err
	}

	if err = ctx.Err(); err != nil {
		return "", err
	}

	if len(v.factDigests) < 512 {
		if v.factDigests == nil {
			v.factDigests = make(map[attemptImmutableValueKey]string)
		}

		v.factDigests[selected] = value
	}

	return value, nil
}

type attemptReceiptKey struct {
	Key    managed.Key
	ID     id.ID
	Digest string
}

// originalIdentityDigest reuses only the content hash of an exact owned identity.
// Association, receipt, authority and freshness checks remain with each caller.
func originalIdentityDigest(ctx context.Context, tx ControllerTransaction, identity managed.Identity) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	reads, checked := tx.(*attemptCheckedReads)
	if checked && reads.digestIdentity != nil && reflect.DeepEqual(*reads.digestIdentity, identity) {
		return reads.identityDigest, nil
	}

	digest, err := managed.OriginalIdentityDigest(identity)
	if err != nil {
		return "", err
	}

	if err = ctx.Err(); err != nil {
		return "", err
	}

	if checked && len(identity.Manifest.Spec) <= managed.MaxManifestSpec && len(identity.Manifest.Members) <= managed.MaxMembers {
		saved := identity
		saved.Manifest.Spec = bytes.Clone(identity.Manifest.Spec)
		saved.Manifest.Members = slices.Clone(identity.Manifest.Members)
		reads.digestIdentity = &saved
		reads.identityDigest = digest
	}

	return digest, nil
}

// parsedManifest retains only successful parsing for this exact immutable identity.
// Returning owned copies keeps later consumers from mutating another binding's input.
func (v *attemptCheckedReads) parsedManifest(ctx context.Context, target *managed.Target) (managed.ProvisionSpec, managed.ManagedSpec, error) {
	if err := ctx.Err(); err != nil {
		return managed.ProvisionSpec{}, managed.ManagedSpec{}, err
	}

	if len(target.Manifest.Spec) > managed.MaxManifestSpec || len(target.Manifest.Members) > managed.MaxMembers {
		return managed.ProvisionSpec{}, managed.ManagedSpec{}, managed.ErrInvalid
	}

	if saved := v.manifestIdentity; saved != nil {
		current := target.Identity
		if saved.Key != current.Key || saved.ProvisionOperationID != current.ProvisionOperationID || saved.Physical != current.Physical || saved.Manifest.Version != current.Manifest.Version || saved.Manifest.SpecDigest != current.Manifest.SpecDigest || !slices.Equal(saved.Manifest.Members, current.Manifest.Members) || !bytes.Equal(saved.Manifest.Spec, current.Manifest.Spec) {
			return managed.ProvisionSpec{}, managed.ManagedSpec{}, managed.ErrUnavailable
		}
	} else {
		spec, e := managed.ParseProvisionSpec(target.Manifest)
		if e != nil {
			return managed.ProvisionSpec{}, managed.ManagedSpec{}, e
		}

		roles, e := managed.ParseManagedSpec(target.Manifest)
		if e != nil {
			return managed.ProvisionSpec{}, managed.ManagedSpec{}, e
		}

		if e = ctx.Err(); e != nil {
			return managed.ProvisionSpec{}, managed.ManagedSpec{}, e
		}

		saved := target.Identity
		saved.Manifest.Spec = bytes.Clone(target.Manifest.Spec)
		saved.Manifest.Members = slices.Clone(target.Manifest.Members)
		v.manifestIdentity = &saved
		v.manifestSpec = spec
		v.manifestRoles = roles
	}

	spec, roles := v.manifestSpec, v.manifestRoles
	spec.Services = slices.Clone(spec.Services)
	roles.RuntimeRoles = slices.Clone(roles.RuntimeRoles)
	roles.Provision = bytes.Clone(roles.Provision)

	return spec, roles, nil
}

func (v *attemptCheckedReads) ReadRecord(ctx context.Context, kind, scope, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	immutable := false

	switch kind {
	case "removal_current_binding", "removal_command", "removal_fence", "removal_abort", "removal_finish", "removal_binding_settlement", "removal_admission", "removal_revocation", "removal_settlement", "removal_attempt_event", managed.OriginalRegistration, managed.OriginalDrain, managed.QuiescenceHistory:
		immutable = true
	}

	selected := [3]string{kind, scope, key}
	if raw, ok := v.heldRows[selected]; ok {
		return bytes.Clone(raw), nil
	}

	if v.heldMissing[selected] {
		return nil, managed.ErrNotFound
	}

	if v.missing[selected] {
		return nil, managed.ErrNotFound
	}

	if immutable {
		if raw, ok := v.rows[selected]; ok {
			return bytes.Clone(raw), nil
		}
	}

	raw, err := v.ControllerTransaction.ReadRecord(ctx, kind, scope, key)
	if err == nil && immutable {
		v.rows[selected] = bytes.Clone(raw)
	}

	return bytes.Clone(raw), err
}
func (v *attemptCheckedReads) ReadReceipt(ctx context.Context, key managed.Key, operation id.ID, digest string) (managed.Receipt, error) {
	if err := ctx.Err(); err != nil {
		return managed.Receipt{}, err
	}

	selected := attemptReceiptKey{key, operation, digest}
	if receipt, ok := v.receipts[selected]; ok {
		return receipt, nil
	}

	receipt, err := v.ControllerTransaction.ReadReceipt(ctx, key, operation, digest)
	if err == nil {
		v.receipts[selected] = receipt
	}

	return receipt, err
}

// A checked read view cannot survive a write or be used as fresh authority.
func (*attemptCheckedReads) WriteRecord(context.Context, string, string, string, []byte, bool) error {
	return managed.ErrUnavailable
}
func (*attemptCheckedReads) Atomic(context.Context, managed.Key, id.ID, string, func(*managed.Target, time.Time) (managed.Receipt, error)) (managed.Receipt, error) {
	return managed.Receipt{}, managed.ErrUnavailable
}
func (*attemptCheckedReads) Create(context.Context, *managed.Target) (*managed.Target, error) {
	return nil, managed.ErrUnavailable
}
func (*attemptCheckedReads) ReserveRows(context.Context, managed.Identity, *managed.Identity) error {
	return managed.ErrUnavailable
}
func (*attemptCheckedReads) ProjectRows(context.Context, managed.Identity, managed.ActionReceipt, bool) error {
	return managed.ErrUnavailable
}

func (*attemptCheckedReads) ReadHeldRecords(context.Context, []ControllerRecordKey) ([]ControllerRecordResult, error) {
	return nil, managed.ErrUnavailable
}

// Batch transport stays private to one checked view and cannot be extended through it.
func (*attemptCheckedReads) ReadImmutableRecords(context.Context, []ControllerRecordKey) ([]ControllerRecordResult, error) {
	return nil, managed.ErrUnavailable
}
func (*attemptCheckedReads) ReadImmutableReceipts(context.Context, managed.Key, []id.ID) ([]ControllerReceiptResult, error) {
	return nil, managed.ErrUnavailable
}

func (v *attemptCheckedReads) prefetchRecords(ctx context.Context, keys []ControllerRecordKey) error {
	return v.prefetchSelectedRecords(ctx, keys, false, nil)
}
func (v *attemptCheckedReads) prefetchHeldRecords(ctx context.Context, keys []ControllerRecordKey) error {
	return v.prefetchSelectedRecords(ctx, keys, true, nil)
}
func (v *attemptCheckedReads) prefetchSelectedRecords(ctx context.Context, keys []ControllerRecordKey, held bool, presence map[ControllerRecordKey]bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	rowsCache, missing, limit, read := v.rows, v.missing, ImmutableRecordLimit, v.ControllerTransaction.ReadImmutableRecords
	if held {
		if v.heldRows == nil {
			v.heldRows = make(map[[3]string][]byte)
			v.heldMissing = make(map[[3]string]bool)
		}

		rowsCache, missing, limit, read = v.heldRows, v.heldMissing, HeldRecordLimit, v.ControllerTransaction.ReadHeldRecords
	}

	pending := make([]ControllerRecordKey, 0, len(keys))

	seenKeys := make(map[ControllerRecordKey]bool, len(keys))
	for _, k := range keys {
		selected := [3]string{k.Kind, k.Scope, k.Key}

		_, found := rowsCache[selected]
		if !found && !missing[selected] && !seenKeys[k] {
			pending = append(pending, k)
		}

		seenKeys[k] = true
	}

	keys = pending
	for len(keys) > 0 {
		count := min(64, len(keys))
		selected := keys[:count]
		keys = keys[count:]

		rows, err := read(ctx, selected)
		if err != nil {
			return err
		}

		if err = ctx.Err(); err != nil {
			return err
		}

		if len(rows) != len(selected) {
			return managed.ErrUnavailable
		}

		expected := make(map[ControllerRecordKey]bool, len(selected))
		for _, k := range selected {
			expected[k] = true
		}

		seen := make(map[ControllerRecordKey]bool, len(rows))
		for _, row := range rows {
			if !expected[row.ControllerRecordKey] || seen[row.ControllerRecordKey] || row.Size < 0 || !row.Found && (row.Size != 0 || len(row.Data) != 0) {
				return managed.ErrUnavailable
			}

			seen[row.ControllerRecordKey] = true
			// Preserve provider presence refusal before payload caps or discovery decoding.
			if wanted, checked := presence[row.ControllerRecordKey]; checked && row.Found != wanted {
				return managed.ErrUnavailable
			}

			if row.Size > limit(row.Kind) {
				if row.Kind == managed.OriginalRegistration || held && (row.Kind == "step" || row.Kind == "step_history") {
					return managed.ErrUnavailable
				}

				return managed.ErrCapacity
			}

			if len(row.Data) != row.Size {
				return managed.ErrUnavailable
			}
		}
		// No value or absence is installed until the complete batch has passed transport checks.
		for _, row := range rows {
			k := [3]string{row.Kind, row.Scope, row.Key}
			if row.Found {
				rowsCache[k] = bytes.Clone(row.Data)
			} else if row.Kind != managed.OriginalRegistration && row.Kind != "removal_attempt_event" && row.Kind != "step_history" {
				missing[k] = true
			}
		}
	}

	return nil
}
func (v *attemptCheckedReads) prefetchReceipts(ctx context.Context, key managed.Key, operations []id.ID) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	pending := make([]id.ID, 0, len(operations))
	cachedOps := make(map[id.ID]bool, len(operations))

	for cached := range v.receipts {
		if cached.Key == key {
			cachedOps[cached.ID] = true
		}
	}

	for _, operation := range operations {
		if !cachedOps[operation] {
			pending = append(pending, operation)
			cachedOps[operation] = true
		}
	}

	operations = pending
	for len(operations) > 0 {
		count := min(64, len(operations))
		selected := operations[:count]
		operations = operations[count:]

		rows, err := v.ControllerTransaction.ReadImmutableReceipts(ctx, key, selected)
		if err != nil {
			return err
		}

		if err = ctx.Err(); err != nil {
			return err
		}

		if len(rows) != len(selected) {
			return managed.ErrUnavailable
		}

		expected := make(map[id.ID]bool, len(selected))
		for _, op := range selected {
			expected[op] = true
		}

		seen := make(map[id.ID]bool, len(rows))
		for _, row := range rows {
			if !expected[row.OperationID] || seen[row.OperationID] || row.Found && row.Receipt.ID != row.OperationID {
				return managed.ErrUnavailable
			}

			seen[row.OperationID] = true
			if row.Found {
				raw, e := json.Marshal(row.Receipt)
				if e != nil || len(raw) > managed.MaxAttemptReceipt {
					return managed.ErrUnavailable
				}
			}
		}

		for _, row := range rows {
			if row.Found {
				v.receipts[attemptReceiptKey{key, row.OperationID, row.Receipt.Digest}] = row.Receipt
			}
		}
	}

	return nil
}
func (reads *attemptCheckedReads) prefetchAttempt(ctx context.Context, t *managed.Target, v attemptView, initial id.ID) error {
	if len(t.Reservations) > 64 || len(v.descriptor.ReservationIDs) > 64 {
		return managed.ErrUnavailable
	}

	original, err := originalScope(t.Key)
	if err != nil {
		return err
	}

	keys := make([]ControllerRecordKey, 0, 512)
	originalKeys := []ControllerRecordKey{}

	for _, binding := range t.Reservations {
		if binding.Phase != managed.Accepted {
			continue
		}

		ref, e := managed.ParseOriginalReference(binding.Receipt)
		if e != nil {
			return managed.ErrBlocked
		}

		selected := ControllerRecordKey{managed.OriginalRegistration, original, ref.OperationID.String()}
		keys = append(keys, selected)
		originalKeys = append(originalKeys, selected)
	}

	nativeKeys := []ControllerRecordKey{}

	for _, rid := range v.descriptor.ReservationIDs {
		binding := attemptBinding(v, rid)
		for _, kind := range []string{managed.AttemptBegin, managed.AttemptAbort, managed.AttemptFinish} {
			slot, e := attemptSlot(binding, kind)
			if e != nil {
				return e
			}

			nativeKeys = append(nativeKeys, ControllerRecordKey{"removal_command", v.scope, slot})
		}

		slot, e := attemptSlot(binding, "binding")
		if e != nil {
			return e
		}

		for _, kind := range []string{"removal_fence", "removal_abort", "removal_finish", "removal_binding_settlement"} {
			nativeKeys = append(nativeKeys, ControllerRecordKey{kind, v.scope, slot})
		}
	}

	keys = append(keys, nativeKeys...)
	// Deduplication affects only transport; every original association is still checked below.
	unique := make([]ControllerRecordKey, 0, len(keys))

	selectedKeys := map[ControllerRecordKey]bool{}
	for _, k := range keys {
		if !selectedKeys[k] {
			unique = append(unique, k)
			selectedKeys[k] = true
		}
	}

	if err = reads.prefetchRecords(ctx, unique); err != nil {
		return err
	}

	if err = reads.prefetchCurrentBindings(ctx, v); err != nil {
		return err
	}

	operations := []id.ID{}
	operationSet := map[id.ID]bool{}
	addOperation := func(op id.ID) error {
		if op.Prefix() != managed.PrefixOperation || len(op.String()) > 64 {
			return managed.ErrUnavailable
		}

		if !operationSet[op] {
			operations = append(operations, op)
			operationSet[op] = true
		}

		return nil
	}

	for _, k := range originalKeys {
		operation, e := id.ParseWithPrefix(k.Key, managed.PrefixOperation)
		if e != nil {
			return managed.ErrUnavailable
		}

		if e = addOperation(operation); e != nil {
			return e
		}
	}

	eventIDs := []id.ID{}
	eventSet := map[id.ID]bool{}

	addEvent := func(op id.ID) error {
		if op.Prefix() != managed.PrefixOperation || len(op.String()) > 64 {
			return managed.ErrUnavailable
		}

		if !eventSet[op] {
			eventIDs = append(eventIDs, op)
			eventSet[op] = true
		}

		return nil
	}
	if err = addEvent(initial); err != nil {
		return err
	}

	for _, k := range nativeKeys {
		raw, ok := reads.rows[[3]string{k.Kind, k.Scope, k.Key}]
		if !ok {
			continue
		}
		// This projection selects transport keys only. Full canonical and semantic
		// validation of every present own row remains mandatory below.
		var discovery struct {
			AuthorizationID  id.ID `json:"authorization_id"`
			LocalOperationID id.ID `json:"local_operation_id"`
		}
		if e := json.Unmarshal(raw, &discovery); e != nil {
			return managed.ErrUnavailable
		}

		operation := discovery.LocalOperationID

		switch k.Kind {
		case "removal_command":
			operation = discovery.AuthorizationID
		case "removal_fence", "removal_abort", "removal_finish", "removal_binding_settlement":
		default:
			return managed.ErrUnavailable
		}

		if err = addEvent(operation); err != nil {
			return err
		}
	}

	events := make([]ControllerRecordKey, 0, len(eventIDs))
	for _, op := range eventIDs {
		events = append(events, ControllerRecordKey{"removal_attempt_event", v.scope, op.String()})
	}

	if err = reads.prefetchRecords(ctx, events); err != nil {
		return err
	}

	for _, operation := range eventIDs {
		if err = addOperation(operation); err != nil {
			return err
		}
	}

	if err = reads.prefetchReceipts(ctx, t.Key, operations); err != nil {
		return err
	}

	return reads.prefetchProviders(ctx, t, v)
}

// prefetchCurrentBindings transports at most 64 exact own slots separately from
// the existing native batch. Presence is checked before payload caps; callers
// still decode each observation and validate every command and original receipt.
func (reads *attemptCheckedReads) prefetchCurrentBindings(ctx context.Context, v attemptView) error {
	if len(v.descriptor.ReservationIDs) > 64 {
		return managed.ErrUnavailable
	}

	keys := make([]ControllerRecordKey, 0, len(v.descriptor.ReservationIDs))
	presence := make(map[ControllerRecordKey]bool, len(v.descriptor.ReservationIDs))

	for _, rid := range v.descriptor.ReservationIDs {
		slot, err := attemptSlot(attemptBinding(v, rid), managed.AttemptBegin)
		if err != nil {
			return err
		}

		selected := ControllerRecordKey{"removal_current_binding", v.scope, slot}
		_, begin := reads.rows[[3]string{"removal_command", v.scope, slot}]
		presence[selected] = v.descriptor.Generation != "1" && begin
		keys = append(keys, selected)
	}

	return reads.prefetchSelectedRecords(ctx, keys, false, presence)
}

// prefetchProviders retains every own-slot selection, including zero masks.
// Discovery supplies transport keys only; attemptProviderRead validates authority.
func (reads *attemptCheckedReads) prefetchProviders(ctx context.Context, t *managed.Target, v attemptView) error {
	steps, err := attemptSteps(t, v)
	if err != nil {
		return err
	}

	if len(steps) > 33 || len(steps) > len(v.state.ProviderSlots) {
		return managed.ErrUnavailable
	}

	keys := make([]ControllerRecordKey, 0, 3*len(steps))

	presence := make(map[ControllerRecordKey]bool, 3*len(steps))

	associations := make([]ControllerRecordKey, 0, len(steps))
	for index, step := range steps {
		slot, e := providerStepID(step)
		if e != nil {
			return e
		}

		provider := ControllerRecordKey{"step", t.TenantID, slot}
		association := ControllerRecordKey{"removal_provider_slot", v.scope, slot}
		failure := ControllerRecordKey{"step_failure", t.TenantID, slot}
		keys = append(keys, provider, association, failure)
		associations = append(associations, association)
		mask := v.state.ProviderSlots[index]
		presence[provider] = mask&attemptPermissionBit != 0
		presence[association] = mask&attemptPermissionBit != 0
		presence[failure] = mask&attemptFailureBit != 0
	}

	if err = reads.prefetchSelectedRecords(ctx, keys, true, presence); err != nil {
		return err
	}

	history := make([]ControllerRecordKey, 0, 2*len(steps))
	operations := make([]id.ID, 0, len(steps))

	for _, selected := range associations {
		raw, found := reads.heldRows[[3]string{selected.Kind, selected.Scope, selected.Key}]
		if !found {
			continue
		}

		var association attemptProviderSlot
		if json.Unmarshal(raw, &association) != nil {
			return managed.ErrUnavailable
		}

		for _, hash := range []string{association.PositiveDigest, association.LastUncertaintyDigest} {
			if hash != "" {
				if !digest(hash) {
					return managed.ErrUnavailable
				}

				history = append(history, ControllerRecordKey{"step_history", t.TenantID, hash})
			}
		}

		receipt, e := association.Receipt.Value()
		if e != nil {
			return e
		}

		if receipt.ID.Prefix() != managed.PrefixOperation {
			return managed.ErrUnavailable
		}

		operations = append(operations, receipt.ID)
	}

	if err = reads.prefetchHeldRecords(ctx, history); err != nil {
		return err
	}

	return reads.prefetchReceipts(ctx, t.Key, operations)
}

func readAttemptBaseHeld(ctx context.Context, tx ControllerTransaction, t *managed.Target, k managed.AttemptKey) (attemptView, error) {
	checked, ok := tx.(*attemptCheckedReads)
	if !ok {
		checked = newAttemptCheckedReads(tx)
	}

	tx = checked

	var v attemptView
	if err := attemptVersion(k); err != nil {
		return v, err
	}

	scope, err := attemptScope(k.Key)
	if err != nil {
		return v, err
	}

	v.scope = scope

	d, err := attemptRead[managed.AttemptDescriptor](ctx, tx, "removal_attempt", scope, k.AttemptID.String(), managed.MaxAttemptDescriptor)
	if err != nil {
		return v, err
	}

	if d.SchemaVersion != "1" || (d.Generation == "1") != (d.Predecessor == nil) {
		return v, managed.ErrUnsupported
	}

	if d.Key != k.Key || d.AttemptID != k.AttemptID || d.Generation != k.Generation {
		return v, managed.ErrAuthority
	}

	dh, err := attemptHash(attemptDescriptorDomain, d)
	if err != nil {
		return v, err
	}

	v.descriptor = d
	v.ref = managed.AttemptRef{AttemptID: d.AttemptID, Generation: d.Generation, DescriptorDigest: dh}

	root, err := attemptRead[attemptRoot](ctx, tx, "removal_attempt_root", scope, "root", managed.MaxAttemptPointer)
	if err != nil {
		return v, err
	}

	if root.SchemaVersion != "1" || root.Key != k.Key || root.AttemptRef.Generation != "1" || root.OriginalOperationKey != (managed.OperationKey{Key: k.Key, ActionID: d.ProjectionActionID}) || d.Generation == "1" && (root.AttemptRef != v.ref || !root.AcceptedAt.Equal(d.AcceptedAt)) {
		return v, managed.ErrUnavailable
	}

	for _, owner := range []struct {
		id   id.ID
		role string
	}{{d.AttemptID, "attempt"}, {d.ProviderOperationID, "provider"}} {
		record, e := attemptRead[attemptIdentity](ctx, tx, "removal_identity", "", owner.id.String(), managed.MaxAttemptPointer)
		if e != nil {
			return v, e
		}

		if record.SchemaVersion != "1" || record.ID != owner.id || record.Role != owner.role || record.Key != k.Key || record.AttemptRef != v.ref {
			return v, managed.ErrUnavailable
		}
	}

	state, activation, pendingOriginals, err := readRecoveryCompatibleAttemptState(ctx, checked, scope, d.AttemptID.String())
	if err != nil {
		return v, err
	}

	if (state.Successor == nil) != (state.ClosureDigest == "") {
		return v, managed.ErrUnavailable
	}

	revision, e := state.Revision.Uint64()
	if e != nil || revision == 0 || revision > math.MaxInt64 || (state.SchemaVersion != "1" && state.SchemaVersion != "2") || state.AttemptRef != v.ref {
		return v, managed.ErrUnavailable
	}

	v.state = state
	v.recoveryActivation = activation
	v.recoveryPendingOriginals = pendingOriginals

	original, err := acceptedOperationBase(ctx, tx, root.OriginalOperationKey)
	if err != nil {
		return v, err
	}

	if original.RemovalAttemptRoot == nil || *original.RemovalAttemptRoot != root.AttemptRef {
		return v, managed.ErrUnavailable
	}

	projection, err := controllerRead[managed.ProjectionReceipt](ctx, tx, "target_projection", k.TenantID, k.InstanceID.String())
	if err != nil {
		return v, err
	}

	if projection.ActionID != d.ProjectionActionID {
		return v, managed.ErrAuthority
	}

	if err = projectionAssociations(ctx, tx, t.Identity, original, projection); err != nil {
		return v, err
	}

	if _, err = acceptedOperationBase(ctx, tx, managed.OperationKey{Key: k.Key, ActionID: d.ActionID}); err != nil {
		return v, err
	}

	identityDigest, err := originalIdentityDigest(ctx, tx, t.Identity)
	if err != nil {
		return v, err
	}

	initialID := root.InitialOperationID
	if d.Generation != "1" {
		initialID = d.ActionID
	}

	if err = checked.prefetchAttempt(ctx, t, v, initialID); err != nil {
		return v, err
	}

	v.originals = make(map[id.ID]managed.AttemptBindingReference)
	v.commands = make(map[managed.AttemptCommandKey]managed.AttemptCommandRecord)
	v.fences = make(map[managed.AttemptBindingKey]managed.AttemptFenceRecord)

	ids, regs, resources, err := attemptSets(ctx, tx, t, v.originals)
	if err != nil {
		return v, err
	}

	if d.IdentityDigest != identityDigest || d.ManifestDigest != t.Manifest.SpecDigest || d.Seal != managed.Decimal(strconv.FormatUint(t.Seal, 10)) || d.RegistrationSetDigest != regs || d.ResourcesDigest != resources || !reflect.DeepEqual(d.ReservationIDs, ids) {
		return v, managed.ErrUnavailable
	}

	if err = attemptValidateSlots(ctx, tx, t, v); err != nil {
		return v, err
	}

	if root.InitialOperationID.Prefix() != managed.PrefixOperation {
		return v, managed.ErrUnavailable
	}

	initial, initialErr := attemptEventRead(ctx, tx, v, initialID)
	if initialErr != nil {
		return v, initialErr
	}

	if !initial.Receipt.AcceptedAt.Equal(d.AcceptedAt) || d.Generation == "1" && (initial.Kind != "begin_attempt" || initial.FactKind != "removal_attempt" || initial.FactKey != d.AttemptID.String() || initial.FactDigest != dh) || d.Generation != "1" && (initial.Kind != "continue_attempt" || initial.FactKind != "removal_attempt_closure" || initial.FactKey != d.Predecessor.AttemptID.String()) {
		return v, managed.ErrUnavailable
	}

	v.validated = true
	if v.recoveryActivation != nil {
		if err = validateRecoveryActivationInspectionHeld(ctx, tx, t, v); err != nil {
			return attemptView{}, err
		}
	}

	return v, nil
}
func selectedAttempt(ctx context.Context, tx ControllerTransaction, t *managed.Target) (attemptView, error) {
	scope, err := attemptScope(t.Key)
	if err != nil {
		return attemptView{}, err
	}

	c, err := attemptRead[attemptCurrent](ctx, tx, "removal_attempt_current", scope, "current", managed.MaxAttemptPointer)
	if err != nil {
		return attemptView{}, err
	}

	return readAttempt(ctx, tx, t, managed.AttemptKey{Key: t.Key, AttemptID: c.AttemptRef.AttemptID, Generation: c.AttemptRef.Generation})
}
func attemptReceiptCheck(ctx context.Context, tx ControllerTransaction, k managed.Key, r managed.Receipt) error {
	actual, err := tx.ReadReceipt(ctx, k, r.ID, r.Digest)
	if errors.Is(err, managed.ErrNotFound) {
		return managed.ErrUnavailable
	}

	if err != nil {
		return err
	}

	if actual != r {
		return managed.ErrUnavailable
	}

	return nil
}
func attemptEventRead(ctx context.Context, tx ControllerTransaction, v attemptView, operation id.ID) (attemptEvent, error) {
	var (
		e   attemptEvent
		err error
	)
	if checked, ok := tx.(*attemptCheckedReads); ok {
		e, err = checked.eventValue(ctx, v.scope, operation.String())
	} else {
		e, err = attemptRead[attemptEvent](ctx, tx, "removal_attempt_event", v.scope, operation.String(), managed.MaxAttemptFact)
	}

	if err != nil {
		return e, err
	}

	if e.SchemaVersion != "1" || e.AttemptRef != v.ref || e.Receipt.ID != operation || e.RequestDigest != e.Receipt.Digest {
		return e, managed.ErrUnavailable
	}

	if err = attemptReceiptCheck(ctx, tx, v.descriptor.Key, e.Receipt); err != nil {
		return e, err
	}

	if e.FactKind != "" {
		raw, readErr := tx.ReadRecord(ctx, e.FactKind, v.scope, e.FactKey)
		if readErr != nil {
			return e, readErr
		}

		var (
			hash    string
			hashErr error
		)
		if checked, ok := tx.(*attemptCheckedReads); ok {
			hash, hashErr = checked.storedFactDigest(ctx, e.FactKind, v.scope, e.FactKey, raw)
		} else {
			hash, hashErr = attemptStoredFactDigest(e.FactKind, raw)
		}

		if hashErr != nil {
			return e, hashErr
		}

		if hash != e.FactDigest {
			return e, managed.ErrUnavailable
		}
	} else if e.FactDigest != e.RequestDigest {
		return e, managed.ErrUnavailable
	}

	return e, nil
}
func attemptGrantReceipt(v attemptView, r managed.Receipt) managed.AttemptReceipt {
	return managed.AttemptReceipt{Receipt: r, AttemptID: v.ref.AttemptID, Generation: v.ref.Generation, ProviderOperationID: v.descriptor.ProviderOperationID, DescriptorDigest: v.ref.DescriptorDigest}
}
func attemptUpdate(ctx context.Context, tx ControllerTransaction, v *attemptView) error {
	if v.state.SchemaVersion != "1" || v.recoveryActivation != nil {
		return managed.ErrUnsupported
	}

	v.validated = false

	n, err := v.state.Revision.Uint64()
	if err != nil || n >= math.MaxInt64 {
		return managed.ErrCapacity
	}

	v.state.Revision = managed.Decimal(strconv.FormatUint(n+1, 10))

	return attemptWrite(ctx, tx, "removal_attempt_state", v.scope, v.ref.AttemptID.String(), v.state, false, managed.MaxAttemptFact)
}
func attemptMutate(ctx context.Context, r *Repository, q managed.AttemptRequest, kind string, input any, change func(ControllerTransaction, *managed.Target, *attemptView, time.Time) error) (managed.Receipt, bool, attemptView, error) {
	var out managed.Receipt

	fresh := false

	var accepted attemptView
	if !validAttemptCommand(q.Command) {
		return out, false, accepted, managed.ErrInvalid
	}

	if err := attemptVersion(attemptLocal(q)); err != nil {
		return out, false, accepted, err
	}

	fingerprint, err := attemptFact(kind, input)
	if err != nil {
		return out, false, accepted, err
	}

	err = r.controller(ctx, "attempt:"+q.InstanceID.String(), []managed.Key{q.Key}, func(tx ControllerTransaction) error {
		scope, e := attemptScope(q.Key)
		if e != nil {
			return e
		}

		_, e = tx.ReadRecord(ctx, "removal_attempt_event", scope, q.ID.String())
		if e == nil {
			target, readErr := tx.Read(ctx, q.Key)
			if readErr != nil {
				return readErr
			}

			v, readErr := readAttempt(ctx, tx, target, attemptLocal(q))
			if readErr != nil {
				return readErr
			}

			saved, readErr := attemptEventRead(ctx, tx, v, q.ID)
			if readErr != nil {
				return readErr
			}

			if saved.Kind != kind || saved.RequestDigest != fingerprint {
				return managed.ErrConflict
			}

			out = saved.Receipt
			accepted = v

			return nil
		}

		if !controllerAbsent(e) {
			return e
		}

		out, e = tx.Atomic(ctx, q.Key, q.ID, fingerprint, func(current *managed.Target, now time.Time) (managed.Receipt, error) {
			if current.Revision != q.Revision || current.LeaseEpoch != q.LeaseEpoch || current.LeaseOwner != q.Owner || !now.Before(current.LeaseUntil) {
				return managed.Receipt{}, managed.ErrConflict
			}

			if e := removalLocalIDAvailable(ctx, tx, q.ID); e != nil {
				return managed.Receipt{}, e
			}

			held, selected, e := readAttemptWithCurrent(ctx, tx, current, attemptLocal(q))
			if e != nil {
				return managed.Receipt{}, e
			}

			if selected != held.ref || held.state.Successor != nil {
				return managed.Receipt{}, managed.ErrBlocked
			}

			if current.Phase == managed.Deleted || current.Revision >= math.MaxInt64 {
				return managed.Receipt{}, managed.ErrBlocked
			}

			if e = change(tx, current, &held, now); e != nil {
				return managed.Receipt{}, e
			}

			if e = attemptMarkMutation(&held, input); e != nil {
				return managed.Receipt{}, e
			}

			current.Revision++

			if e = attemptUpdate(ctx, tx, &held); e != nil {
				return managed.Receipt{}, e
			}

			receipt := managed.Receipt{ID: q.ID, Digest: fingerprint, Revision: current.Revision, LeaseEpoch: current.LeaseEpoch, AcceptedAt: now.UTC(), LeaseUntil: current.LeaseUntil}

			factKind, factKey, factErr := attemptMutationFact(held, input)
			if factErr != nil {
				return managed.Receipt{}, factErr
			}

			factDigest := fingerprint

			if factKind != "" {
				raw, readErr := tx.ReadRecord(ctx, factKind, held.scope, factKey)
				if readErr != nil {
					return managed.Receipt{}, readErr
				}

				factDigest, factErr = attemptStoredFactDigest(factKind, raw)
				if factErr != nil {
					return managed.Receipt{}, factErr
				}
			}

			event := attemptEvent{SchemaVersion: "1", AttemptRef: held.ref, Kind: kind, RequestDigest: fingerprint, FactKind: factKind, FactKey: factKey, FactDigest: factDigest, Receipt: receipt}
			if e = attemptWrite(ctx, tx, "removal_attempt_event", held.scope, q.ID.String(), event, true, managed.MaxAttemptFact); e != nil {
				return managed.Receipt{}, e
			}

			fresh = true
			accepted = held

			return receipt, nil
		})
		if e == nil && !fresh {
			return managed.ErrUnavailable
		}

		if e == nil {
			current, readErr := tx.Read(ctx, q.Key)
			if readErr != nil {
				return readErr
			}

			accepted, readErr = readAttempt(ctx, tx, current, attemptLocal(q))
			if readErr != nil {
				return readErr
			}
		}

		return e
	})

	return out, fresh, accepted, err
}

// BeginManagedRemovalAttempt atomically anchors the complete original attempt before send.
func (r *Repository) BeginManagedRemovalAttempt(ctx context.Context, q managed.AttemptStartRequest) (managed.AttemptReceipt, error) {
	var out managed.AttemptReceipt
	if !validAttemptCommand(q.Command) || q.AttemptID.Prefix() != managed.PrefixOperation || q.ProviderOperationID.Prefix() != managed.PrefixOperation || q.AttemptID == q.ProviderOperationID || !token(q.VerifierID) {
		return out, managed.ErrInvalid
	}

	fingerprint, err := attemptFact("begin_attempt", q)
	if err != nil {
		return out, err
	}

	err = r.controller(ctx, "attempt:"+q.InstanceID.String(), []managed.Key{q.Key}, func(tx ControllerTransaction) error {
		t, e := tx.Read(ctx, q.Key)
		if e != nil {
			return e
		}

		scope, e := attemptScope(q.Key)
		if e != nil {
			return e
		}

		existing, e := readAttempt(ctx, tx, t, managed.AttemptKey{Key: q.Key, AttemptID: q.AttemptID, Generation: "1"})
		if e == nil {
			saved, se := attemptEventRead(ctx, tx, existing, q.ID)
			if se != nil {
				return se
			}

			if saved.RequestDigest != fingerprint || existing.ref.AttemptID != q.AttemptID {
				return managed.ErrConflict
			}

			out = attemptGrantReceipt(existing, saved.Receipt)

			return nil
		}

		if !controllerAbsent(e) {
			return e
		}

		projection, e := controllerRead[managed.ProjectionReceipt](ctx, tx, "target_projection", q.TenantID, q.InstanceID.String())
		if e != nil {
			return e
		}

		operation, e := acceptedOperationBase(ctx, tx, managed.OperationKey{Key: q.Key, ActionID: projection.ActionID})
		if e != nil {
			return e
		}

		if operation.RemovalAttemptRoot != nil {
			return managed.ErrUnavailable
		}

		if _, e = tx.ReadRecord(ctx, "removal_attempt_root", scope, "root"); !controllerAbsent(e) {
			if e != nil {
				return e
			}

			return managed.ErrUnavailable
		}

		if _, e = acceptedOperationBase(ctx, tx, managed.OperationKey{Key: q.Key, ActionID: q.ActionID}); e != nil {
			return e
		}

		if e = projectionAssociations(ctx, tx, t.Identity, operation, projection); e != nil {
			return e
		}

		var v attemptView

		fresh := false

		receipt, e := tx.Atomic(ctx, q.Key, q.ID, fingerprint, func(current *managed.Target, now time.Time) (managed.Receipt, error) {
			fresh = true

			if current.Revision != q.Revision || current.LeaseEpoch != q.LeaseEpoch || current.LeaseOwner != q.Owner || !now.Before(current.LeaseUntil) {
				return managed.Receipt{}, managed.ErrConflict
			}

			if current.Phase != managed.Sealed || current.Seal == 0 || current.Deletion != nil || !fixedAttemptProfile(current) {
				return managed.Receipt{}, managed.ErrBlocked
			}

			for _, binding := range current.Reservations {
				if binding.Fence != nil || binding.Abort != nil || len(binding.Finished) > 0 {
					return managed.Receipt{}, managed.ErrBlocked
				}
			}

			ids, regs, res, e := attemptSets(ctx, tx, current, nil)
			if e != nil {
				return managed.Receipt{}, e
			}

			spec, e := managed.ParseProvisionSpec(current.Manifest)
			if e != nil {
				return managed.Receipt{}, e
			}

			reserve := uint64(7*len(ids) + len(spec.Services) + 1 + 8)
			if current.Revision > math.MaxInt64-reserve-1 {
				return managed.Receipt{}, managed.ErrCapacity
			}

			identityDigest, e := managed.OriginalIdentityDigest(current.Identity)
			if e != nil {
				return managed.Receipt{}, e
			}

			d := managed.AttemptDescriptor{SchemaVersion: "1", Key: q.Key, AttemptID: q.AttemptID, Generation: "1", ActionID: q.ActionID, ProjectionActionID: projection.ActionID, ProviderOperationID: q.ProviderOperationID, IdentityDigest: identityDigest, ManifestDigest: current.Manifest.SpecDigest, Seal: managed.Decimal(strconv.FormatUint(current.Seal, 10)), RegistrationSetDigest: regs, ReservationIDs: ids, ResourcesDigest: res, VerifierID: q.VerifierID, AcceptedAt: now.UTC()}

			dh, e := attemptHash(attemptDescriptorDomain, d)
			if e != nil {
				return managed.Receipt{}, e
			}

			ref := managed.AttemptRef{AttemptID: d.AttemptID, Generation: "1", DescriptorDigest: dh}

			v = attemptView{scope: scope, descriptor: d, ref: ref, state: managed.AttemptState{SchemaVersion: "1", AttemptRef: ref, Revision: "1", Phase: "allocated", BindingSlots: make([]byte, 64), ProviderSlots: make([]byte, 33)}}
			for _, owner := range []attemptIdentity{{"1", q.AttemptID, "attempt", q.Key, ref}, {"1", q.ProviderOperationID, "provider", q.Key, ref}} {
				if e = attemptWrite(ctx, tx, "removal_identity", "", owner.ID.String(), owner, true, managed.MaxAttemptPointer); e != nil {
					return managed.Receipt{}, e
				}
			}

			if e = attemptWrite(ctx, tx, "removal_attempt", scope, q.AttemptID.String(), d, true, managed.MaxAttemptDescriptor); e != nil {
				return managed.Receipt{}, e
			}

			if e = attemptWrite(ctx, tx, "removal_attempt_state", scope, q.AttemptID.String(), v.state, true, managed.MaxAttemptFact); e != nil {
				return managed.Receipt{}, e
			}

			if e = attemptWrite(ctx, tx, "removal_attempt_root", scope, "root", attemptRoot{"1", q.Key, ref, operation.Key, q.ID, now.UTC()}, true, managed.MaxAttemptPointer); e != nil {
				return managed.Receipt{}, e
			}

			if e = attemptWrite(ctx, tx, "removal_attempt_current", scope, "current", attemptCurrent{"1", q.Key, ref, "1"}, true, managed.MaxAttemptPointer); e != nil {
				return managed.Receipt{}, e
			}

			operation.RemovalAttemptRoot = &ref
			if e = controllerWrite(ctx, tx, "operation", q.TenantID, operation.Key.ActionID.String(), operation, false, managed.MaxControllerRecord); e != nil {
				return managed.Receipt{}, e
			}

			current.Revision++

			saved := managed.Receipt{ID: q.ID, Digest: fingerprint, Revision: current.Revision, LeaseEpoch: current.LeaseEpoch, AcceptedAt: now.UTC(), LeaseUntil: current.LeaseUntil}
			if e = attemptWrite(ctx, tx, "removal_attempt_event", scope, q.ID.String(), attemptEvent{"1", ref, "begin_attempt", fingerprint, "removal_attempt", q.AttemptID.String(), dh, saved}, true, managed.MaxAttemptFact); e != nil {
				return managed.Receipt{}, e
			}

			return saved, nil
		})
		if e != nil {
			return e
		}

		if !fresh {
			return managed.ErrUnavailable
		}

		out = attemptGrantReceipt(v, receipt)

		return nil
	})

	return out, err
}

// Only absence of an optional row itself may be treated as an empty slot.
func attemptRequiredAssociation(err error) error {
	if errors.Is(err, managed.ErrNotFound) {
		return managed.ErrUnavailable
	}

	return err
}

func attemptCommandRead(ctx context.Context, tx ControllerTransaction, t *managed.Target, v attemptView, k managed.AttemptCommandKey) (managed.AttemptCommandRecord, error) {
	var empty managed.AttemptCommandRecord
	if k.Kind != managed.AttemptBegin && k.Kind != managed.AttemptAbort && k.Kind != managed.AttemptFinish {
		return empty, managed.ErrInvalid
	}

	if k.AttemptKey != attemptKey(v) {
		return empty, managed.ErrAuthority
	}

	if cached, ok := v.commands[k]; ok {
		return cached, nil
	}

	slot, err := attemptSlot(k.AttemptBindingKey, k.Kind)
	if err != nil {
		return empty, err
	}

	out, err := attemptRead[managed.AttemptCommandRecord](ctx, tx, "removal_command", v.scope, slot, managed.MaxAttemptCommand)
	if err != nil {
		return out, err
	}

	if out.SchemaVersion != "1" || out.Key != k || out.DescriptorDigest != v.ref.DescriptorDigest || out.NativeCommand.Validate() != nil {
		return out, managed.ErrUnavailable
	}

	ref, ok := v.originals[k.ReservationID]
	if !ok {
		return out, managed.ErrUnavailable
	}

	if out.BindingReference != ref {
		return out, managed.ErrUnavailable
	}

	event, err := attemptEventRead(ctx, tx, v, out.AuthorizationID)
	if err != nil {
		return out, attemptRequiredAssociation(err)
	}

	if event.Kind != "attempt_command" || event.RequestDigest != out.AuthorizationDigest || !out.IssuedAt.Equal(event.Receipt.AcceptedAt) {
		return out, managed.ErrUnavailable
	}

	if v.descriptor.Generation != "1" && k.Kind == managed.AttemptBegin {
		if out.CurrentBindingDigest == "" {
			return out, managed.ErrUnavailable
		}

		if _, err = attemptCurrentBindingRead(ctx, tx, t, v, out); err != nil {
			return out, err
		}
	} else if out.CurrentBindingDigest != "" {
		return out, managed.ErrUnavailable
	}

	v.commands[k] = out

	return out, nil
}
func attemptFenceRead(ctx context.Context, tx ControllerTransaction, t *managed.Target, v attemptView, k managed.AttemptBindingKey) (managed.AttemptFenceRecord, error) {
	var out managed.AttemptFenceRecord

	if cached, ok := v.fences[k]; ok {
		return cached, nil
	}

	slot, err := attemptSlot(k, "binding")
	if err != nil {
		return out, err
	}

	out, err = attemptRead[managed.AttemptFenceRecord](ctx, tx, "removal_fence", v.scope, slot, managed.MaxAttemptFence)
	if err != nil {
		return out, err
	}

	c, err := attemptCommandRead(ctx, tx, t, v, managed.AttemptCommandKey{AttemptBindingKey: k, Kind: managed.AttemptBegin})
	if err != nil {
		return out, attemptRequiredAssociation(err)
	}

	hash, err := attemptHash(attemptCommandDomain, c)
	if err != nil {
		return out, err
	}

	if out.SchemaVersion != "1" || out.Key != k || out.DescriptorDigest != v.ref.DescriptorDigest || out.CommandDigest != hash {
		return out, managed.ErrUnavailable
	}

	if err = attemptNativeMatch(c, out.AttemptNativeAcceptance, time.Time{}); err != nil {
		return out, err
	}

	if v.descriptor.Generation != "1" {
		observation, e := attemptCurrentBindingRead(ctx, tx, t, v, c)
		if e != nil {
			return out, e
		}

		version, _ := observation.BindingVersion.Uint64()

		epoch, _ := observation.RemovalEpoch.Uint64()
		if out.NativeAcceptance.BindingVersion != continuationDecimal(version+1) || out.NativeAcceptance.RemovalEpoch != continuationDecimal(epoch+1) {
			return out, managed.ErrUnavailable
		}
	}

	v.fences[k] = out

	return out, nil
}
func attemptNativeMatch(c managed.AttemptCommandRecord, a managed.AttemptNativeAcceptance, now time.Time) error {
	n := a.NativeAcceptance
	if !token(a.ReceiptID) || !digest(a.NativeFenceDigest) || !evidence(a.Receipt) || managed.Digest(a.Receipt) != a.ReceiptDigest || !evidence(a.OriginalBindingProof) || managed.Digest(a.OriginalBindingProof) != a.ProofDigest || n.RequestID != c.NativeCommand.RequestID || n.RequestDigest != c.NativeCommand.NativeDigest || !digest(n.CommandDigest) || !attemptTime(n.AcceptedAt) || n.AcceptedAt.Before(c.IssuedAt) || !now.IsZero() && n.AcceptedAt.After(now) {
		return managed.ErrInvalid
	}

	for _, d := range []managed.Decimal{n.ResponseVersion, n.BindingVersion, n.RemovalEpoch} {
		i, err := d.Uint64()
		if err != nil || i == 0 || i > math.MaxInt64 {
			return managed.ErrInvalid
		}
	}

	return nil
}
func attemptSetFact(ctx context.Context, tx ControllerTransaction, v *attemptView, kind, key string, fact any, maximum int) (string, error) {
	h, err := attemptFact(kind, fact)
	if err != nil {
		return "", err
	}

	if err = attemptWrite(ctx, tx, kind, v.scope, key, fact, true, maximum); err != nil {
		return "", err
	}

	return h, nil
}

// AuthorizeManagedAttemptCommand retains a finite original command before native issue.
func (r *Repository) AuthorizeManagedAttemptCommand(ctx context.Context, q managed.AttemptCommandRequest) (managed.AttemptCommandGrant, error) {
	var out managed.AttemptCommandGrant
	if q.NativeCommand.Validate() != nil || q.CurrentBinding != nil && (q.Generation == "1" || q.Kind != managed.AttemptBegin) || q.Generation != "1" && q.Kind == managed.AttemptBegin && q.CurrentBinding == nil {
		return out, managed.ErrInvalid
	}

	if q.Generation != "1" {
		raw, e := json.Marshal(q)
		if e != nil || len(raw) > managed.MaxSuccessorCommandRequest {
			return out, managed.ErrInvalid
		}
	}

	version, err := q.ExpectedNativeVersion.Uint64()
	if err != nil || version == 0 || version > math.MaxInt64 {
		return out, managed.ErrInvalid
	}

	fingerprint, err := attemptFact("attempt_command", q)
	if err != nil {
		return out, err
	}

	receipt, fresh, accepted, err := attemptMutate(ctx, r, q.AttemptRequest, "attempt_command", q, func(tx ControllerTransaction, t *managed.Target, v *attemptView, now time.Time) error {
		if e := removalLocalIDAvailable(ctx, tx, q.NativeCommand.OperationID); e != nil {
			return e
		}

		ref, e := attemptOriginal(ctx, tx, t, q.ReservationID)
		if e != nil {
			return e
		}

		switch q.Kind {
		case managed.AttemptBegin:
			if v.state.RevocationDigest != "" || v.state.SettlementDigest != "" || v.state.AdmissionDigest != "" || v.descriptor.Generation == "1" && q.ExpectedNativeVersion != ref.ReservationVersion {
				return managed.ErrBlocked
			}
		case managed.AttemptAbort:
			if v.state.RevocationDigest == "" {
				return managed.ErrBlocked
			}

			witness, e := attemptWitnessRead(ctx, tx, t, *v, attemptBinding(*v, q.ReservationID))
			if e != nil {
				return e
			}

			if witness.Disposition == managed.Removed {
				return managed.ErrBlocked
			}
		case managed.AttemptFinish:
			settled, e := attemptSettlementRead(ctx, tx, *v)
			if e != nil {
				return e
			}

			if settled.Disposition != managed.Removed {
				return managed.ErrBlocked
			}
		default:
			return managed.ErrInvalid
		}

		if q.Kind != managed.AttemptBegin {
			f, e := attemptFenceRead(ctx, tx, t, *v, attemptBinding(*v, q.ReservationID))
			if e != nil {
				return e
			}

			if q.ExpectedNativeVersion != f.NativeAcceptance.BindingVersion {
				return managed.ErrConflict
			}
		}

		if e = attemptReserve(t, *v, 0, q.Kind != managed.AttemptBegin); e != nil {
			return e
		}

		k := managed.AttemptCommandKey{AttemptBindingKey: attemptBinding(*v, q.ReservationID), Kind: q.Kind}

		slot, e := attemptSlot(k.AttemptBindingKey, q.Kind)
		if e != nil {
			return e
		}

		c := managed.AttemptCommandRecord{SchemaVersion: "1", Key: k, DescriptorDigest: v.ref.DescriptorDigest, BindingReference: ref, ExpectedNativeVersion: q.ExpectedNativeVersion, NativeCommand: q.NativeCommand, AuthorizationID: q.ID, AuthorizationDigest: fingerprint, IssuedAt: now.UTC()}
		if q.CurrentBinding != nil {
			if e = currentBindingCheck(ctx, tx, t, *v, c, *q.CurrentBinding, now); e != nil {
				return e
			}

			if v.descriptor.Predecessor != nil {
				previousKey := managed.AttemptBindingKey{AttemptKey: managed.AttemptKey{Key: t.Key, AttemptID: v.descriptor.Predecessor.AttemptID, Generation: v.descriptor.Predecessor.Generation}, ReservationID: q.ReservationID}

				previousSlot, err := attemptSlot(previousKey, "binding")
				if err != nil {
					return err
				}

				previous, err := attemptRead[managed.AttemptTerminalRecord](ctx, tx, "removal_abort", v.scope, previousSlot, managed.MaxAttemptCommand)
				if err == nil {
					minimum, err := previous.NativeAcceptance.BindingVersion.Uint64()
					if err != nil || version < minimum {
						return managed.ErrConflict
					}
				} else if !controllerAbsent(err) {
					return err
				}
			}

			c.CurrentBindingDigest, e = attemptHash("ctrlplane.managed.current-binding.v1", *q.CurrentBinding)
			if e != nil {
				return e
			}

			if e = attemptWrite(ctx, tx, "removal_current_binding", v.scope, slot, *q.CurrentBinding, true, managed.MaxAttemptCurrentBinding); e != nil {
				return e
			}
		}

		if e = attemptWrite(ctx, tx, "removal_command", v.scope, slot, c, true, managed.MaxAttemptCommand); e != nil {
			return e
		}

		v.commands[k] = c

		return nil
	})
	if err != nil {
		return out, err
	}

	key := managed.AttemptCommandKey{AttemptBindingKey: managed.AttemptBindingKey{AttemptKey: attemptLocal(q.AttemptRequest), ReservationID: q.ReservationID}, Kind: q.Kind}

	c, ok := accepted.commands[key]
	if !ok {
		return out, managed.ErrUnavailable
	}

	out = managed.AttemptCommandGrant{Receipt: attemptGrantReceipt(accepted, receipt), Command: c, NewlyIssued: fresh}

	return out, nil
}

// AuthorizeManagedAttemptRecovery grants only the exact retained native request.
func (r *Repository) AuthorizeManagedAttemptRecovery(ctx context.Context, q managed.AttemptRecoveryRequest) (managed.AttemptRecoveryGrant, error) {
	var out managed.AttemptRecoveryGrant
	if !validAttemptCommand(q.Command) {
		return out, managed.ErrInvalid
	}

	if err := attemptVersion(attemptLocal(q.AttemptRequest)); err != nil {
		return out, err
	}

	fingerprint, err := attemptFact("attempt_recovery", q)
	if err != nil {
		return out, err
	}

	err = r.controller(ctx, "attempt:"+q.InstanceID.String(), []managed.Key{q.Key}, func(tx ControllerTransaction) error {
		t, e := tx.Read(ctx, q.Key)
		if e != nil {
			return e
		}

		v, e := readAttempt(ctx, tx, t, attemptLocal(q.AttemptRequest))
		if e != nil {
			return e
		}

		command, e := attemptCommandRead(ctx, tx, t, v, managed.AttemptCommandKey{AttemptBindingKey: attemptBinding(v, q.ReservationID), Kind: q.Kind})
		if e != nil {
			return e
		}

		h, e := attemptHash(attemptCommandDomain, command)
		if e != nil {
			return e
		}

		if h != q.CommandDigest {
			return managed.ErrConflict
		}

		out.Command = command

		recoverySlot, e := attemptSlot(command.Key.AttemptBindingKey, command.Key.Kind)
		if e != nil {
			return e
		}

		saved, e := attemptEventRead(ctx, tx, v, q.ID)
		if e == nil {
			if saved.Kind != "attempt_recovery" || saved.RequestDigest != fingerprint {
				return managed.ErrConflict
			}

			out.Receipt = attemptGrantReceipt(v, saved.Receipt)

			return nil
		}

		if !controllerAbsent(e) {
			return e
		}

		receipt, e := tx.Atomic(ctx, q.Key, q.ID, fingerprint, func(current *managed.Target, now time.Time) (managed.Receipt, error) {
			if current.Revision != q.Revision || current.LeaseEpoch != q.LeaseEpoch || current.LeaseOwner != q.Owner || !now.Before(current.LeaseUntil) {
				return managed.Receipt{}, managed.ErrConflict
			}

			if e := removalLocalIDAvailable(ctx, tx, q.ID); e != nil {
				return managed.Receipt{}, e
			}

			selected, e := readAttempt(ctx, tx, current, attemptLocal(q.AttemptRequest))
			if e != nil {
				return managed.Receipt{}, e
			}

			original, e := attemptCommandRead(ctx, tx, current, selected, command.Key)
			if e != nil {
				return managed.Receipt{}, e
			}

			if !reflect.DeepEqual(original, command) {
				return managed.Receipt{}, managed.ErrConflict
			}

			saved := managed.Receipt{ID: q.ID, Digest: fingerprint, Revision: current.Revision, LeaseEpoch: current.LeaseEpoch, AcceptedAt: now.UTC(), LeaseUntil: current.LeaseUntil}
			if e = attemptWrite(ctx, tx, "removal_attempt_event", v.scope, q.ID.String(), attemptEvent{"1", v.ref, "attempt_recovery", fingerprint, "removal_command", recoverySlot, h, saved}, true, managed.MaxAttemptFact); e != nil {
				return managed.Receipt{}, e
			}

			out.NewlyAuthorized = true

			return saved, nil
		})
		if e != nil {
			return e
		}

		if !out.NewlyAuthorized {
			return managed.ErrUnavailable
		}

		out.Receipt = attemptGrantReceipt(v, receipt)

		return nil
	})
	if err != nil {
		return managed.AttemptRecoveryGrant{}, err
	}

	return out, nil
}

// RecordManagedAttemptFence retains an exact accepted Begin without inventing admission.
func (r *Repository) RecordManagedAttemptFence(ctx context.Context, q managed.AttemptFenceRequest) (managed.Receipt, error) {
	out, _, _, err := attemptMutate(ctx, r, q.AttemptRequest, "attempt_fence", q, func(tx ControllerTransaction, t *managed.Target, v *attemptView, now time.Time) error {
		k := attemptBinding(*v, q.ReservationID)

		c, e := attemptCommandRead(ctx, tx, t, *v, managed.AttemptCommandKey{AttemptBindingKey: k, Kind: managed.AttemptBegin})
		if e != nil {
			return e
		}

		if e = attemptNativeMatch(c, q.Acceptance, now); e != nil {
			return e
		}

		f, e := q.Fence.Value()
		if e != nil {
			return e
		}

		if len(f.Survivors) > managed.MaxReservations {
			return managed.ErrInvalid
		}

		for _, s := range f.Survivors {
			for _, binding := range t.Reservations {
				if s == binding.InstanceID {
					return managed.ErrAuthority
				}
			}
		}

		ch, e := attemptHash(attemptCommandDomain, c)
		if e != nil {
			return e
		}

		slot, e := attemptSlot(k, "binding")
		if e != nil {
			return e
		}

		fact := managed.AttemptFenceRecord{LocalOperationID: q.ID, SchemaVersion: "1", Key: k, DescriptorDigest: v.ref.DescriptorDigest, CommandDigest: ch, AttemptNativeAcceptance: q.Acceptance, Fence: q.Fence}
		if e = attemptWrite(ctx, tx, "removal_fence", v.scope, slot, fact, true, managed.MaxAttemptFence); e != nil {
			return e
		}

		binding, e := reservation(t, q.ReservationID)
		if e != nil {
			return e
		}

		if v.descriptor.Generation == "1" && v.state.RevocationDigest == "" && f.ValidUntil.After(now) && t.Phase == managed.Sealed {
			if binding.Fence != nil && !reflect.DeepEqual(*binding.Fence, f) {
				return managed.ErrConflict
			}

			binding.Fence = &f
		}

		return nil
	})

	return out, err
}

// AdmitManagedRemovalAttempt validates every native binding and live closure together.
func (r *Repository) AdmitManagedRemovalAttempt(ctx context.Context, q managed.AttemptAdmissionRequest) (managed.Receipt, error) {
	out, _, _, err := attemptMutate(ctx, r, q.AttemptRequest, "attempt_admit", q, func(tx ControllerTransaction, t *managed.Target, v *attemptView, now time.Time) error {
		if v.state.AdmissionDigest != "" {
			return managed.ErrConflict
		}

		if v.state.RevocationDigest != "" || v.descriptor.Generation == "1" && (t.Deletion != nil || t.Phase != managed.Sealed) || q.VerifierID != v.descriptor.VerifierID {
			return managed.ErrBlocked
		}

		if v.descriptor.Generation == "1" {
			for _, binding := range t.Reservations {
				if binding.Phase == managed.Revoked {
					continue
				}

				f, e := attemptFenceRead(ctx, tx, t, *v, attemptBinding(*v, binding.ID))
				if e != nil {
					return e
				}

				native, e := f.Fence.Value()
				if e != nil {
					return e
				}

				if binding.Fence == nil || !reflect.DeepEqual(*binding.Fence, native) {
					return managed.ErrUnavailable
				}
			}
		}

		fence, e := attemptLiveClosureHeld(ctx, tx, t, *v, now)
		if e != nil {
			return e
		}

		a := managed.AttemptAdmission{LocalOperationID: q.ID, SchemaVersion: "1", AttemptRef: v.ref, ProviderOperationID: v.descriptor.ProviderOperationID, FenceSetDigest: fence, RegistrationSetDigest: v.descriptor.RegistrationSetDigest, ResourcesDigest: v.descriptor.ResourcesDigest, VerifierID: q.VerifierID, AcceptedAt: now.UTC(), ParentRevision: managed.Decimal(strconv.FormatUint(t.Revision+1, 10))}

		h, e := attemptSetFact(ctx, tx, v, "removal_admission", v.ref.AttemptID.String(), a, managed.MaxAttemptFact)
		if e != nil {
			return e
		}

		v.state.AdmissionDigest = h

		v.state.Phase = "admitted"
		if v.descriptor.Generation == "1" {
			t.Deletion = &managed.Deletion{OperationID: v.descriptor.ProviderOperationID, FenceDigest: fence, Verifier: q.VerifierID}
			t.Phase = managed.Admitted
		}

		return nil
	})

	return out, err
}

type attemptStepCoverage struct {
	Step           managed.ProviderStepKey `json:"step"`
	State          string                  `json:"state"`
	MaterialDigest string                  `json:"material_digest"`
}

func attemptSteps(t *managed.Target, v attemptView) ([]managed.ProviderStepKey, error) {
	copyTarget := *t
	copyTarget.Deletion = &managed.Deletion{OperationID: v.descriptor.ProviderOperationID}

	return expectedSteps(&copyTarget, v.descriptor.ActionID, true)
}
func attemptCoverage(ctx context.Context, tx ControllerTransaction, t *managed.Target, v attemptView) ([]attemptStepCoverage, []string, string, error) {
	steps, err := attemptSteps(t, v)
	if err != nil {
		return nil, nil, "", err
	}

	coverage := []attemptStepCoverage{}
	refs := []string{}

	for index, step := range steps {
		key, e := providerStepID(step)
		if e != nil {
			return nil, nil, "", e
		}

		record, _, e := attemptProviderRead(ctx, tx, t, v, step, index)
		slot := attemptStepCoverage{Step: step, State: "unissued"}

		if e != nil {
			return nil, nil, "", e
		}

		if v.state.ProviderSlots[index]&attemptPermissionBit != 0 {
			resourceName := step.Service
			if resourceName == "" {
				resourceName = "network"
			}

			if record.Permission.Step != step || record.Permission.ResourceID != t.Resources[resourceName] || record.Permission.SpecDigest != t.Manifest.SpecDigest || record.Permission.Physical != t.Physical || record.Permission.Seal != t.Seal || record.Permission.CreateProfile != "ctrlplane.managed.create.v1" {
				return nil, nil, "", managed.ErrUnavailable
			}

			failure, fe := attemptRead[managed.ProviderStepFailure](ctx, tx, "step_failure", t.TenantID, key, managed.MaxAttemptSettlement)
			if fe != nil && !controllerAbsent(fe) {
				return nil, nil, "", fe
			}

			slot.State = "unresolved"

			switch {
			case fe == nil:
				if !reflect.DeepEqual(failure.Permission, record.Permission) || failure.AttemptRef != v.ref || record.Result != nil && record.Result.Outcome == managed.PortTerminal {
					return nil, nil, "", managed.ErrConflict
				}

				slot.State = "local_no_dispatch"
				slot.MaterialDigest, e = attemptHash(attemptFailureDomain, failure)
			case record.Result != nil && record.Result.Outcome == managed.PortTerminal:
				slot.State = "removed"
				slot.MaterialDigest, e = attemptFact("positive_step", *record.Result)
			default:
				slot.MaterialDigest, e = attemptFact("provider_permission", record.Permission)
			}

			if e != nil {
				return nil, nil, "", e
			}
		}

		h, e := attemptFact("step_coverage", slot)
		if e != nil {
			return nil, nil, "", e
		}

		refs = append(refs, h)
		coverage = append(coverage, slot)
	}

	hash, err := attemptHash(attemptCoverageDomain, struct {
		Attempt      managed.AttemptRef    `json:"attempt"`
		Reservations []id.ID               `json:"reservations"`
		Steps        []attemptStepCoverage `json:"steps"`
	}{v.ref, v.descriptor.ReservationIDs, coverage})

	return coverage, refs, hash, err
}
func attemptSettlementRead(ctx context.Context, tx ControllerTransaction, v attemptView) (managed.AttemptSettlement, error) {
	s, err := attemptRead[managed.AttemptSettlement](ctx, tx, "removal_settlement", v.scope, v.ref.AttemptID.String(), managed.MaxAttemptSettlement)
	if err != nil {
		return s, err
	}

	h, err := attemptFact("removal_settlement", s)
	if err != nil {
		return s, err
	}

	if h != v.state.SettlementDigest || s.AttemptRef != v.ref {
		return s, managed.ErrUnavailable
	}

	return s, nil
}
func attemptRevocationRead(ctx context.Context, tx ControllerTransaction, v attemptView) (managed.AttemptRevocation, error) {
	s, err := attemptRead[managed.AttemptRevocation](ctx, tx, "removal_revocation", v.scope, v.ref.AttemptID.String(), managed.MaxAttemptFact)
	if err != nil {
		return s, err
	}

	h, err := attemptFact("removal_revocation", s)
	if err != nil {
		return s, err
	}

	if h != v.state.RevocationDigest || s.AttemptRef != v.ref {
		return s, managed.ErrUnavailable
	}

	return s, nil
}
func attemptRevoke(ctx context.Context, tx ControllerTransaction, t *managed.Target, v *attemptView, disposition managed.Disposition, coverage string, now time.Time, localID id.ID) error {
	if v.state.RevocationDigest != "" {
		return managed.ErrConflict
	}

	fact := managed.AttemptRevocation{LocalOperationID: localID, SchemaVersion: "1", AttemptRef: v.ref, ProviderOperationID: v.descriptor.ProviderOperationID, CoverageDigest: coverage, Disposition: disposition, SettlementDigest: v.state.SettlementDigest, RevokedAt: now.UTC(), ParentRevision: managed.Decimal(strconv.FormatUint(t.Revision+1, 10))}

	h, err := attemptSetFact(ctx, tx, v, "removal_revocation", v.ref.AttemptID.String(), fact, managed.MaxAttemptFact)
	if err != nil {
		return err
	}

	v.state.RevocationDigest = h
	v.state.Phase = "revoked"

	return nil
}

// RevokeManagedRemovalAttempt permanently closes every unissued native/provider slot.
func (r *Repository) RevokeManagedRemovalAttempt(ctx context.Context, q managed.AttemptRequest) (managed.Receipt, error) {
	out, _, _, err := attemptMutate(ctx, r, q, "attempt_revoke", q, func(tx ControllerTransaction, t *managed.Target, v *attemptView, now time.Time) error {
		coverage, _, hash, e := attemptCoverage(ctx, tx, t, *v)
		if e != nil {
			return e
		}

		disposition := managed.NotIssued

		for _, slot := range coverage {
			if slot.State != "unissued" {
				s, e := attemptSettlementRead(ctx, tx, *v)
				if e != nil {
					return managed.ErrBlocked
				}

				if s.Disposition != managed.SettledWithoutDeletion {
					return managed.ErrBlocked
				}

				disposition = s.Disposition

				break
			}
		}

		if e = attemptRevoke(ctx, tx, t, v, disposition, hash, now, q.ID); e != nil {
			return e
		}

		if v.descriptor.Generation == "1" && t.Deletion != nil && !t.Deletion.Issued {
			t.Deletion.Settlement = &managed.Settlement{OperationID: v.descriptor.ProviderOperationID, FenceDigest: t.Deletion.FenceDigest, Disposition: managed.NotIssued, Verifier: v.descriptor.VerifierID, Evidence: []byte(q.ID.String()), SettledAt: now.UTC()}
			t.Phase = managed.Sealed
		}

		return nil
	})

	return out, err
}

// SettleManagedRemovalAttempt accepts only complete, qualified finite-step disposition.
func (r *Repository) SettleManagedRemovalAttempt(ctx context.Context, q managed.AttemptSettlementRequest) (managed.Receipt, error) {
	out, _, _, err := attemptMutate(ctx, r, q.AttemptRequest, "attempt_settle", q, func(tx ControllerTransaction, t *managed.Target, v *attemptView, now time.Time) error {
		if v.state.SettlementDigest != "" {
			return managed.ErrConflict
		}

		if !evidence(q.Evidence) || !attemptTime(q.SettledAt) || q.SettledAt.After(now) || q.SettledAt.Before(v.descriptor.AcceptedAt) || q.VerifierID != v.descriptor.VerifierID {
			return managed.ErrInvalid
		}

		slots, refs, hash, e := attemptCoverage(ctx, tx, t, *v)
		if e != nil {
			return e
		}

		switch q.Disposition {
		case managed.Removed:
			if q.ProducerProfile != "ctrlplane.managed.remove.v1" || v.state.ProviderIssuedAt == nil {
				return managed.ErrBlocked
			}

			for _, slot := range slots {
				if slot.State != "removed" {
					return managed.ErrBlocked
				}
			}
		case managed.SettledWithoutDeletion:
			if q.ProducerProfile != managed.LocalNoDispatchProfile {
				return managed.ErrUnsupported
			}

			issued := false

			for _, slot := range slots {
				if slot.State == "local_no_dispatch" {
					issued = true
				} else if slot.State != "unissued" {
					return managed.ErrBlocked
				}
			}

			if !issued {
				return managed.ErrBlocked
			}
		default:
			return managed.ErrUnsupported
		}

		s := managed.AttemptSettlement{LocalOperationID: q.ID, SchemaVersion: "1", AttemptRef: v.ref, ProviderOperationID: v.descriptor.ProviderOperationID, ResourcesDigest: v.descriptor.ResourcesDigest, AdmissionDigest: v.state.AdmissionDigest, RevocationDigest: v.state.RevocationDigest, Disposition: q.Disposition, StepReferences: refs, CoverageDigest: hash, ProducerProfile: q.ProducerProfile, VerifierID: q.VerifierID, Evidence: q.Evidence, EvidenceDigest: managed.Digest(q.Evidence), SettledAt: q.SettledAt}

		h, e := attemptSetFact(ctx, tx, v, "removal_settlement", v.ref.AttemptID.String(), s, managed.MaxAttemptSettlement)
		if e != nil {
			return e
		}

		v.state.SettlementDigest = h

		v.state.Phase = "settled"
		if q.Disposition == managed.SettledWithoutDeletion && v.state.RevocationDigest == "" {
			if e = attemptRevoke(ctx, tx, t, v, q.Disposition, hash, now, q.ID); e != nil {
				return e
			}
		}

		if v.descriptor.Generation == "1" && t.Deletion != nil {
			t.Deletion.Settlement = &managed.Settlement{OperationID: v.descriptor.ProviderOperationID, FenceDigest: t.Deletion.FenceDigest, Disposition: q.Disposition, Verifier: q.VerifierID, Evidence: q.Evidence, SettledAt: q.SettledAt}
		}

		return nil
	})

	return out, err
}
func attemptWitnessRead(ctx context.Context, tx ControllerTransaction, t *managed.Target, v attemptView, k managed.AttemptBindingKey) (managed.AttemptBindingSettlement, error) {
	var out managed.AttemptBindingSettlement

	slot, err := attemptSlot(k, "binding")
	if err != nil {
		return out, err
	}

	out, err = attemptRead[managed.AttemptBindingSettlement](ctx, tx, "removal_binding_settlement", v.scope, slot, managed.MaxAttemptFact)
	if err != nil {
		return out, err
	}

	f, err := attemptFenceRead(ctx, tx, t, v, k)
	if err != nil {
		return out, attemptRequiredAssociation(err)
	}

	h, err := attemptFact("removal_fence", f)
	if err != nil {
		return out, err
	}

	if out.SchemaVersion != "1" || out.Key != k || out.DescriptorDigest != v.ref.DescriptorDigest || out.FenceFactDigest != h || out.NativeFenceDigest != f.NativeFenceDigest || out.WitnessedAt.Before(f.NativeAcceptance.AcceptedAt) || out.RevocationDigest != v.state.RevocationDigest || out.SettlementDigest != v.state.SettlementDigest {
		return out, managed.ErrUnavailable
	}

	return out, nil
}

// RecordManagedAttemptBindingSettlement witnesses continuing nondeletion after late Begin.
func (r *Repository) RecordManagedAttemptBindingSettlement(ctx context.Context, q managed.AttemptBindingSettlementRequest) (managed.Receipt, error) {
	out, _, _, err := attemptMutate(ctx, r, q.AttemptRequest, "attempt_witness", q, func(tx ControllerTransaction, t *managed.Target, v *attemptView, now time.Time) error {
		rev, e := attemptRevocationRead(ctx, tx, *v)
		if e != nil {
			return e
		}

		k := attemptBinding(*v, q.ReservationID)

		f, e := attemptFenceRead(ctx, tx, t, *v, k)
		if e != nil {
			return e
		}

		fh, e := attemptFact("removal_fence", f)
		if e != nil {
			return e
		}

		if fh != q.FenceFactDigest || now.Before(f.NativeAcceptance.AcceptedAt) {
			return managed.ErrInvalid
		}

		slots, _, coverage, e := attemptCoverage(ctx, tx, t, *v)
		if e != nil {
			return e
		}

		for _, s := range slots {
			if rev.Disposition == managed.NotIssued && s.State != "unissued" || rev.Disposition == managed.SettledWithoutDeletion && s.State != "unissued" && s.State != "local_no_dispatch" {
				return managed.ErrBlocked
			}
		}

		slot, e := attemptSlot(k, "binding")
		if e != nil {
			return e
		}

		witness := managed.AttemptBindingSettlement{LocalOperationID: q.ID, SchemaVersion: "1", Key: k, DescriptorDigest: v.ref.DescriptorDigest, FenceFactDigest: fh, NativeFenceDigest: f.NativeFenceDigest, RevocationDigest: v.state.RevocationDigest, SettlementDigest: v.state.SettlementDigest, CoverageDigest: coverage, Disposition: rev.Disposition, WitnessedAt: now.UTC(), ParentRevision: managed.Decimal(strconv.FormatUint(t.Revision+1, 10))}

		return attemptWrite(ctx, tx, "removal_binding_settlement", v.scope, slot, witness, true, managed.MaxAttemptFact)
	})

	return out, err
}
func attemptTerminal(ctx context.Context, r *Repository, q managed.AttemptRequest, rid id.ID, a managed.AttemptNativeAcceptance, verified, until time.Time, kind string, input any) (managed.Receipt, error) {
	out, _, _, err := attemptMutate(ctx, r, q, "attempt_"+kind, input, func(tx ControllerTransaction, t *managed.Target, v *attemptView, now time.Time) error {
		k := attemptBinding(*v, rid)

		command, e := attemptCommandRead(ctx, tx, t, *v, managed.AttemptCommandKey{AttemptBindingKey: k, Kind: kind})
		if e != nil {
			return e
		}

		if e = attemptNativeMatch(command, a, now); e != nil {
			return e
		}

		fence, e := attemptFenceRead(ctx, tx, t, *v, k)
		if e != nil {
			return e
		}

		if fence.NativeFenceDigest != a.NativeFenceDigest {
			return managed.ErrAuthority
		}

		fh, e := attemptFact("removal_fence", fence)
		if e != nil {
			return e
		}

		ch, e := attemptHash(attemptCommandDomain, command)
		if e != nil {
			return e
		}

		witnessDigest := ""

		if kind == managed.AttemptAbort {
			witness, e := attemptWitnessRead(ctx, tx, t, *v, k)
			if e != nil {
				return e
			}

			if verified.Before(witness.WitnessedAt) || verified.After(now) || !until.After(now) || !until.After(verified) {
				return managed.ErrBlocked
			}

			witnessDigest, e = attemptFact("removal_binding_settlement", witness)
			if e != nil {
				return e
			}
		} else {
			s, e := attemptSettlementRead(ctx, tx, *v)
			if e != nil {
				return e
			}

			if s.Disposition != managed.Removed {
				return managed.ErrBlocked
			}
		}

		slot, e := attemptSlot(k, "binding")
		if e != nil {
			return e
		}

		opposite := "removal_abort"
		if kind == managed.AttemptAbort {
			opposite = "removal_finish"
		}

		if _, e = tx.ReadRecord(ctx, opposite, v.scope, slot); !controllerAbsent(e) {
			if e != nil {
				return e
			}

			return managed.ErrConflict
		}

		fact := managed.AttemptTerminalRecord{LocalOperationID: q.ID, SchemaVersion: "1", Key: k, DescriptorDigest: v.ref.DescriptorDigest, CommandDigest: ch, FenceFactDigest: fh, AttemptNativeAcceptance: a, ProofVerifiedAt: verified, ProofValidUntil: until, SettlementWitnessDigest: witnessDigest}
		if e = attemptWrite(ctx, tx, "removal_"+kind, v.scope, slot, fact, true, managed.MaxAttemptCommand); e != nil {
			return e
		}

		binding, e := reservation(t, rid)
		if e != nil {
			return e
		}

		if v.descriptor.Generation == "1" && binding.Fence != nil && t.Deletion != nil && t.Deletion.Settlement != nil {
			if kind == managed.AttemptFinish {
				binding.Finished = a.Receipt
			} else {
				binding.Abort = &managed.AbortProof{FenceDigest: binding.Fence.Digest, VerifiedAt: verified, ValidUntil: until, Evidence: a.OriginalBindingProof, Receipt: a.Receipt}
				t.Phase = managed.Sealed
			}
		}

		return nil
	})

	return out, err
}

// RecordManagedAttemptAbort retains the accepted exact original Abort.
func (r *Repository) RecordManagedAttemptAbort(ctx context.Context, q managed.AttemptAbortRequest) (managed.Receipt, error) {
	return attemptTerminal(ctx, r, q.AttemptRequest, q.ReservationID, q.Acceptance, q.ProofVerifiedAt, q.ProofValidUntil, managed.AttemptAbort, q)
}

// RecordManagedAttemptFinish retains the accepted exact original Finish after Removed.
func (r *Repository) RecordManagedAttemptFinish(ctx context.Context, q managed.AttemptFinishRequest) (managed.Receipt, error) {
	return attemptTerminal(ctx, r, q.AttemptRequest, q.ReservationID, q.Acceptance, time.Time{}, time.Time{}, managed.AttemptFinish, q)
}

// TombstoneManagedRemovalAttempt preserves every exact original terminal obligation.
func (r *Repository) TombstoneManagedRemovalAttempt(ctx context.Context, q managed.AttemptRequest) (managed.Receipt, error) {
	out, _, _, err := attemptMutate(ctx, r, q, "attempt_tombstone", q, func(tx ControllerTransaction, t *managed.Target, v *attemptView, _ time.Time) error {
		s, e := attemptSettlementRead(ctx, tx, *v)
		if e != nil {
			return e
		}

		if s.Disposition != managed.Removed {
			return managed.ErrBlocked
		}

		coverage, _, _, e := attemptCoverage(ctx, tx, t, *v)
		if e != nil {
			return e
		}

		for _, step := range coverage {
			if step.State != "removed" {
				return managed.ErrBlocked
			}
		}

		for _, binding := range t.Reservations {
			if binding.Phase == managed.Revoked {
				continue
			}

			record, e := attemptBindingRead(ctx, tx, t, *v, attemptBinding(*v, binding.ID))
			if e != nil {
				return e
			}

			if record.Finish == nil || record.Abort != nil {
				return managed.ErrBlocked
			}
		}

		t.Phase = managed.Deleted
		t.TerminalDisposition = managed.Removed
		v.state.Phase = "removed"
		v.state.TerminalDigest = v.state.SettlementDigest

		return nil
	})

	return out, err
}

func attemptBindingRead(ctx context.Context, tx ControllerTransaction, t *managed.Target, v attemptView, k managed.AttemptBindingKey) (managed.AttemptBindingRecord, error) {
	out := managed.AttemptBindingRecord{Key: k, DescriptorDigest: v.ref.DescriptorDigest, Commands: []managed.AttemptCommandRecord{}}
	if k.AttemptKey != attemptKey(v) {
		return out, managed.ErrAuthority
	}

	if _, err := reservation(t, k.ReservationID); err != nil {
		return out, err
	}

	for _, kind := range []string{managed.AttemptBegin, managed.AttemptAbort, managed.AttemptFinish} {
		c, err := attemptCommandRead(ctx, tx, t, v, managed.AttemptCommandKey{AttemptBindingKey: k, Kind: kind})
		if err == nil {
			out.Commands = append(out.Commands, c)
		} else if !controllerAbsent(err) {
			return out, err
		}
	}

	sourceSlot, err := attemptSlot(k, managed.AttemptBegin)
	if err != nil {
		return out, err
	}

	var begin *managed.AttemptCommandRecord

	for i := range out.Commands {
		if out.Commands[i].Key.Kind == managed.AttemptBegin {
			begin = &out.Commands[i]
		}
	}

	wanted := v.descriptor.Generation != "1" && begin != nil
	if err = attemptPresence(ctx, tx, "removal_current_binding", v.scope, sourceSlot, wanted); err != nil {
		return out, err
	}

	if wanted {
		source, e := attemptCurrentBindingRead(ctx, tx, t, v, *begin)
		if e != nil {
			return out, e
		}

		out.CurrentBinding = &source
	}

	f, err := attemptFenceRead(ctx, tx, t, v, k)
	if err == nil {
		out.Fence = &f
	} else if !controllerAbsent(err) {
		return out, err
	}

	slot, err := attemptSlot(k, "binding")
	if err != nil {
		return out, err
	}

	for _, kind := range []string{managed.AttemptAbort, managed.AttemptFinish} {
		terminal, e := attemptRead[managed.AttemptTerminalRecord](ctx, tx, "removal_"+kind, v.scope, slot, managed.MaxAttemptCommand)
		if controllerAbsent(e) {
			continue
		}

		if e != nil {
			return out, e
		}

		c, e := attemptCommandRead(ctx, tx, t, v, managed.AttemptCommandKey{AttemptBindingKey: k, Kind: kind})
		if e != nil {
			return out, attemptRequiredAssociation(e)
		}

		ch, e := attemptHash(attemptCommandDomain, c)
		if e != nil {
			return out, e
		}

		if out.Fence == nil || terminal.Key != k || terminal.DescriptorDigest != v.ref.DescriptorDigest || terminal.CommandDigest != ch {
			return out, managed.ErrUnavailable
		}

		fh, e := attemptFact("removal_fence", *out.Fence)
		if e != nil {
			return out, e
		}

		if terminal.FenceFactDigest != fh || terminal.NativeFenceDigest != out.Fence.NativeFenceDigest {
			return out, managed.ErrUnavailable
		}

		if e = attemptNativeMatch(c, terminal.AttemptNativeAcceptance, time.Time{}); e != nil {
			return out, e
		}

		if kind == managed.AttemptAbort {
			out.Abort = &terminal
		} else {
			out.Finish = &terminal
		}
	}

	if out.Abort != nil && out.Finish != nil {
		return out, managed.ErrConflict
	}

	w, err := attemptWitnessRead(ctx, tx, t, v, k)
	if err == nil {
		out.Witness = &w
	} else if !controllerAbsent(err) {
		return out, err
	}

	raw, err := json.Marshal(out)
	if err != nil {
		return out, err
	}

	if len(raw) > managed.MaxAttemptBindingResponse {
		return out, managed.ErrCapacity
	}

	return out, nil
}

// ReadManagedRemovalAttempt resolves exact retained authority without invoking producers.
func (r *Repository) ReadManagedRemovalAttempt(ctx context.Context, k managed.AttemptKey) (managed.AttemptRecord, error) {
	var out managed.AttemptRecord

	err := r.controller(ctx, "attempt:"+k.InstanceID.String(), []managed.Key{k.Key}, func(tx ControllerTransaction) error {
		t, e := tx.Read(ctx, k.Key)
		if e != nil {
			return e
		}

		v, e := r.readConfiguredAttemptInspection(ctx, tx, t, k)
		if e != nil {
			return e
		}

		root, e := attemptRead[attemptRoot](ctx, tx, "removal_attempt_root", v.scope, "root", managed.MaxAttemptPointer)
		if e != nil {
			return e
		}

		initialID := root.InitialOperationID
		if v.descriptor.Generation != "1" {
			initialID = v.descriptor.ActionID
		}

		event, e := attemptEventRead(ctx, tx, v, initialID)
		if e != nil {
			return e
		}

		if v.descriptor.Generation == "1" && (event.Kind != "begin_attempt" || event.FactDigest != v.ref.DescriptorDigest) {
			return managed.ErrUnavailable
		}

		out = managed.AttemptRecord{Descriptor: v.descriptor, State: v.state, Receipt: attemptGrantReceipt(v, event.Receipt)}

		return nil
	})

	return out, err
}

// ReadManagedAttemptCommand recovers one exact immutable original command.
func (r *Repository) ReadManagedAttemptCommand(ctx context.Context, k managed.AttemptCommandKey) (managed.AttemptCommandRecord, error) {
	var out managed.AttemptCommandRecord

	err := r.controller(ctx, "attempt:"+k.InstanceID.String(), []managed.Key{k.Key}, func(tx ControllerTransaction) error {
		t, e := tx.Read(ctx, k.Key)
		if e != nil {
			return e
		}

		v, e := readAttempt(ctx, tx, t, k.AttemptKey)
		if e != nil {
			return e
		}

		out, e = attemptCommandRead(ctx, tx, t, v, k)

		return e
	})

	return out, err
}

// ReadManagedAttemptBinding returns bounded full evidence for one exact binding.
func (r *Repository) ReadManagedAttemptBinding(ctx context.Context, k managed.AttemptBindingKey) (managed.AttemptBindingRecord, error) {
	var out managed.AttemptBindingRecord

	err := r.controller(ctx, "attempt:"+k.InstanceID.String(), []managed.Key{k.Key}, func(tx ControllerTransaction) error {
		t, e := tx.Read(ctx, k.Key)
		if e != nil {
			return e
		}

		v, e := r.readConfiguredAttemptInspection(ctx, tx, t, k.AttemptKey)
		if e != nil {
			return e
		}

		out, e = attemptBindingRead(ctx, tx, t, v, k)

		return e
	})

	return out, err
}

// ReadManagedAttemptHistoricalSettlement separates partial native binding facts from A admission.
func (r *Repository) ReadManagedAttemptHistoricalSettlement(ctx context.Context, q managed.AttemptSettlementReadRequest) (managed.AttemptHistoricalSettlement, error) {
	var out managed.AttemptHistoricalSettlement

	err := r.controller(ctx, "attempt:"+q.InstanceID.String(), []managed.Key{q.Key}, func(tx ControllerTransaction) error {
		t, e := tx.Read(ctx, q.Key)
		if e != nil {
			return e
		}

		v, e := readAttempt(ctx, tx, t, q.AttemptKey)
		if e != nil {
			return e
		}

		if v.descriptor.ProviderOperationID != q.ProviderOperationID {
			return managed.ErrAuthority
		}

		witness, e := attemptWitnessRead(ctx, tx, t, v, q.AttemptBindingKey)
		if e != nil {
			return e
		}

		if witness.NativeFenceDigest != q.NativeFenceDigest {
			return managed.ErrAuthority
		}

		_, refs, _, e := attemptCoverage(ctx, tx, t, v)
		if e != nil {
			return e
		}

		out = managed.AttemptHistoricalSettlement{Request: q, AttemptRef: v.ref, Witness: witness, StepReferences: refs, ResourcesDigest: v.descriptor.ResourcesDigest}
		if v.state.AdmissionDigest != "" {
			a, e := attemptRead[managed.AttemptAdmission](ctx, tx, "removal_admission", v.scope, v.ref.AttemptID.String(), managed.MaxAttemptFact)
			if e != nil {
				return e
			}

			out.Admission = &a
		}

		if v.state.RevocationDigest != "" {
			rev, e := attemptRevocationRead(ctx, tx, v)
			if e != nil {
				return e
			}

			out.Revocation = &rev
		}

		if v.state.SettlementDigest != "" {
			settled, e := attemptSettlementRead(ctx, tx, v)
			if e != nil {
				return e
			}

			out.Settlement = &settled
		}

		raw, e := json.Marshal(out)
		if e != nil {
			return e
		}

		if len(raw) > managed.MaxAttemptBindingResponse {
			return managed.ErrCapacity
		}

		return nil
	})

	return out, err
}

// ReadManagedHistoricalSettlement preserves the original admitted aggregate fence meaning.
func (r *Repository) ReadManagedHistoricalSettlement(ctx context.Context, q managed.HistoricalSettlementRequest) (managed.HistoricalSettlement, error) {
	var out managed.HistoricalSettlement
	if err := q.Validate(); err != nil {
		return out, err
	}

	err := r.controller(ctx, "attempt:"+q.InstanceID.String(), []managed.Key{q.Key}, func(tx ControllerTransaction) error {
		t, e := tx.Read(ctx, q.Key)
		if e != nil {
			return e
		}

		v, e := readAttempt(ctx, tx, t, managed.AttemptKey{Key: q.Key, AttemptID: q.AttemptID, Generation: q.AttemptGeneration})
		if e != nil {
			return e
		}

		var settlement managed.Settlement

		revokedBeforeIssue := v.state.ProviderIssuedAt == nil
		if v.ref.Generation == "1" {
			if t.Deletion == nil || t.Deletion.Settlement == nil {
				return managed.ErrAuthority
			}

			settlement = *t.Deletion.Settlement
		} else {
			saved, e := attemptSettlementRead(ctx, tx, v)
			if e != nil {
				return e
			}

			admission, e := attemptRead[managed.AttemptAdmission](ctx, tx, "removal_admission", v.scope, v.ref.AttemptID.String(), managed.MaxAttemptFact)
			if e != nil {
				return e
			}

			settlement = managed.Settlement{OperationID: v.descriptor.ProviderOperationID, FenceDigest: admission.FenceSetDigest, Disposition: saved.Disposition, Verifier: saved.VerifierID, Evidence: saved.Evidence, SettledAt: saved.SettledAt}
		}

		if q.ProviderOperationID != v.descriptor.ProviderOperationID || q.FenceDigest != settlement.FenceDigest || q.ManifestDigest != v.descriptor.ManifestDigest || q.Seal != v.descriptor.Seal {
			return managed.ErrAuthority
		}

		action, e := acceptedOperationBase(ctx, tx, managed.OperationKey{Key: q.Key, ActionID: v.descriptor.ActionID})
		if e != nil {
			return e
		}

		if action.Submission.Installation != q.Installation || action.Submission.Authority != q.Authority {
			return managed.ErrAuthority
		}

		steps, e := attemptSteps(t, v)
		if e != nil {
			return e
		}

		records := []managed.WireProviderStepRecord{}

		for _, step := range steps {
			key, e := providerStepID(step)
			if e != nil {
				return e
			}

			record, e := controllerRead[managed.ProviderStepRecord](ctx, tx, "step", q.TenantID, key)
			if controllerAbsent(e) {
				continue
			}

			if e != nil {
				return e
			}

			records = append(records, managed.ProviderStepWire(record))
		}

		root, e := attemptRead[attemptRoot](ctx, tx, "removal_attempt_root", v.scope, "root", managed.MaxAttemptPointer)
		if e != nil {
			return e
		}

		birthID := root.InitialOperationID
		if v.ref.Generation != "1" {
			birthID = v.descriptor.ActionID
		}

		event, e := attemptEventRead(ctx, tx, v, birthID)
		if e != nil {
			return e
		}

		out = managed.HistoricalSettlement{Request: q, Physical: t.Physical, Resources: t.Resources, Steps: records, Disposition: settlement.Disposition, RevokedBeforeIssue: revokedBeforeIssue, Settlement: settlement, Receipt: managed.ReceiptWire(event.Receipt)}

		raw, e := json.Marshal(out)
		if e != nil {
			return e
		}

		if len(raw) > managed.MaxAttemptSettlementResponse {
			return managed.ErrCapacity
		}

		return nil
	})

	return out, err
}

// RecordManagedProviderStepFailure retains only exact positively witnessed nondelegation.
func (r *Repository) RecordManagedProviderStepFailure(ctx context.Context, q managed.ProviderStepFailure) error {
	if q.SchemaVersion != "1" || q.ProducerProfile != managed.LocalNoDispatchProfile || q.Class != "local_no_dispatch" || q.Phase != "before_transport_delegate" || q.InvocationID != q.Permission.CommandID || !token(q.VerifierID) || !attemptTime(q.ObservedAt) || q.ObservedAt.Before(q.Permission.IssuedAt) || !evidence(q.Witness) || managed.Digest(q.Witness) != q.WitnessDigest {
		return managed.ErrInvalid
	}

	switch q.Reason {
	case "authority_refused", "request_binding_mismatch", "invocation_closed":
	default:
		return managed.ErrInvalid
	}

	key, err := providerStepID(q.Permission.Step)
	if err != nil {
		return err
	}

	return r.controller(ctx, "step:"+key, []managed.Key{q.Permission.Step.Key}, func(tx ControllerTransaction) error {
		t, e := tx.Read(ctx, q.Permission.Step.Key)
		if e != nil {
			return e
		}

		v, e := providerAttemptHeld(ctx, tx, t, q.Permission.Step)
		if e != nil {
			return e
		}

		if v.ref != q.AttemptRef || v.descriptor.ProviderOperationID != q.Permission.Step.ProviderOperationID || v.descriptor.ActionID != q.Permission.Step.ActionID {
			return managed.ErrAuthority
		}

		stored, e := controllerRead[managed.ProviderStepRecord](ctx, tx, "step", t.TenantID, key)
		if e != nil {
			return e
		}

		if !reflect.DeepEqual(stored.Permission, q.Permission) {
			return managed.ErrConflict
		}

		existing, e := attemptRead[managed.ProviderStepFailure](ctx, tx, "step_failure", t.TenantID, key, managed.MaxAttemptSettlement)
		if e == nil {
			if !reflect.DeepEqual(existing, q) {
				return managed.ErrConflict
			}

			if stored.Result != nil && stored.Result.Outcome == managed.PortTerminal {
				return managed.ErrConflict
			}

			return nil
		}

		if !controllerAbsent(e) {
			return e
		}

		if q.ObservedAt.After(tx.Now()) || stored.Result != nil && stored.Result.Outcome == managed.PortTerminal {
			return managed.ErrConflict
		}

		if v.state.SettlementDigest != "" {
			return managed.ErrConflict
		}

		index, e := attemptProviderIndex(t, v, q.Permission.Step)
		if e != nil {
			return e
		}

		_, association, e := attemptProviderRead(ctx, tx, t, v, q.Permission.Step, index)
		if e != nil {
			return e
		}

		association.FailureDigest, e = attemptHash(attemptFailureDomain, q)
		if e != nil {
			return e
		}

		if e = attemptWrite(ctx, tx, "step_failure", t.TenantID, key, q, true, managed.MaxAttemptSettlement); e != nil {
			return e
		}

		return attemptMaintainProvider(ctx, tx, &v, key, index, association, attemptFailureBit, false)
	})
}

// ReadManagedProviderStepFailure validates the same original permission and attempt.
func (r *Repository) ReadManagedProviderStepFailure(ctx context.Context, q managed.ProviderStepKey) (managed.ProviderStepFailure, error) {
	var out managed.ProviderStepFailure

	key, err := providerStepID(q)
	if err != nil {
		return out, err
	}

	err = r.controller(ctx, "step:"+key, []managed.Key{q.Key}, func(tx ControllerTransaction) error {
		t, e := tx.Read(ctx, q.Key)
		if e != nil {
			return e
		}

		v, e := providerAttemptHeld(ctx, tx, t, q)
		if e != nil {
			return e
		}

		out, e = attemptRead[managed.ProviderStepFailure](ctx, tx, "step_failure", q.TenantID, key, managed.MaxAttemptSettlement)
		if e != nil {
			return e
		}

		stored, e := controllerRead[managed.ProviderStepRecord](ctx, tx, "step", q.TenantID, key)
		if e != nil {
			return e
		}

		if out.AttemptRef != v.ref || !reflect.DeepEqual(stored.Permission, out.Permission) || stored.Result != nil && stored.Result.Outcome == managed.PortTerminal {
			return managed.ErrConflict
		}

		return nil
	})

	return out, err
}

// Callers use the completed readAttempt view before their first write:
// AuthorizeManagedAttemptCommand, attemptLegacyGuard's fresh callback and the
// held quiescence batch transition. Markers
// alone are insufficient; readAttempt validates every slot and exact result first.
func attemptReserve(t *managed.Target, v attemptView, cost uint64, terminal bool) error {
	if !v.validated {
		return managed.ErrUnavailable
	}

	reserve := uint64(8)

	for _, digest := range []string{v.state.AdmissionDigest, v.state.RevocationDigest, v.state.SettlementDigest, v.state.TerminalDigest} {
		if digest != "" {
			reserve--
		}
	}

	positiveDeletion := false

	if v.state.AdmissionDigest != "" {
		for _, mask := range v.state.ProviderSlots {
			if mask&attemptPositiveBit != 0 {
				positiveDeletion = true
			}
		}
	}

	for i, binding := range t.Reservations {
		if binding.Phase == managed.Revoked {
			continue
		}

		mask := v.state.BindingSlots[i]
		if positiveDeletion && mask&(attemptBeginBit|attemptFenceBit) != (attemptBeginBit|attemptFenceBit) {
			return managed.ErrUnavailable
		}

		for _, bit := range []byte{attemptBeginBit, attemptAbortCommandBit, attemptFinishCommandBit, attemptFenceBit, attemptWitnessBit, attemptAbortBit, attemptFinishBit} {
			if positiveDeletion && (bit == attemptAbortCommandBit || bit == attemptWitnessBit || bit == attemptAbortBit) {
				continue
			}

			if mask&bit == 0 {
				reserve++
			}
		}
	}

	steps, err := attemptSteps(t, v)
	if err != nil {
		return err
	}

	for i := range steps {
		if v.state.ProviderSlots[i]&attemptPermissionBit == 0 {
			reserve++
		}
	}

	if terminal {
		reserve = 0
	}

	if cost > math.MaxInt64 || reserve > math.MaxInt64-cost || t.Revision > math.MaxInt64-cost-reserve {
		return managed.ErrCapacity
	}

	return nil
}

func attemptLegacyGuard(ctx context.Context, tx ControllerTransaction, t *managed.Target, kind string, _ time.Time) error {
	if tx == nil {
		if fixedAttemptProfile(t) && attemptRemovalKind(kind) {
			return managed.ErrUnsupported
		}

		return nil
	}

	scope, err := attemptScope(t.Key)
	if err != nil {
		return err
	}

	_, lookup := tx.ReadRecord(ctx, "removal_attempt_current", scope, "current")
	if controllerAbsent(lookup) {
		if _, err = tx.ReadRecord(ctx, "removal_attempt_root", scope, "root"); !controllerAbsent(err) {
			if err != nil {
				return err
			}

			return managed.ErrUnavailable
		}

		if fixedAttemptProfile(t) && attemptRemovalKind(kind) && kind != "tombstone_unissued" {
			return managed.ErrBlocked
		}

		return attemptAbsentAnchor(ctx, tx, t)
	}

	if lookup != nil {
		return lookup
	}

	v, err := selectedAttempt(ctx, tx, t)
	if err != nil {
		return err
	}

	if attemptRemovalKind(kind) || kind == "drain" {
		return managed.ErrBlocked
	}

	if kind == "provider_step" {
		if v.state.RevocationDigest != "" || v.state.SettlementDigest != "" {
			return managed.ErrBlocked
		}
	}

	cost := uint64(1)
	if kind == "provider_step" {
		cost = 0
	}

	return attemptReserve(t, v, cost, false)
}
func attemptRemovalKind(kind string) bool {
	switch kind {
	case "fence", "admit", "issue_delete", "revoke_delete", "settle", "abort", "finish", "tombstone", "tombstone_unissued":
		return true
	}

	return false
}

var _ managed.RemovalAttemptStore = (*Repository)(nil)
var _ managed.ProviderStepFailureAuthority = (*Repository)(nil)
var _ managed.HistoricalSettlementReader = (*Repository)(nil)

func (v attemptEvent) MarshalJSON() ([]byte, error) {
	type plain attemptEvent

	return json.Marshal(struct {
		*plain

		Receipt managed.WireReceipt `json:"receipt"`
	}{(*plain)(&v), managed.ReceiptWire(v.Receipt)})
}
func (v *attemptEvent) UnmarshalJSON(raw []byte) error {
	type plain attemptEvent

	var value plain

	wire := struct {
		*plain

		Receipt managed.WireReceipt `json:"receipt"`
	}{plain: &value}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&wire); err != nil {
		return managed.ErrUnavailable
	}

	receipt, err := wire.Receipt.Value()
	if err != nil {
		return err
	}

	value.Receipt = receipt
	*v = attemptEvent(value)

	canonical, err := v.MarshalJSON()
	if err != nil || !bytes.Equal(canonical, raw) {
		return managed.ErrUnavailable
	}

	return nil
}

func attemptStoredFactDigest(kind string, raw []byte) (string, error) {
	switch kind {
	case "removal_attempt":
		return attemptHash(attemptDescriptorDomain, json.RawMessage(raw))
	case "removal_command":
		return attemptHash(attemptCommandDomain, json.RawMessage(raw))
	default:
		return attemptFact(kind, json.RawMessage(raw))
	}
}
func attemptMutationFact(v attemptView, input any) (string, string, error) {
	kind := ""

	var rid id.ID

	commandKind := ""

	switch q := input.(type) {
	case managed.AttemptCommandRequest:
		kind = "removal_command"
		rid = q.ReservationID
		commandKind = q.Kind
	case managed.AttemptFenceRequest:
		kind = "removal_fence"
		rid = q.ReservationID
	case managed.AttemptAdmissionRequest:
		kind = "removal_admission"
	case managed.AttemptSettlementRequest:
		kind = "removal_settlement"
	case managed.AttemptBindingSettlementRequest:
		kind = "removal_binding_settlement"
		rid = q.ReservationID
	case managed.AttemptAbortRequest:
		kind = "removal_abort"
		rid = q.ReservationID
	case managed.AttemptFinishRequest:
		kind = "removal_finish"
		rid = q.ReservationID
	case managed.AttemptRequest:
		if v.state.Phase != "removed" {
			kind = "removal_revocation"
		}
	}

	if kind == "" {
		return "", "", nil
	}

	if rid.IsNil() {
		return kind, v.ref.AttemptID.String(), nil
	}

	if commandKind == "" {
		commandKind = "binding"
	}

	slot, err := attemptSlot(attemptBinding(v, rid), commandKind)

	return kind, slot, err
}

func attemptAbsentAnchor(ctx context.Context, tx ControllerTransaction, t *managed.Target) error {
	projection, err := controllerRead[managed.ProjectionReceipt](ctx, tx, "target_projection", t.TenantID, t.InstanceID.String())
	if controllerAbsent(err) {
		return nil
	}

	if err != nil {
		return err
	}

	operation, err := acceptedOperationBase(ctx, tx, managed.OperationKey{Key: t.Key, ActionID: projection.ActionID})
	if err != nil {
		return err
	}

	if operation.RemovalAttemptRoot != nil {
		return managed.ErrUnavailable
	}

	return nil
}
func attemptIntegrity(ctx context.Context, tx ControllerTransaction, t *managed.Target) error {
	if tx == nil {
		return nil
	}

	scope, err := attemptScope(t.Key)
	if err != nil {
		return err
	}

	_, err = tx.ReadRecord(ctx, "removal_attempt_current", scope, "current")
	if controllerAbsent(err) {
		if _, rootErr := tx.ReadRecord(ctx, "removal_attempt_root", scope, "root"); !controllerAbsent(rootErr) {
			if rootErr != nil {
				return rootErr
			}

			return managed.ErrUnavailable
		}

		return attemptAbsentAnchor(ctx, tx, t)
	}

	if err != nil {
		return err
	}

	_, err = selectedAttempt(ctx, tx, t)

	return err
}

const (
	attemptBeginBit         byte = 1
	attemptAbortCommandBit  byte = 2
	attemptFinishCommandBit byte = 4
	attemptFenceBit         byte = 8
	attemptAbortBit         byte = 16
	attemptFinishBit        byte = 32
	attemptWitnessBit       byte = 64
	attemptPermissionBit    byte = 1
	attemptPositiveBit      byte = 2
	attemptUncertaintyBit   byte = 4
	attemptFailureBit       byte = 8
)

type attemptProviderSlot struct {
	SchemaVersion         managed.Decimal     `json:"schema_version"`
	AttemptRef            managed.AttemptRef  `json:"attempt_ref"`
	ProviderOperationID   id.ID               `json:"provider_operation_id"`
	Index                 managed.Decimal     `json:"index"`
	PermissionDigest      string              `json:"permission_digest"`
	Receipt               managed.WireReceipt `json:"receipt"`
	PositiveDigest        string              `json:"positive_digest,omitempty"`
	FailureDigest         string              `json:"failure_digest,omitempty"`
	LastUncertaintyDigest string              `json:"last_uncertainty_digest,omitempty"`
}

func attemptMarkMutation(v *attemptView, input any) error {
	var (
		rid id.ID
		bit byte
	)

	switch q := input.(type) {
	case managed.AttemptCommandRequest:
		rid = q.ReservationID
		switch q.Kind {
		case managed.AttemptBegin:
			bit = attemptBeginBit
		case managed.AttemptAbort:
			bit = attemptAbortCommandBit
		case managed.AttemptFinish:
			bit = attemptFinishCommandBit
		default:
			return managed.ErrInvalid
		}
	case managed.AttemptFenceRequest:
		rid = q.ReservationID
		bit = attemptFenceBit
	case managed.AttemptBindingSettlementRequest:
		rid = q.ReservationID
		bit = attemptWitnessBit
	case managed.AttemptAbortRequest:
		rid = q.ReservationID
		bit = attemptAbortBit
	case managed.AttemptFinishRequest:
		rid = q.ReservationID
		bit = attemptFinishBit
	default:
		return nil
	}

	for i, binding := range v.descriptor.ReservationIDs {
		if binding == rid {
			if v.state.BindingSlots[i]&bit != 0 {
				return managed.ErrConflict
			}

			v.state.BindingSlots[i] |= bit

			return nil
		}
	}

	return managed.ErrAuthority
}

func attemptPresence(ctx context.Context, tx ControllerTransaction, kind, scope, key string, wanted bool) error {
	_, err := tx.ReadRecord(ctx, kind, scope, key)
	if controllerAbsent(err) {
		if wanted {
			return managed.ErrUnavailable
		}

		return nil
	}

	if err != nil {
		return err
	}

	if !wanted {
		return managed.ErrUnavailable
	}

	return nil
}

func attemptLocalFact(ctx context.Context, tx ControllerTransaction, v attemptView, localID id.ID, kind, key string, value any) error {
	if localID.Prefix() != managed.PrefixOperation {
		return managed.ErrUnavailable
	}

	event, err := attemptEventRead(ctx, tx, v, localID)
	if err != nil {
		return err
	}

	hash, err := attemptFact(kind, value)
	if err != nil {
		return err
	}

	if event.FactKind == kind && event.FactKey == key && event.FactDigest == hash {
		return nil
	}
	// Nondeletion settlement atomically records its permanent cutoff under the same acceptance.
	if kind == "removal_revocation" && event.Kind == "attempt_settle" && event.FactKind == "removal_settlement" && event.FactKey == v.ref.AttemptID.String() {
		rev, ok := value.(managed.AttemptRevocation)
		if !ok {
			return managed.ErrUnavailable
		}

		settled, err := attemptRead[managed.AttemptSettlement](ctx, tx, "removal_settlement", v.scope, event.FactKey, managed.MaxAttemptSettlement)
		if err != nil {
			return err
		}

		if settled.LocalOperationID == localID && settled.Disposition == managed.SettledWithoutDeletion && rev.Disposition == settled.Disposition && rev.SettlementDigest == event.FactDigest && rev.CoverageDigest == settled.CoverageDigest && v.state.RevocationDigest == hash {
			return nil
		}
	}

	return managed.ErrUnavailable
}

func attemptValidateSlots(ctx context.Context, tx ControllerTransaction, t *managed.Target, v attemptView) error {
	if len(v.state.BindingSlots) != 64 || len(v.state.ProviderSlots) != 33 {
		return managed.ErrUnavailable
	}

	for i, mask := range v.state.BindingSlots {
		if mask&128 != 0 || i >= len(v.descriptor.ReservationIDs) && mask != 0 {
			return managed.ErrUnavailable
		}

		if i >= len(v.descriptor.ReservationIDs) {
			continue
		}

		rid := v.descriptor.ReservationIDs[i]
		k := attemptBinding(v, rid)

		binding, err := reservation(t, rid)
		if err != nil {
			return err
		}

		if binding.Phase == managed.Revoked && mask != 0 {
			return managed.ErrUnavailable
		}

		slot, err := attemptSlot(k, "binding")
		if err != nil {
			return err
		}
		{
			record, err := attemptBindingRead(ctx, tx, t, v, k)
			if err != nil {
				return err
			}

			var observed byte

			for _, command := range record.Commands {
				switch command.Key.Kind {
				case managed.AttemptBegin:
					observed |= attemptBeginBit
				case managed.AttemptAbort:
					observed |= attemptAbortCommandBit
				case managed.AttemptFinish:
					observed |= attemptFinishCommandBit
				}
			}

			if record.Fence != nil {
				observed |= attemptFenceBit
			}

			if record.Abort != nil {
				observed |= attemptAbortBit
			}

			if record.Finish != nil {
				observed |= attemptFinishBit
			}

			if record.Witness != nil {
				observed |= attemptWitnessBit
			}

			if observed != mask {
				return managed.ErrUnavailable
			}

			for _, command := range record.Commands {
				commandSlot, e := attemptSlot(k, command.Key.Kind)
				if e != nil {
					return e
				}

				event, e := attemptEventRead(ctx, tx, v, command.AuthorizationID)
				if e != nil {
					return e
				}

				hash, e := attemptHash(attemptCommandDomain, command)
				if e != nil {
					return e
				}

				if event.FactKind != "removal_command" || event.FactKey != commandSlot || event.FactDigest != hash {
					return managed.ErrUnavailable
				}
			}

			if record.Fence != nil {
				if err = attemptLocalFact(ctx, tx, v, record.Fence.LocalOperationID, "removal_fence", slot, *record.Fence); err != nil {
					return err
				}
			}

			if record.Abort != nil {
				if err = attemptLocalFact(ctx, tx, v, record.Abort.LocalOperationID, "removal_abort", slot, *record.Abort); err != nil {
					return err
				}
			}

			if record.Finish != nil {
				if err = attemptLocalFact(ctx, tx, v, record.Finish.LocalOperationID, "removal_finish", slot, *record.Finish); err != nil {
					return err
				}
			}

			if record.Witness != nil {
				if err = attemptLocalFact(ctx, tx, v, record.Witness.LocalOperationID, "removal_binding_settlement", slot, *record.Witness); err != nil {
					return err
				}
			}
		}
	}

	for _, entry := range []struct{ kind, digest string }{{"removal_admission", v.state.AdmissionDigest}, {"removal_revocation", v.state.RevocationDigest}, {"removal_settlement", v.state.SettlementDigest}} {
		recordKey := v.ref.AttemptID.String()
		if err := attemptPresence(ctx, tx, entry.kind, v.scope, recordKey, entry.digest != ""); err != nil {
			return err
		}

		if entry.digest == "" {
			continue
		}

		var (
			value    any
			localID  id.ID
			ref      managed.AttemptRef
			provider id.ID
		)

		switch entry.kind {
		case "removal_admission":
			fact, err := attemptRead[managed.AttemptAdmission](ctx, tx, entry.kind, v.scope, recordKey, managed.MaxAttemptFact)
			if err != nil {
				return err
			}

			value = fact
			localID = fact.LocalOperationID
			ref = fact.AttemptRef
			provider = fact.ProviderOperationID
		case "removal_revocation":
			fact, err := attemptRead[managed.AttemptRevocation](ctx, tx, entry.kind, v.scope, recordKey, managed.MaxAttemptFact)
			if err != nil {
				return err
			}

			value = fact
			localID = fact.LocalOperationID
			ref = fact.AttemptRef
			provider = fact.ProviderOperationID
		case "removal_settlement":
			fact, err := attemptRead[managed.AttemptSettlement](ctx, tx, entry.kind, v.scope, recordKey, managed.MaxAttemptSettlement)
			if err != nil {
				return err
			}

			value = fact
			localID = fact.LocalOperationID
			ref = fact.AttemptRef
			provider = fact.ProviderOperationID
		}

		hash, err := attemptFact(entry.kind, value)
		if err != nil {
			return err
		}

		if hash != entry.digest || ref != v.ref || provider != v.descriptor.ProviderOperationID {
			return managed.ErrUnavailable
		}

		if err = attemptLocalFact(ctx, tx, v, localID, entry.kind, recordKey, value); err != nil {
			return err
		}
	}

	steps, err := attemptSteps(t, v)
	if err != nil {
		return err
	}

	for i, mask := range v.state.ProviderSlots {
		if mask&240 != 0 || i >= len(steps) && mask != 0 {
			return managed.ErrUnavailable
		}

		if i >= len(steps) {
			continue
		}

		if _, _, err = attemptProviderRead(ctx, tx, t, v, steps[i], i); err != nil {
			return err
		}
	}

	return nil
}

func attemptProviderRead(ctx context.Context, tx ControllerTransaction, t *managed.Target, v attemptView, step managed.ProviderStepKey, index int) (managed.ProviderStepRecord, attemptProviderSlot, error) {
	var (
		record      managed.ProviderStepRecord
		association attemptProviderSlot
	)
	if index < 0 || index >= len(v.state.ProviderSlots) {
		return record, association, managed.ErrUnavailable
	}

	mask := v.state.ProviderSlots[index]
	if mask&240 != 0 || mask&attemptPermissionBit == 0 && mask != 0 || mask&attemptPositiveBit != 0 && mask&attemptFailureBit != 0 {
		return record, association, managed.ErrUnavailable
	}

	key, err := providerStepID(step)
	if err != nil {
		return record, association, err
	}

	for _, entry := range []struct {
		kind, scope string
		bit         byte
	}{{"step", t.TenantID, attemptPermissionBit}, {"removal_provider_slot", v.scope, attemptPermissionBit}, {"step_failure", t.TenantID, attemptFailureBit}} {
		if err = attemptPresence(ctx, tx, entry.kind, entry.scope, key, mask&entry.bit != 0); err != nil {
			return record, association, err
		}
	}

	if mask == 0 {
		return record, association, nil
	}

	record, err = controllerRead[managed.ProviderStepRecord](ctx, tx, "step", t.TenantID, key)
	if err != nil {
		return record, association, err
	}

	association, err = attemptRead[attemptProviderSlot](ctx, tx, "removal_provider_slot", v.scope, key, managed.MaxAttemptAssociation)
	if err != nil {
		return record, association, err
	}

	expectedIndex := managed.Decimal(strconv.Itoa(index))

	raw, err := json.Marshal(record.Permission)
	if err != nil {
		return record, association, err
	}

	resource := step.Service
	if resource == "" {
		resource = "network"
	}

	expectedFence := ""

	if v.descriptor.Generation == "1" {
		if t.Deletion != nil {
			expectedFence = t.Deletion.FenceDigest
		}
	} else {
		admission, e := attemptRead[managed.AttemptAdmission](ctx, tx, "removal_admission", v.scope, v.ref.AttemptID.String(), managed.MaxAttemptFact)
		if e != nil {
			return record, association, e
		}

		expectedFence = admission.FenceSetDigest
	}

	if association.SchemaVersion != "1" || association.AttemptRef != v.ref || association.ProviderOperationID != v.descriptor.ProviderOperationID || association.Index != expectedIndex || association.PermissionDigest != managed.Digest(raw) || record.Permission.Step != step || record.Permission.ResourceID != t.Resources[resource] || record.Permission.SpecDigest != t.Manifest.SpecDigest || record.Permission.Physical != t.Physical || record.Permission.Seal != t.Seal || record.Permission.CreateProfile != "ctrlplane.managed.create.v1" || expectedFence == "" || record.Permission.FenceDigest != expectedFence {
		return record, association, managed.ErrUnavailable
	}

	receipt, err := association.Receipt.Value()
	if err != nil {
		return record, association, err
	}

	if receipt.ID != record.Permission.CommandID || receipt.Revision != record.Permission.Revision || !receipt.AcceptedAt.Equal(record.Permission.IssuedAt) {
		return record, association, managed.ErrUnavailable
	}

	if err = attemptReceiptCheck(ctx, tx, t.Key, receipt); err != nil {
		return record, association, err
	}

	if (association.PositiveDigest != "") != (mask&attemptPositiveBit != 0) || (association.FailureDigest != "") != (mask&attemptFailureBit != 0) || (association.LastUncertaintyDigest != "") != (mask&attemptUncertaintyBit != 0) {
		return record, association, managed.ErrUnavailable
	}

	if record.Result != nil {
		if !reflect.DeepEqual(record.Result.Permission, record.Permission) {
			return record, association, managed.ErrUnavailable
		}

		switch record.Result.Outcome {
		case managed.PortTerminal:
			raw, err = json.Marshal(*record.Result)
			if err != nil {
				return record, association, err
			}

			if mask&attemptPositiveBit == 0 || association.PositiveDigest != managed.Digest(raw) || record.Result.ResourceID != record.Permission.ResourceID {
				return record, association, managed.ErrUnavailable
			}

			historical, e := controllerRead[managed.ProviderStepResult](ctx, tx, "step_history", t.TenantID, association.PositiveDigest)
			if e != nil {
				return record, association, e
			}

			if !reflect.DeepEqual(historical, *record.Result) {
				return record, association, managed.ErrUnavailable
			}
		case managed.PortUnavailable:
			fingerprint, e := controllerFingerprint(*record.Result)
			if e != nil {
				return record, association, e
			}

			if mask&attemptUncertaintyBit == 0 || mask&attemptPositiveBit != 0 || fingerprint != association.LastUncertaintyDigest {
				return record, association, managed.ErrUnavailable
			}
		default:
			return record, association, managed.ErrUnavailable
		}
	} else if mask&(attemptPositiveBit|attemptUncertaintyBit) != 0 {
		return record, association, managed.ErrUnavailable
	}

	if mask&attemptUncertaintyBit != 0 {
		uncertainty, e := controllerRead[managed.ProviderStepResult](ctx, tx, "step_history", t.TenantID, association.LastUncertaintyDigest)
		if e != nil {
			return record, association, e
		}

		fingerprint, e := controllerFingerprint(uncertainty)
		if e != nil {
			return record, association, e
		}

		if fingerprint != association.LastUncertaintyDigest || uncertainty.Outcome != managed.PortUnavailable || !reflect.DeepEqual(uncertainty.Permission, record.Permission) {
			return record, association, managed.ErrUnavailable
		}
	}

	if mask&attemptFailureBit != 0 {
		failure, e := attemptRead[managed.ProviderStepFailure](ctx, tx, "step_failure", t.TenantID, key, managed.MaxAttemptSettlement)
		if e != nil {
			return record, association, e
		}

		hash, e := attemptHash(attemptFailureDomain, failure)
		if e != nil {
			return record, association, e
		}

		if hash != association.FailureDigest || failure.AttemptRef != v.ref || !reflect.DeepEqual(failure.Permission, record.Permission) {
			return record, association, managed.ErrUnavailable
		}
	}

	return record, association, nil
}

func attemptProviderIndex(t *managed.Target, v attemptView, key managed.ProviderStepKey) (int, error) {
	steps, err := attemptSteps(t, v)
	if err != nil {
		return 0, err
	}

	for i, step := range steps {
		if step == key {
			return i, nil
		}
	}

	return 0, managed.ErrAuthority
}

// This finite maintenance path changes no parent or attempt revision.
func attemptMaintainProvider(ctx context.Context, tx ControllerTransaction, v *attemptView, key string, index int, association attemptProviderSlot, bit byte, insert bool) error {
	if index < 0 || index >= len(v.state.ProviderSlots) || bit&240 != 0 {
		return managed.ErrInvalid
	}

	if err := attemptWrite(ctx, tx, "removal_provider_slot", v.scope, key, association, insert, managed.MaxAttemptAssociation); err != nil {
		return err
	}

	v.state.ProviderSlots[index] |= bit

	return attemptWrite(ctx, tx, "removal_attempt_state", v.scope, v.ref.AttemptID.String(), v.state, false, managed.MaxAttemptFact)
}

func (v *attemptCheckedReads) ApplyQuiescenceObservationBatch(context.Context, managed.QuiescenceBatchAcceptanceRequest) (managed.QuiescenceBatchReceipt, error) {
	return managed.QuiescenceBatchReceipt{}, managed.ErrBlocked
}

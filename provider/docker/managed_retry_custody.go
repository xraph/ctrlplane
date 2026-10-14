package docker

import (
	"context"
	"encoding/json"
	"math"
	"reflect"

	"github.com/xraph/ctrlplane/managed"
)

// ManagedDockerRetryEvidence retains the accepted source and exact retry invocation.
// Marker is required before preparing transport custody. None of these records grants
// another send; the configured authority must independently authenticate all of them.
type ManagedDockerRetryEvidence struct {
	Root          managed.RecoveryRecord[managed.ProviderStepRetryRoot]          `json:"root"`
	Permission    managed.RecoveryRecord[managed.ProviderInvocationPermission]   `json:"permission"`
	Authorization managed.RecoveryRecord[managed.ProviderStepRetryAuthorization] `json:"authorization"`
	Marker        managed.RecoveryRecord[managed.ProviderInvocationMarker]       `json:"marker"`
}

// ManagedDockerRetryAuthority explicitly opts the configured authority into retries.
// VerifyRetry must reload the exact accepted records and independent receipts from
// the configured core store, including the retained retry request/event, predecessor
// closure certificate, root selection and native proof/commit associations. It may
// inspect historical records but must never manufacture fresh authority from them.
// AdmitRetry must additionally authenticate current native binding and consume the
// volatile grant from a newly accepted marker before transport. Reopening a saved
// marker, journal or receipt cannot recreate this grant. An error may consume it.
// These methods supplement full VerifyRequest; no permissive default is supplied.
type ManagedDockerRetryAuthority interface {
	VerifyRetry(ctx context.Context, request ManagedDockerRequest) error
	AdmitRetry(ctx context.Context, request ManagedDockerRequest) error
}

func validateManagedDockerRetry(request ManagedDockerRequest) error {
	evidence := request.Retry
	if evidence == nil || request.Binding == nil || request.Invocation == nil {
		return managed.ErrInvalid
	}

	if _, err := managed.EncodeRecoveryRecord(evidence.Root); err != nil {
		return err
	}

	if _, err := managed.EncodeRecoveryRecord(evidence.Permission); err != nil {
		return err
	}

	if _, err := managed.EncodeRecoveryRecord(evidence.Authorization); err != nil {
		return err
	}

	if _, err := managed.EncodeRecoveryRecord(evidence.Marker); err != nil {
		return err
	}

	root, permission, authorization, marker := evidence.Root.Facts, evidence.Permission.Facts, evidence.Authorization.Facts, evidence.Marker.Facts
	original := request.Binding.Original

	ordinal, err := permission.Ordinal.Uint64()
	if err != nil || ordinal == 0 || ordinal > math.MaxInt64 {
		return managed.ErrInvalid
	}

	if permission.Key.AttemptKey != original.Key.AttemptKey || permission.Key.Step != original.Key.Step || permission.Key.CommandID != request.Permission.CommandID || permission.ProviderCommandDigest != request.Binding.ProviderCommandDigest {
		return managed.ErrAuthority
	}

	if root.Profile != managed.ProviderStepRetryProfile || root.AttemptRef != original.AttemptRef || root.Step != original.Key.Step || root.SpecDigest != original.Permission.SpecDigest || root.ResourceID != original.Permission.ResourceID || !reflect.DeepEqual(root.OriginalPermission, original.Permission) || !reflect.DeepEqual(root.OriginalPermissionReceipt, original.Receipt) || root.ProviderCommandDigest != request.Binding.ProviderCommandDigest {
		return managed.ErrAuthority
	}

	command, err := managed.DecodeProviderEffectCommand(request.Binding.Command)
	if err != nil || root.Incarnation != command.Incarnation {
		return managed.ErrAuthority
	}

	if !reflect.DeepEqual(permission, *request.Invocation) || permission.RootDigest != evidence.Root.Digest || permission.Key.CommandID == original.Permission.CommandID || !reflect.DeepEqual(permission.Permission, request.Permission) || request.Receipt != evidence.Permission.Acceptance.Receipt || evidence.Permission.Acceptance != evidence.Authorization.Acceptance {
		return managed.ErrAuthority
	}

	current := permission.Permission
	expected := original.Permission
	expected.CommandID = current.CommandID
	expected.Digest = current.Digest
	expected.IssuedAt = current.IssuedAt

	expected.Revision = current.Revision
	if current != expected || current.Digest != evidence.Permission.Acceptance.RequestDigest || current.Revision != request.Receipt.Revision || !current.IssuedAt.Equal(request.Receipt.AcceptedAt) {
		return managed.ErrAuthority
	}

	count, err := permission.PermissionProofCount.Uint64()
	if err != nil || count == 0 || count > managed.MaxRecoveryBindings || !dockerObjectID(permission.PermissionProofSetDigest) {
		return managed.ErrInvalid
	}

	if authorization.Key != permission.Key || authorization.RootDigest != evidence.Root.Digest || authorization.Ordinal != permission.Ordinal || authorization.ParentRevision != current.Revision || !authorization.AuthorizedAt.Equal(current.IssuedAt) || !dockerObjectID(authorization.ExpectedCurrentDigest) {
		return managed.ErrAuthority
	}

	attemptRevision, err := authorization.AttemptRevision.Uint64()
	if err != nil || attemptRevision == 0 || attemptRevision > math.MaxInt64 {
		return managed.ErrInvalid
	}

	reference, err := managed.RecoveryAssociationFor(authorization.Permission.Key, evidence.Permission)
	if err != nil || reference != authorization.Permission {
		return managed.ErrAuthority
	}

	previous := authorization.Previous

	previousOrdinal, err := previous.Ordinal.Uint64()
	if err != nil || previousOrdinal >= math.MaxInt64 || previousOrdinal+1 != ordinal || previous.SchemaVersion != "1" || previous.RootDigest != evidence.Root.Digest || previous.CommandID.Prefix() != managed.PrefixOperation || previous.CommandID == current.CommandID || !dockerObjectID(previous.PermissionDigest) {
		return managed.ErrAuthority
	}

	digest, err := managed.RetryPreviousDigest(previous)
	if err != nil || previous.Digest != digest {
		return managed.ErrAuthority
	}

	for _, association := range []managed.RecoveryAssociation{previous.Failure, previous.Closure, previous.Authority} {
		if err = managed.ValidateRecoveryAssociation(association); err != nil {
			return err
		}
	}

	if previous.Failure.Kind != managed.RecoveryFailureRecord || previous.Closure.Kind != managed.RecoveryClosureRecord || previous.Failure.OperationID != previous.Closure.OperationID || previous.Failure.ReceiptDigest != previous.Closure.ReceiptDigest {
		return managed.ErrAuthority
	}

	if previousOrdinal == 0 {
		if previous.CommandID != original.Permission.CommandID {
			return managed.ErrAuthority
		}

		rootReference, referenceErr := managed.RecoveryAssociationFor(previous.Authority.Key, evidence.Root)
		if referenceErr != nil || previous.Authority != rootReference {
			return managed.ErrAuthority
		}
	} else if previous.Authority.Kind != managed.RecoveryAuthorizationRecord || previous.CommandID == original.Permission.CommandID {
		return managed.ErrAuthority
	}

	p, err := current.Value()
	if err != nil {
		return err
	}

	raw, err := json.Marshal(p)
	if err != nil {
		return managed.ErrInvalid
	}

	markerCount, err := marker.BindingCount.Uint64()
	if err != nil || markerCount != count || marker.Key != permission.Key || marker.RootDigest != evidence.Root.Digest || marker.PermissionDigest != managed.Digest(raw) || !dockerObjectID(marker.SelectionDigest) || !dockerObjectID(marker.NativeCommitSetDigest) || !dockerObjectID(marker.CurrentProofSetDigest) || !marker.AcceptedAt.Equal(evidence.Marker.Acceptance.Receipt.AcceptedAt) {
		return managed.ErrAuthority
	}

	rootReceipt, err := evidence.Root.Acceptance.Receipt.Value()
	if err != nil {
		return err
	}

	permissionReceipt, err := request.Receipt.Value()
	if err != nil {
		return err
	}

	markerReceipt, err := evidence.Marker.Acceptance.Receipt.Value()
	if err != nil {
		return err
	}

	if rootReceipt.Revision >= permissionReceipt.Revision || rootReceipt.AcceptedAt.After(permissionReceipt.AcceptedAt) || markerReceipt.Revision <= permissionReceipt.Revision || markerReceipt.AcceptedAt.Before(permissionReceipt.AcceptedAt) || !markerReceipt.LeaseUntil.After(markerReceipt.AcceptedAt) {
		return managed.ErrAuthority
	}

	return nil
}

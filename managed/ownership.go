package managed

import (
	"context"
	"encoding/hex"
	"strings"

	"github.com/xraph/ctrlplane/id"
)

// OwnerKey identifies the immutable workload owner within a tenant.
type OwnerKey struct {
	TenantID string `json:"tenant_id"`
	OwnerID  id.ID  `json:"owner_id"`
}

// Validate refuses incomplete owner scopes before consulting persistence.
func (k OwnerKey) Validate() error {
	if !ownerToken(k.TenantID) || k.OwnerID.Prefix() != id.PrefixWorkload {
		return ErrInvalid
	}

	return nil
}

// OwnerProtectionStore derives ownership from retained immutable target manifests.
// Implementations must share the ordinary workload persistence authority.
type OwnerProtectionStore interface {
	InspectManagedOwnerProtection(ctx context.Context, key OwnerKey) (Protection, error)
}

// InspectOwnerTarget validates a retained row before deriving immutable ownership.
// You must pass the row key separately so malformed documents cannot hide ownership.
func InspectOwnerTarget(t *Target, stored Key, owner OwnerKey) (Protection, error) {
	if t == nil || t.Key != stored || t.TenantID != owner.TenantID || t.InstanceID.Prefix() != id.PrefixInstance || t.Revision == 0 || t.ProvisionOperationID.Prefix() != PrefixOperation || !ownerToken(t.Physical.Provider) || !ownerToken(t.Physical.Placement) || !ownerToken(t.Physical.Project) || !ownerToken(t.Physical.Generation) || t.Manifest.Version == 0 || len(t.Manifest.Members) == 0 || len(t.Manifest.Members) > MaxMembers || Digest(t.Manifest.Spec) != t.Manifest.SpecDigest || len(t.Manifest.Spec) == 0 {
		return Protection{}, ErrAuthority
	}

	switch t.Phase {
	case Open, Sealed, Admitted, DeleteIssued, Deleted:
	default:
		return Protection{}, ErrAuthority
	}

	protected := false
	names := map[string]bool{}
	bindings := map[string]bool{}

	for _, member := range t.Manifest.Members {
		if !ownerToken(member.Name) || !ownerToken(member.Service) || !ownerToken(member.Installation) || !ownerToken(member.Namespace) || !ownerToken(member.Queue) || !ownerToken(member.Build) || !ownerDigest(member.ArtifactDigest) || !ownerDigest(member.ConfigurationDigest) || names[member.Name] || (!member.OwnerID.IsNil() && member.OwnerID.Prefix() != id.PrefixWorkload) {
			return Protection{}, ErrAuthority
		}

		binding := strings.Join([]string{member.Installation, member.Namespace, member.Queue, member.Build, member.Service}, "\x00")
		if bindings[binding] {
			return Protection{}, ErrAuthority
		}

		names[member.Name] = true
		bindings[binding] = true

		if member.OwnerID == owner.OwnerID {
			protected = true
		}
	}

	if protected {
		return Protection{Protected: true, Phase: t.Phase, Revision: t.Revision}, nil
	}

	return Protection{}, nil
}

func ownerToken(value string) bool {
	if len(value) == 0 || len(value) > 256 {
		return false
	}

	for _, c := range value {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && !strings.ContainsRune("-_.:/@+=", c) {
			return false
		}
	}

	return true
}
func ownerDigest(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}

	_, err := hex.DecodeString(value)

	return err == nil
}

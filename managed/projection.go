package managed

import "github.com/xraph/ctrlplane/id"

// ProjectionStage selects a supported authoritative ordinary-row projection.
type ProjectionStage string

// ProjectionPending exposes reserved identities without asserting provider completion.
const ProjectionPending ProjectionStage = "pending"

// ProjectionRequest selects persisted acceptance and parent CAS, never arbitrary rows.
type ProjectionRequest struct {
	Command

	ActionID id.ID           `json:"action_id"`
	Stage    ProjectionStage `json:"stage"`
}

// ProjectionReceipt is the immutable result of atomic row and release association writes.
type ProjectionReceipt struct {
	Receipt

	ActionID   id.ID           `json:"action_id"`
	Target     Key             `json:"target"`
	OwnerID    id.ID           `json:"owner_id,omitzero"`
	ReleaseID  id.ID           `json:"release_id"`
	SpecDigest string          `json:"spec_digest"`
	Stage      ProjectionStage `json:"stage"`
}

// ProjectionOwner returns the single immutable owner supported by this profile.
// Multiple owners or mixed standalone ownership require a separate profile.
func ProjectionOwner(i Identity) (id.ID, error) {
	if len(i.Manifest.Members) == 0 {
		return id.Nil, ErrInvalid
	}

	owner := i.Manifest.Members[0].OwnerID
	if !owner.IsNil() && owner.Prefix() != id.PrefixWorkload {
		return id.Nil, ErrInvalid
	}

	for _, member := range i.Manifest.Members {
		if member.OwnerID != owner {
			return id.Nil, ErrInvalid
		}
	}

	return owner, nil
}

// OwnerReservation permanently binds a fresh owner to its original managed target.
type OwnerReservation struct {
	OwnerID id.ID `json:"owner_id"`
	Origin  Key   `json:"origin"`
}

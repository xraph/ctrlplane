package managed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"time"

	"github.com/xraph/ctrlplane/id"
)

// Bounds limit persisted records and public pages. Exhaustion refuses new work.
const (
	DefaultPage                 = 100
	MaxPage                     = 500
	MaxMembers                  = 32
	MaxReservations             = 64
	MaxEvidence                 = 4096
	MaxDocument                 = 8 << 20
	MaxLease                    = 5 * time.Minute
	PrefixOperation   id.Prefix = "mop"
	PrefixReservation id.Prefix = "mres"
)

// Errors distinguish absent records, conflicts and unavailable protection authority.
var (
	ErrNotFound    = errors.New("managed: target not found")
	ErrConflict    = errors.New("managed: conflicting command or revision")
	ErrInvalid     = errors.New("managed: invalid request")
	ErrBlocked     = errors.New("managed: unresolved lifecycle obligation")
	ErrUnavailable = errors.New("managed: protection authority unavailable")
	ErrUnsupported = errors.New("managed: protection capability unsupported")
	ErrAuthority   = errors.New("managed: ownership or authority mismatch")
	ErrCapacity    = errors.New("managed: record capacity exceeded")
)

// Digest identifies exact non-secret command or evidence bytes.
func Digest(b []byte) string {
	sum := sha256.Sum256(b)

	return hex.EncodeToString(sum[:])
}

// Key names a tenant-owned physical instance. Instance IDs are never reused.
type Key struct {
	TenantID   string `json:"tenant_id"`
	InstanceID id.ID  `json:"instance_id"`
}

// Physical identifies the provider namespace and permanently reserved resource name.
type Physical struct {
	Provider   string `json:"provider"`
	Placement  string `json:"placement"`
	Project    string `json:"project"`
	Generation string `json:"generation"`
}

// Member freezes one permitted binding within the whole-instance manifest.
type Member struct {
	Name                string `json:"name"`
	Service             string `json:"service"`
	OwnerID             id.ID  `json:"owner_id,omitzero"`
	Installation        string `json:"installation"`
	Namespace           string `json:"namespace"`
	Queue               string `json:"queue"`
	Build               string `json:"build"`
	ArtifactDigest      string `json:"artifact_digest"`
	ConfigurationDigest string `json:"configuration_digest"`
}

// Manifest contains the immutable, non-secret provision specification and bindings.
type Manifest struct {
	Version    uint64   `json:"version"`
	Members    []Member `json:"members"`
	Spec       []byte   `json:"spec"`
	SpecDigest string   `json:"spec_digest"`
}

// Identity is allocated before any provisioning or registration call.
type Identity struct {
	Key

	ProvisionOperationID id.ID    `json:"provision_operation_id"`
	Physical             Physical `json:"physical"`
	Manifest             Manifest `json:"manifest"`
}

// Phase closes enrollment independently of the desired mode.
type Phase string

const (
	Open         Phase = "open"
	Sealed       Phase = "sealed_pending"
	Admitted     Phase = "removal_admitted"
	DeleteIssued Phase = "delete_may_have_issued"
	Deleted      Phase = "deleted"
)

// DesiredMode never cancels an outstanding issued operation.
type DesiredMode string

const (
	Active   DesiredMode = "active"
	Paused   DesiredMode = "paused"
	Retiring DesiredMode = "retiring"
)

// ReservationPhase preserves uncertainty after permission to issue was committed.
type ReservationPhase string

const (
	Reserved      ReservationPhase = "reserved"
	MayHaveIssued ReservationPhase = "may_have_issued"
	Accepted      ReservationPhase = "accepted"
	Revoked       ReservationPhase = "revoked_before_issue"
)

// Command fences local writes. Exact accepted replay returns its original receipt.
type Command struct {
	Key

	ID         id.ID  `json:"id"`
	Revision   uint64 `json:"revision"`
	LeaseEpoch uint64 `json:"lease_epoch"`
	Owner      string `json:"owner"`
}

// Receipt is the immutable result of a local conditional command.
type Receipt struct {
	ID         id.ID     `json:"id"`
	Digest     string    `json:"digest"`
	Revision   uint64    `json:"revision"`
	LeaseEpoch uint64    `json:"lease_epoch"`
	AcceptedAt time.Time `json:"accepted_at"`
	LeaseUntil time.Time `json:"lease_until"`
}

// Registration freezes the exact command sent to a trusted remote worker host.
type Registration struct {
	ID            id.ID  `json:"id"`
	Member        string `json:"member"`
	RuntimeID     string `json:"runtime_id"`
	InstanceID    string `json:"instance_id"`
	HostID        string `json:"host_id"`
	RequestID     string `json:"request_id"`
	Command       []byte `json:"command"`
	CommandDigest string `json:"command_digest"`
}

// Fence preserves the full opaque removal fence and its off-instance survivor set.
type Fence struct {
	CompatibilityEvidence []byte    `json:"compatibility_evidence"`
	Epoch                 uint64    `json:"epoch"`
	Evidence              []byte    `json:"evidence"`
	Digest                string    `json:"digest"`
	ValidUntil            time.Time `json:"valid_until"`
	Survivors             []string  `json:"survivors"`
}

// Drain identifies a single accepted process drain with an immutable deadline.
type Drain struct {
	OperationID string    `json:"operation_id"`
	Deadline    time.Time `json:"deadline"`
	Complete    bool      `json:"complete"`
	Quiescent   bool      `json:"quiescent"`
	CompletedAt time.Time `json:"completed_at"`
	Evidence    []byte    `json:"evidence"`
}

// Reservation retains accepted, revoked and unresolved incarnation obligations.
type Reservation struct {
	Registration

	Phase    ReservationPhase `json:"phase"`
	Receipt  []byte           `json:"receipt"`
	Unknown  bool             `json:"unknown"`
	Fence    *Fence           `json:"fence,omitempty"`
	Drain    *Drain           `json:"drain,omitempty"`
	Finished []byte           `json:"finished,omitempty"`
	Abort    *AbortProof      `json:"abort,omitempty"`
}

// Disposition requires actual provider settlement except for atomic pre-issue revocation.
type Disposition string

const (
	NoResourcesCreated     Disposition = "no_resources_created"
	NotIssued              Disposition = "not_issued"
	Rejected               Disposition = "rejected"
	Cancelled              Disposition = "cancelled"
	Fenced                 Disposition = "fenced"
	SettledWithoutDeletion Disposition = "settled_without_deletion"
	Removed                Disposition = "deleted"
)

// Settlement is trusted host evidence, not a claim inferred from an existence probe.
type Settlement struct {
	OperationID id.ID       `json:"operation_id"`
	FenceDigest string      `json:"fence_digest"`
	Disposition Disposition `json:"disposition"`
	Verifier    string      `json:"verifier"`
	Evidence    []byte      `json:"evidence"`
	SettledAt   time.Time   `json:"settled_at"`
}

// Deletion retains a stable external operation identity and its full fence-set digest.
type Deletion struct {
	IssuedAt    time.Time   `json:"issued_at"`
	OperationID id.ID       `json:"operation_id"`
	FenceDigest string      `json:"fence_digest"`
	Verifier    string      `json:"verifier"`
	Issued      bool        `json:"issued"`
	Settlement  *Settlement `json:"settlement,omitempty"`
}

// AbortProof binds a recovered abort receipt and fresh original-binding query proof.
type AbortProof struct {
	FenceDigest string    `json:"fence_digest"`
	VerifiedAt  time.Time `json:"verified_at"`
	ValidUntil  time.Time `json:"valid_until"`
	Evidence    []byte    `json:"evidence"`
	Receipt     []byte    `json:"receipt"`
}

// Provision retains creation uncertainty until the exact provider operation settles.
type Provision struct {
	Issued   bool   `json:"issued"`
	Complete bool   `json:"complete"`
	Revoked  bool   `json:"revoked"`
	Receipt  []byte `json:"receipt,omitempty"`
}

// Target is the complete bounded obligation set, updated under one physical-instance lock.
type Target struct {
	Identity

	TerminalDisposition    Disposition       `json:"terminal_disposition,omitempty"`
	Provision              Provision         `json:"provision"`
	ReplacementOperationID id.ID             `json:"replacement_operation_id,omitzero"`
	CreatedAt              time.Time         `json:"created_at"`
	Revision               uint64            `json:"revision"`
	Desired                DesiredMode       `json:"desired"`
	DesiredRevision        uint64            `json:"desired_revision"`
	Phase                  Phase             `json:"phase"`
	Seal                   uint64            `json:"seal"`
	LeaseOwner             string            `json:"lease_owner"`
	LeaseEpoch             uint64            `json:"lease_epoch"`
	LeaseUntil             time.Time         `json:"lease_until"`
	Resources              map[string]string `json:"resources,omitempty"`
	Reservations           []Reservation     `json:"reservations"`
	Deletion               *Deletion         `json:"deletion,omitempty"`
	Replacement            *Identity         `json:"replacement,omitempty"`
}

// Protection distinguishes authoritative absence from unavailable capability or failed reads.
type Protection struct {
	Protected bool   `json:"protected"`
	Phase     Phase  `json:"phase,omitempty"`
	Revision  uint64 `json:"revision"`
}

// ProtectionStore is independent of controller startup and optional lifecycle ports.
// Implementations must read the same authority as instance persistence, including tombstones.
type ProtectionStore interface {
	InspectManagedProtection(ctx context.Context, request Key) (Protection, error)
	HasManagedObligations(ctx context.Context, request string) (bool, error)
}

// RequireProtection refuses a wrapper or backend which cannot supply authoritative protection.
func RequireProtection(s any) (ProtectionStore, error) {
	p, ok := s.(ProtectionStore)
	if !ok || p == nil || reflect.ValueOf(p).Kind() == reflect.Pointer && reflect.ValueOf(p).IsNil() {
		return nil, ErrUnsupported
	}

	return p, nil
}

// PageRequest is tenant scoped. Controllers page each explicitly authorized tenant.
type PageRequest struct {
	TenantID string `json:"tenant_id"`
	Cursor   string `json:"cursor,omitempty"`
	Limit    int    `json:"limit"`
}

// Summary omits incarnation commands, receipts and proof material from enumeration.
type Summary struct {
	Key

	Phase            Phase       `json:"phase"`
	Desired          DesiredMode `json:"desired"`
	Revision         uint64      `json:"revision"`
	ReservationCount int         `json:"reservation_count"`
}

// ControllerPageRequest is reserved for an explicitly authorized independent controller.
// It enumerates persisted targets even when tenant metadata has been removed.
type ControllerPageRequest struct {
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit"`
}

// Page returns a bounded stable continuation independent of mutable instance rows.
type Page struct {
	Items      []Summary `json:"items"`
	NextCursor string    `json:"next_cursor,omitempty"`
}

package managed

import (
	"context"
	"time"

	"github.com/xraph/ctrlplane/id"
)

// LeaseRequest acquires or renews a lease with a fresh fenced epoch.
type LeaseRequest struct {
	Command

	Duration time.Duration `json:"duration"`
}

// DesiredRequest changes durable desired state without reopening enrollment.
type DesiredRequest struct {
	Command

	Mode DesiredMode `json:"mode"`
}

// ReserveRequest records a permitted incarnation before any remote issue.
type ReserveRequest struct {
	Command

	Registration Registration `json:"registration"`
}

// ReservationRequest selects an exact persisted reservation.
type ReservationRequest struct {
	Command

	ReservationID id.ID `json:"reservation_id"`
}

// AcceptanceRequest records the exact accepted remote result.
type AcceptanceRequest struct {
	ReservationRequest

	Receipt []byte `json:"receipt"`
}

// FenceRequest captures one immutable binding fence.
type FenceRequest struct {
	ReservationRequest

	Fence Fence `json:"fence"`
}

// DrainRequest captures the original drain identity/deadline or its terminal outcome.
type DrainRequest struct {
	ReservationRequest

	Drain Drain `json:"drain"`
}

// AdmissionRequest allocates the deletion operation only after whole-instance closure.
type AdmissionRequest struct {
	Command

	OperationID id.ID  `json:"operation_id"`
	Verifier    string `json:"verifier"`
}

// SettlementRequest records definitive evidence for the allocated deletion.
type SettlementRequest struct {
	Command

	Settlement Settlement `json:"settlement"`
}

// AbortRequest records an accepted per-binding abort after exact operation settlement.
type AbortRequest struct {
	ReservationRequest

	Proof AbortProof `json:"proof"`
}

// ReplacementRequest freezes the next instance identity before provisioning.
type ReplacementRequest struct {
	Command

	Replacement Identity `json:"replacement"`
}

// ResourcesRequest binds observed exact provider resource IDs once.
type ResourcesRequest struct {
	Command

	OperationID id.ID             `json:"operation_id"`
	Receipt     []byte            `json:"receipt"`
	Resources   map[string]string `json:"resources"`
}

// Store owns the physical-instance CAS. These methods do not authorize external actions.
// Hosts must authenticate, authorize and verify evidence before invoking the capability.
type Store interface {
	ProtectionStore
	CreateManagedTarget(ctx context.Context, request Identity) (*Target, error)
	ReadManagedTarget(ctx context.Context, request Key) (*Target, error)
	ListManagedTargets(ctx context.Context, request PageRequest) (Page, error)
	ListManagedControllerTargets(ctx context.Context, request ControllerPageRequest) (Page, error)
	ClaimManagedLease(ctx context.Context, request LeaseRequest) (Receipt, error)
	SetManagedDesired(ctx context.Context, request DesiredRequest) (Receipt, error)
	IssueManagedProvision(ctx context.Context, request Command) (Receipt, error)
	BindManagedResources(ctx context.Context, request ResourcesRequest) (Receipt, error)
	AllocateManagedReplacement(ctx context.Context, request ReplacementRequest) (Receipt, error)
	ReserveManagedRegistration(ctx context.Context, request ReserveRequest) (Receipt, error)
	IssueManagedRegistration(ctx context.Context, request ReservationRequest) (Receipt, error)
	ObserveManagedUnknown(ctx context.Context, request ReservationRequest) (Receipt, error)
	AcceptManagedRegistration(ctx context.Context, request AcceptanceRequest) (Receipt, error)
	SealManagedTarget(ctx context.Context, request Command) (Receipt, error)
	RecordManagedFence(ctx context.Context, request FenceRequest) (Receipt, error)
	RecordManagedDrain(ctx context.Context, request DrainRequest) (Receipt, error)
	AdmitManagedRemoval(ctx context.Context, request AdmissionRequest) (Receipt, error)
	IssueManagedDeletion(ctx context.Context, request Command) (Receipt, error)
	RevokeManagedDeletion(ctx context.Context, request Command) (Receipt, error)
	SettleManagedDeletion(ctx context.Context, request SettlementRequest) (Receipt, error)
	AbortManagedBinding(ctx context.Context, request AbortRequest) (Receipt, error)
	FinishManagedBinding(ctx context.Context, request AcceptanceRequest) (Receipt, error)
	TombstoneManagedTarget(ctx context.Context, request Command) (Receipt, error)
	TombstoneManagedUnissuedTarget(ctx context.Context, request Command) (Receipt, error)
}

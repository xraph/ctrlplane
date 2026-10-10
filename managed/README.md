# Managed worker persistence

You can persist a physical instance's deployment obligations through `managed.Store`.
Memory and PostgreSQL implement the capability independently of controller options.
`managed.ProtectionStore` exposes protection without requiring worker lifecycle ports.
The aggregate ordinary store interface is unchanged.

This package supplies storage contracts. It does not install service guards, call a
provider, authenticate a caller or verify remote evidence. The host must authorize
each operation and verify the actual runtime, artifact, provider operation and proof
before submitting the typed result. Direct provider handles, SQL access and older
unguarded controllers remain trusted operations outside this protection boundary.

## Identity and progress

Create a target with its tenant, fresh instance TypeID, stable provision operation
TypeID, provider identity, placement, project, generation and immutable manifest.
Provider plus placement must identify the actual provider authority, including its
account/daemon and namespace. A mutable registry alias is insufficient. The project
name is permanently unique within that authority, including after tombstoning.

Claim a conditional lease before advancing the target. Every mutation checks the
parent revision and lease epoch. A successful command returns an immutable receipt.
Retry exactly after a lost reply. Changing the request under an accepted command ID
conflicts. Receipt recovery does not issue another remote call and does not authorize
reusing an old deletion or provisioning permission. Hosts must reconcile the same
external operation, including delayed calls that were already permitted.

Provisioning and registration record potential issuance before a remote call.
Sealing revokes permissions which were never issued and retains outstanding calls.
A missing container, receipt lookup, expired lease or stopped controller cannot
settle an issued operation. A trusted provision result must establish settlement of
the entire creation operation, including network, service and init creation, before
its exact resources are bound. Partial creation remains unresolved. This version also refuses a terminal empty
provider result because it has no qualified producer contract for that disposition.
That is a liveness limitation, distinct from an unknown result.

A sealed provision that was atomically revoked before issuance can close locally.
The store checks that no create was authorized, no resources or issued bindings exist,
and no deletion operation was allocated. It retains a `no_resources_created`
tombstone and the exact local command receipt. Missing resources alone never qualify.

Removal checks the complete bounded obligation set under one lock. Each accepted
binding needs its current opaque query fence, compatibility/retirement evidence and
completed quiescent drain. Fence survivors must be off the target instance. The host
must recheck current remote fences immediately before provider execution; a stored
sample cannot make Docker deletion atomic.

Only atomic pre-issue revocation creates `not_issued` settlement. Other closed
settlement dispositions require verified external facts for the configured verifier,
operation and full fence digest. An abort also needs an original-binding proof
captured at or after settlement and still valid after acquiring the store lock.
Accepted abort keeps enrollment closed. Each binding's finish or abort receipt is
retained independently of later desired-state changes.

A replacement allocation persists its own immutable identity and the allocating
operation ID before provisioning. The next target must reserve its physical ownership
through CreateManagedTarget before any provider call. Allocating a replacement does
not grant ownership of another target's project.

## Bounds and retained receipts

The first profile permits 32 manifest members and 64 total reservations per physical
instance. Historical incarnations count toward the reservation limit. Once full,
new enrollment and restarts fail before issue; use a distinct managed replacement.
Do not drop an old command to make room.

Identifiers are at most 256 bytes and use ASCII letters, digits or `-_.:/@+=`.
Opaque command, specification, evidence and remote receipt fields are each bounded
at 4096 bytes. There are at most 64 survivor identities per binding and 33 bound
provider resource entries. A host must confirm these protocol bounds before it
issues work. Large payloads and secret material do not belong in these fields.

The complete target document has an 8 MiB ceiling. Tests serialize all maximum
fields together, including base64 expansion, maximal counters/timestamps and even
mutually exclusive terminal/abort fields. They also measure PostgreSQL's canonical
JSON representation. Enrollment cannot consume the space reserved for terminal
results. Oversized identifiers that could escape or expand in JSON are rejected.

Local command receipts live outside the target document in independently retained
rows/maps. They are committed atomically with each transition and are checked before
fresh phase, lease or capacity validation. There is no shared receipt-count quota
that could block closure, takeover or exact accepted replay. PostgreSQL limits each
local receipt row to 2048 bytes; receipt fields are generated by the store.

Receipt rows, reservations and tombstones have no reclamation in this profile.
Storage use therefore grows with accepted commands. Normal memory/disk exhaustion,
database unavailability and caller cancellation can stop progress; these contracts
do not promise availability without functioning authoritative storage. A cleanup
policy requires separate proof that delayed issuance cannot apply.

## Reading and backend scope

ListManagedTargets returns compact summaries and tenant-bound continuation cursors,
with a default page of 100 and maximum of 500. It does not return commands or proofs.
The explicit controller enumeration can discover targets whose tenant metadata was
removed; authorize that method as a controller capability. ReadManagedTarget returns
the complete bounded record for reconciliation. A list page never proves closure.

Protection reads include sealed, aborted and deleted targets even if mutable instance
or tenant rows are absent. Only authoritative absence permits ordinary unmanaged
behavior. Unsupported capability, missing schema, failed reads and ownership mismatch
must refuse affected mutations in service integration. Custom wrappers must preserve
the same store authority; an empty substitute reader is not a fallback.

PostgreSQL uses a parent row lock, post-lock database time and one transaction for
state plus receipt. Additive migrations retain managed tables and reject rollback
which would discard protection. Memory uses one mutex and defensive copies; its
persistence lasts only for that store object's lifetime. Other backends do not gain
managed lifecycle support through the aggregate interface.

Run the `store` package tests with `CTRLPLANE_TEST_POSTGRES_DSN` to exercise real
PostgreSQL. Configured connection failures fail the tests; absence of that variable
skips database cases. The optional restart test has separate seed and verify phases
around an externally managed restart of an owned fixture. Normal test runs do not
restart databases. Service authorization, remote workers and Docker qualification
remain separate integration work.

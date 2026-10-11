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

## Controller persistence

Use `ControllerStore` to accept a managed action, its reserved identities,
immutable receipt and pending audit intent in one authority transaction. Access
attempts use the same outbox without changing desired state. Tenant, authorized
global and configured denial scopes are explicit. Exact replay preserves original
source identity, occurrence time and command digests. It does not authorize the
current caller or prove delivery to an external audit sink.

Memory stages the complete change under its existing mutex. PostgreSQL takes one
controller advisory transaction lock, then physical parent locks in sorted instance
order, then reads database time. Nested lifecycle operations share that transaction
and only the outer operation commits. The advisory lock serializes controller
reads and writes across tenants. This is a throughput limit; remote calls never run while
these locks are held. Direct lifecycle operations take only their parent lock and
never acquire the controller lock in reverse order.

The progress journal and persisted continuation are recovery state. They grant no
provider permission. Each finite provider step validates the accepted action,
audit, immutable release association, projected rows, current parent revision and
lease, plus the full removal closure when applicable. A committed permission marks
possible issuance. Only a newly committed grant permits the first send; replay
returns the original permission for inspection and cannot authorize another send.
Result recording retains uncertainty and definitive responses separately from the
target document. A missing result remains unresolved.

Projection derives a pending Instance, an optional reserved Workload and an immutable
Release from accepted facts. It creates no successful Deployment or route. Owner
reuse needs retained managed provenance. Generic owners, identity collisions and
missing projected rows refuse; replay does not recreate them. Direct store access
remains a trusted embedding capability.

New actions use the strict `ctrlplane.managed.v1` envelope inside the existing
4096-byte Spec. Roles stay in member order. Every service pins a nonsecret startup
binding ID, generation and digest alongside its immutable image and positive
resource limits. This publication validates and preserves those references. It does
not resolve credentials, start processes or attest a runtime. Legacy opaque Specs
keep their original bytes and protection obligations and cannot gain new managed
issue permission through this controller profile.

`ValidateRoleBinding` checks an exact target/member/reservation mapping and refuses
contradictory roles sharing a RuntimeID. This is a leaf contract, not a change to
raw lifecycle reservation admission. The later secured service must validate the
current snapshot and bind issuance to that same revision, then refetch after
contention. Activation must reject contradictory persisted bindings, including
bindings admitted by trusted raw store callers. Every incarnation still counts.

Leaf transport values encode integer counters as canonical decimal strings,
including nested identities, fences, receipts and provider steps. Checked wire
conversions preserve original bytes and stored digests; they never rehash historical
commands during translation. Lifecycle and routing values carry bounded proofs and
explicit command schema versions. Your trusted adapter must validate these values and
authenticate their source before using them as evidence.

New retained action, operation and association rows are limited to 256 KiB; audit
rows to 32 KiB; provider step rows to 16 KiB; continuation rows to 32 KiB. Each opaque
proof or command is at most 4096 bytes, tokens are at most 256 ASCII bytes, cursors
are at most 1024 bytes, and controller pages and pending continuation keys are at
most 64 entries. The first profile permits at most 32 services and 98 distinct
steps: one network create, service creates and starts, service removals, and one
network removal. The Spec byte limit can reduce the number of representable
services. Separate history and receipt rows preserve completion headroom without
changing the existing 8 MiB target bound. Retained rows have no reclamation policy.

Controller persistence supplies no running controller, provider executor, public
managed endpoint, Chronicle publisher or protected disclosure adapter. Your secured
composition must close raw routes, supply machine authorization and durable
access recording, and privately resolve pinned startup configuration. The historical
settlement reader is a leaf interface for those consumers; this slice supplies no
new continuation generations or invented provider evidence.

## Original lifecycle evidence

Use `AcceptManagedOriginalRegistration` and `RecordManagedOriginalDrain` when you
need to recover the original native receipt together with its local acceptance.
You supply leaf facts and the exact opaque receipt export. The wrapper computes
its compact reference and commits that reference, the retained material and the
local receipt in one authority transaction. `ReadManagedOriginalLifecycle` takes
the target key, original kind and local operation ID. It resolves that exact row.

Replay checks the original A request digest, saved local receipt and current
immutable target, reservation and descriptor associations before returning the
saved result. Current lease ownership and expiry do not invalidate an exact
accepted replay. Missing history refuses recovery. You cannot backfill a legacy
receipt or replace an original drain with another local operation ID.

The first drain records process acceptance and permanent admission closure. Its
saved A drain remains incomplete and nonquiescent, with no completion timestamp.
Every reservation sharing the runtime must independently resolve its original
registration and match the same process, configuration and startup descriptor.
These facts do not grant removal permission or prove a later terminal outcome.

The profile uses fixed typed Go JSON field order and UTC timestamps. Decode and
re-encode must reproduce the accepted bytes. Required null values, duplicate or
unknown fields and trailing values refuse. Every adapter-facing counter is a
canonical decimal string, including the parent CAS and saved receipt projection.
Parent, descriptor generation and native signed counters retain their signed
limits. The immutable manifest version retains its uint64 domain; the native
configuration version stays an opaque token. A's stored bytes do not change.

The digest preimage is the UTF-8 domain, one newline and the fixed typed JSON value:

- `ctrlplane.managed.identity.v1` hashes key, provision operation ID, physical
  identity and the complete manifest in that order, with manifest version encoded
  as decimal and spec bytes preserved exactly.
- `ctrlplane.managed.original-facts.v1` hashes kind and its normalized facts variant.
- `ctrlplane.managed.original-material.v1` hashes the single material snapshot:
  original command and reservation, kind, identity digest, facts, raw receipt and
  its digest. It excludes the reference and local receipt.
- `ctrlplane.managed.drain-handle.v1` hashes the target identity digest, complete
  process facts, operation ID and UTC deadline. Runtime ID is inside process facts.
  This derived profile reference does not add a field to the native handle tuple.

The raw native receipt digest is SHA256 of its exact bytes. Native request and
operator command digests keep their own domains; the stored registration command
has a separate digest. You must not substitute one for another.

An original row contains one material snapshot, its computed reference and one
local receipt projection. Its actual JSON encoding is bounded to 32768 bytes;
references to 1024 bytes; raw native exports to 4096 bytes; ASCII tokens to 256
bytes; and TypeIDs to 64 bytes with their required prefixes. The PostgreSQL
constraint applies only to original record kinds in the existing record table.
No target column or extra backend map is required.

Your trusted adapter must authenticate the producer, validate the full native
receipt schema and verify descriptor, role and process provenance before calling
these methods. The store tests use synthetic receipt bytes and qualify storage
only. They do not establish that a real full native receipt fits this profile,
that a process accepted the request, or that the system meets a production SLO.
Later quiescence observation and removal admission remain separate work.

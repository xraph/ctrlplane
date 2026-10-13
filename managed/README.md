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
completed quiescent drain or a fresh accepted later observation for that exact
reservation. Fence survivors must be off the target instance. The host
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
these locks are held. When you use a backend that implements controller authority,
all direct lifecycle mutations acquire it before the parent lock. Nested mutations
reuse the held controller transaction. Backends without that support use their
existing parent transaction. None acquires controller authority from a held parent
callback.

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

## Later quiescence

Use `AcceptManagedQuiescence` after your adapter has authenticated a terminal native
observation. You can submit terminal success or deadline-incomplete quiescence.
Both require permanently closed admission, zero active and unknown claims, and an
exact association with the retained registration and original drain. Other error
classes refuse. `LifecycleObservation` retains its successful-drain validator.

The observation binds the target identity digest, reservation, member, role, both
original operation IDs, exact reference-byte and material digests, process facts,
startup descriptor and native RuntimeID/OperationID/Deadline tuple. It contains no
full manifest copy. Its challenge digest covers every field of `QuiescenceLeaf`,
including source times, terminal outcome, claim counts, observation/challenge IDs
and producer verifier. The attestation is opaque. The optional
`QuiescenceObservationPort` runs outside storage transactions; the store neither
calls it nor authenticates its output.

Source `ObservedAt` must be nonzero and no later than the accepting authority time.
That time must be strictly before `ValidUntil`, with a positive validity interval
of at most 60 seconds. There is no future-time tolerance. Clock disagreement can
refuse progress, and the store never substitutes a convenient source timestamp.
`VerifiedAt` comes from the fresh parent transition callback after locking. There
is no native completion timestamp in this contract.

Acceptance commits one immutable request snapshot, its local receipt and a current
index in the same transaction. The original native exports, saved A drain and
original local receipt stay byte-identical. You cannot use an observation to
backfill a missing original, extend its deadline or replace its receipt.

Current selection advances only for a strictly newer source time. A distinct equal
or older observation conflicts. Exact historical replay checks the saved original
associations, receipt and current history before returning its original result,
even after lease takeover or expiry. It cannot select the historical value again.
Missing or corrupt history refuses recovery. When a current index is absent, a
one-row existence probe in that exact reservation scope distinguishes first
acceptance from a lost index; it never selects evidence or enumerates obligations.

Removal admission, deletion issuance and each fresh provider removal step resolve
all reservations from the authoritative target under the same transaction. Each
binding still needs its fence and every other closure condition. A successful A
drain remains valid through its existing branch. The later branch requires that
binding's exact current observation to remain fresh. Fence-set hashing is unchanged.
A saved permission replay is inspection-only and cannot authorize another send.

The additional digest preimages use UTF-8 domain, newline, then fixed typed JSON:

- `ctrlplane.managed.quiescence-scope.v1` covers `{key, reservation_id}` in that order.
- `ctrlplane.managed.quiescence-challenge.v1` covers the complete `QuiescenceLeaf`
  field order. It excludes the challenge digest itself and opaque attestation.
- `ctrlplane.managed.quiescence-record.v1` covers the final record, including its
  single request snapshot, verification time and saved local receipt.

History uses the reservation scope and exact original local operation ID. The
current row uses that scope and key `current`. Its typed value contains the schema,
reservation, local operation ID, source observation time and final record digest.
Actual JSON caps are 16384 bytes for each history row and 1024 for each current
index, with at most 4096 raw attestation bytes. The codec uses canonical decimal
counters, UTC timestamps and strict accepted-byte round trips, as original evidence
does. No target column or extra backend map is needed. Retained observation history
has no total count or lifetime storage bound.

Tests qualify storage admission using synthetic producer bytes. Native receipt
validation, machine authentication, descriptor provenance and real process
observations remain requirements for the secured adapter. An independent PostgreSQL
connection can recover the retained association; that check does not establish
native process or daemon recovery.

## Original removal attempts

For a `ctrlplane.managed.v1` target, start with
`BeginManagedRemovalAttempt`. You need a sealed, resolved registration set and
complete provisioned resources. The store retains generation one, permanent
attempt and provider operation IDs, root/current records, the initial local
receipt association, and an anchor on the original projected operation in one
transaction. You can't bootstrap an attempt from old partial fences. Other
supported A-only manifests keep their existing lifecycle.

Use the attempt methods in this order:

1. Authorize each original native Begin with `AuthorizeManagedAttemptCommand`.
   Persist its authenticated acceptance with `RecordManagedAttemptFence`.
2. Call `AdmitManagedRemovalAttempt` after every required binding has its original
   fence. Fresh provider permissions still go through `AuthorizeManagedProviderStep`.
3. Record the exact original provider result. After all removal steps have positive
   terminal results, call `SettleManagedRemovalAttempt` with `Removed`.
4. Authorize and retain each original native Finish, then call
   `TombstoneManagedRemovalAttempt`.

A command slot means the original invocation may have been sent, even if your
process stopped before transport. Inspecting or replaying that slot does not grant
a new send. `AuthorizeManagedAttemptRecovery` requires fresh parent CAS and lease
checks but can grant only the exact retained native request. It neither advances
the target/attempt revision nor authorizes a provider retry. Concurrent recovery
grants can repeat that same native idempotent request, so your native adapter must
preserve its original operation, request and command identities.

You can permanently revoke unissued work with `RevokeManagedRemovalAttempt`.
Already accepted native commands remain obligations. A late original Begin may be
recorded after expiry for recovery and Abort, but it does not create live provider
authority. Before Abort, retain `RecordManagedAttemptBindingSettlement`; its witness
uses actual callback time, after the original native acceptance. The Abort proof
must be verified after that witness and remain fresh when the store records it.
Use the binding and historical settlement readers when an attempt never reached
aggregate admission.

`ProviderStepFailureAuthority` accepts one initial failure class:
`ctrlplane.managed.local-no-dispatch.v1`, `local_no_dispatch`,
`before_transport_delegate`. Its reason is `authority_refused`,
`request_binding_mismatch` or `invocation_closed`. Your trusted hook must establish
that the exact permission's invocation never entered the transport delegate.
Remote status codes, timeouts, unavailable results and negative inspection do not
prove this fact. A qualified failure can support `SettledWithoutDeletion` only
when complete provider coverage has no positive deletion or unresolved issued
step. Positive and failure facts conflict atomically. Partial deletion followed by
a failure stays unresolved.

Every fresh provider send validates all retained reservations and all required
provider steps under the current authority transaction. The maximum is 64
reservations and 33 steps. Quiescence remains limited to 60 seconds, and renewing
it does not extend the original native fence. Once that fence expires, further
provider issuance refuses; already issued positive results can still support
truthful Removed settlement, Finish and tombstoning. This interface has no native
fence renewal. Successor allocation uses the separate typed continuation contract below.

Attempt state contains fixed 64-byte binding and 33-byte provider presence masks,
encoded as canonical base64. Each bit must agree with its exact retained row,
local event and receipt. Missing rows never turn an issued slot into an unissued
one. Qualified failure, positive result and uncertainty maintenance is atomic and
does not consume parent or attempt revisions. Local operation IDs are part of new
fact digests; use the same local request to replay an accepted fact.

New digest preimages use UTF-8 domain, a newline, then fixed typed JSON:
`ctrlplane.managed.attempt-scope.v1`, `ctrlplane.managed.removal-attempt.v1`,
`ctrlplane.managed.attempt-fact.v1`, `ctrlplane.managed.attempt-command.v1`,
`ctrlplane.managed.attempt-coverage.v1`,
`ctrlplane.managed.provider-step-failure.v1`,
`ctrlplane.managed.attempt-registration-set.v1` and
`ctrlplane.managed.attempt-resources.v1`. New envelopes encode counters as canonical
decimal strings. Existing A receipts, positive provider records and operation
history keep their original numeric encodings; operation state adds only the
optional attempt anchor.

Before allocation, the store reserves `7*N + S + 8` target revisions for the
finite original transitions, plus one for Begin. At the maximum set that is 489
reserved revisions and one allocation revision. Fresh observations, lease renewals
and other optional metadata need their own capacity. A genuine positive deletion,
validated against complete coverage, can release reserve for impossible Abort
branches. A `local_no_dispatch` fact alone cannot release Finish capacity.

The fixed manifest profile has a separate 64 KiB specification limit, sufficient
for its supported 32 services and 32 members. Native command and evidence limits
remain 4096 bytes. Typed attempt limits apply to complete encoded records and
readers, including nested evidence and receipts.

Managed authority transitions acquire controller authority before the physical
parent lock. PostgreSQL currently uses a global controller advisory lock; Memory
uses its existing store mutex and staged clones. This serialization and retained
history growth need deployment measurement. Immutable receipt probes read only
inside the held transaction, and successful immutable row reuse lasts for one
read-only validation. It cannot cross a write or fresh callback.

Store tests use synthetic native and failure producers. You'll still need an
authenticated native adapter, the qualified transport hook and separate process,
daemon and database recovery qualification before deploying this protocol.


You can refresh a descriptor-ordered subset of 1 to 64 observations through the
optional `QuiescenceBatchStore` capability. Each entry keeps its own operation ID
and unchanged individual request bytes, derived at parent revision `R+i`. The
batch saves those individual receipts at `R+i+1` and one aggregate receipt at
`R+N+1` in the same transaction. It consumes `N+1` parent revisions, with no attempt
revision, event, journal or lease-epoch change. Budget every renewal.

Use a new batch ID and new child operation IDs for fresh observations. Replaying
the exact accepted batch validates its retained graph and returns its saved
receipt, including after tombstoning. A missing child or receipt refuses the whole
read; the store does not rebuild it. If the batch fails, don't fall back to partial
individual writes. Check that your store implements the optional capability.

Generation-one batches and individual observations reject a projected Abort on any
reservation sharing the selected runtime. An Abort on another runtime does not
block that observation. Historical reads still validate the retained registration,
drain and receipts after Abort. They grant no send authority. Revocation, Removed
settlement and Finish permit observational retention; Deleted refuses fresh
observations. The original native fence and the 60-second proof lifetime still
apply to fresh provider issuance.

Current-code legacy-method refusals protect enrolled attempts only when those
calls reach this implementation. They do not fence an older binary that writes
the same database. Mixed-version operation and rollback remain unqualified until
storage admission and the trusted transport enforce a writer floor.


The maximum lifecycle campaigns run separately from ordinary unit tests. CI's
persistence job requires all three generation-one campaigns against PostgreSQL
and retains their structured logs. You can run the same selections locally with a private
`CTRLPLANE_TEST_POSTGRES_DSN` pointing to a throwaway database:

```sh
GOWORK=off CTRLPLANE_MANAGED_MAXIMUM=individual go test ./store ./store/memory -run '^(TestManagedAttemptFullFiniteSet|TestManagedAttemptPostgresFullFiniteSetNearLimit|TestManagedAttemptMemoryFullFiniteSetNearLimit)$' -count=1 -timeout=30m -v
GOWORK=off CTRLPLANE_MANAGED_MAXIMUM=batch go test ./store ./store/memory -run '^TestManagedQuiescenceBatch(FullFiniteSet|PostgresFullFiniteSetNearLimit|MemoryFullFiniteSetNearLimit)$' -count=1 -timeout=30m -v
GOWORK=off CTRLPLANE_MANAGED_MAXIMUM=batch go test -race ./store ./store/memory -run '^TestManagedQuiescenceBatch(FullFiniteSet|PostgresFullFiniteSetNearLimit|MemoryFullFiniteSetNearLimit)$' -count=1 -timeout=30m -v
```

Without that selector, ordinary test runs visibly skip the six maximum entry
points. The exact 490 acceptance and 489 refusal checks still run by default.
The normal individual campaign preserves its 555-revision fixture. Normal and
race constrained batch completion fixtures start with 2,862 transitions of
headroom. That
finite allowance covers the 490-transition admission boundary, at most 34 full
batches and 162 lease renewals from the fixture's declared call sites. Each run
counts attempted and accepted renewals. The unconstrained case also retains its
final diagnostic batch. A single operation that outlasts its proof still fails;
this allowance does not promise progress through arbitrary delays. Separate
64-binding, 24-positive-slot tests prove that a full batch refuses 206 headroom
without writes and accepts 209, leaving 144 for the unchanged terminal reserve.
The full positive lifecycle consumes 293 parent
transitions including allocation, and 292 attempt transitions after revision 1;
the latter includes all 33 provider permissions. Positive result retention does
not advance either counter. Sequential individual race sweeps remain explicit
timing diagnostics, with their actual failures reported separately.


## Removal continuations

Use `RemovalContinuationStore.BeginManagedRemovalContinuation` after the current
attempt is permanently revoked with `not_issued` or qualified
`settled_without_deletion` coverage. Every retained Begin needs its exact native
fence, a settlement witness and accepted Abort. Missing receipts and expired
leases cannot establish settlement. Generic `SubmitManagedAction` always refuses
Continue, including replay.

Build a `ContinuationRequest` with the exact predecessor, a fresh action ID,
fresh successor attempt and provider IDs, and the existing parent authority.
`RemovalContinuationDigest` computes its dedicated digest without authorizing it.
The store checks the complete retained graph under the parent lock, closes the
predecessor and allocates one successor atomically. Original target bindings,
fences, Abort proofs and generation-one descriptors remain unchanged. Generation
and pointer counters advance once; there is no lifetime generation quota.

You can reconcile an uncertain reply with `ReadManagedRemovalContinuation` and
exact same-ID replay. An error may follow a committed transaction. No usable
grant accompanies that error, and timeout alone never proves rollback or permits
a replacement identity. If authoritative reads establish absence, fresh acceptance
still needs valid current revision, lease and predecessor. A partial graph refuses
without repair. Historical replay returns the saved acceptance, not current send
authority.

A fresh successor Begin requires `AttemptCurrentBindingObservation`, supplied by
the trusted native producer. It binds the original registration and process,
current native version and removal epoch, verifier, evidence and validity window.
The original process profile bounds that window. The store validates the exact
supplied facts and native response association; it does not authenticate an
external native authority. You still need that producer and transport integration.
Synthetic storage fixtures do not establish it.

Successor batches accept 1 through 64 observations. They classify every sibling
sharing a runtime, including an original projected Abort, against its retained
root fence, witness and Abort. Original individual-observation behavior stays the
same. A still-live observation from before Continue can be reused with its original
source time and 60-second maximum lifetime. Continue never refreshes it. Native
source expiry and quiescence expiry independently deny fresh provider issue.

Revocation permanently denies new Begin and provider permissions. It still permits
an exact eligible terminal Abort or Finish and nonissuing recovery of an already
retained command. Fresh terminal commands keep their existing settlement and
witness prerequisites. Diagnostic observation retention alone grants no issue.

The fixed old executable in `qualification/oldb23a` tests the published B23a
library. Its recognized successor issuance paths refuse, but a valid old journal
advance can discard the successor association. The new strict reader detects that
loss. Passing these fixtures does not establish mixed-writer safety, rollback
safety, a writer floor or authenticated native execution.

Run the successor maximum explicitly:

```sh
GOWORK=off CTRLPLANE_MANAGED_MAXIMUM=successor go test ./store -run '^TestManagedRemovalContinuationFullFiniteSet$' -count=1 -timeout=30m -v
```

It completes a generation-three 64-binding, 33-step lifecycle with 4 KiB evidence
fields and checks Memory and PostgreSQL. PostgreSQL combines completion with a
synthetic near-limit parent seed before the final Continue. The required lifecycle
uses 292 attempt transitions after allocation and 293 parent transitions including
Continue. Optional work is counted separately: at most 34 full batches and 162
lease renewals. The 2,864-transition fixture allowance retains the conservative
490 reserve and two explicit spare transitions. CI requires this campaign under
race on the exact commit and audits every required case without skips.

## Recovery foundation

The optional removal recovery contracts preserve the original attempt identity,
provider command and possible effects while retaining revalidation, invocation and
retry records separately. Complete recovery is still unavailable. Do not advertise
`ctrlplane.managed.removal-recovery.v1` support from the presence of these types or
from a successful read of one retained record. Terminal coverage, native closure,
provider custody and host integration still need implementation and qualification.

Memory and PostgreSQL retain an unresolved-work index with bounded pages when an
action or continuation is accepted. `WorkDiscoveryStore` pages it by ordinal; a newer
user request does not hide older obligations. `PhaseCommandStore` retains the exact
registration, drain, activation or route command with its independent parent
receipt and required audit. `PhaseOriginalResultStore` attaches an independently
retained original registration or drain result to that command. Both receipts must
agree with the full stored associations. A missing row refuses without backfill.
These records remain unresolved until a later qualified lifecycle closes them.

Use the producer's native request and command digests when you construct a
`PhaseNativeIdentity`. A hash of a local transport envelope cannot replace the
native protocol's digest. The configured host must authenticate that translation
before accepting the result.

Recovery invocation readers verify the retained permission, marker, result and
native commit associations before returning them. Fresh public commands require
the complete recovery capability, which remains disabled. A retained cutoff
revokes further issuance but does not prove that an earlier command had no effect
or authorize settlement. Late results still need their original associations.

## Protected writer admission

You can opt a Memory or PostgreSQL store into managed write admission with
`NewWithWriterFloorAdmission`. Its authority is fixed when you construct the store.
The PostgreSQL protected schema must be installed separately through the floor
administrator before you use this mode. Missing tables or denied reads refuse;
they do not select legacy behavior.

A pristine protected namespace admits existing managed writes. Every positive
floor currently refuses them because no capable writer protocol is enabled.
The check covers managed commands, retained records and audits, projections and
direct quiescence batch staging. The floor decision stays inside the same locked
transaction as the mutation. Exact read-only receipt recovery does not authorize
a write.

This opt-in store check does not fence an old executable, an ordinary store write
such as `InsertAuditEntry`, or a raw database handle. Existing `New` constructors
remain legacy. You need a separate deployed credential and grant barrier covering
all writers before activating a protected deployment. The dedicated administrator
and read handles separate library capabilities; they do not prove database role
isolation. Restoring an older database also requires independent admission state
and effect reconciliation before any writer can restart.

For a dedicated PostgreSQL authority database, `NewWriterFloorExclusionAdmin`
adds grant revocation to the floor administrator's transaction. Configure its
separate administrator, offline owner and exact retired writer roles. Your launcher
must first close admission, disable those logins and drain their sessions. Activation
checks the roles, sessions and catalog, then revokes table, column, sequence, routine,
schema, database and default privileges before updating the floor and history.
Any refusal rolls back both the privilege changes and the floor update.

The exclusion administrator grants no replacement writer access. Positive writer
protocols remain disabled. This database check does not implement the launcher's
admission barrier or the independent state needed to quarantine a restored database.

# Ctrlplane dashboard contract hardening

The React migration already checks the authenticated tenant and configured policy. This pass preserves that boundary while making transport behavior explicit. The numbered architecture files listed in AGENTS.md are absent; app/controlplane.go, domain service interfaces, extension/contract and store implementations are the source references.

## Implementation slices

1. Validate manifest and handler names, kinds, versions and invalidations before publishing. Resolve the initialized extension at request time and return retryable unavailable while startup has not succeeded. Sanitize errors while retaining server diagnostics. Replace blanket invalidation with domain dependencies.
2. Project all domain responses onto explicit dashboard types. Preserve authoring sources and service references. Never serialize managed secret values. Keep unknown health and telemetry distinct from zero measurements.
3. Verify continuation and partial updates against memory and persistent stores. Correct ignored cursors where pagination is advertised; explicitly bound aggregate windows. Preserve unrelated fields on updates and prove tenant isolation on each path.
4. Run focused transport tests, full Go build/test/lint and React compatibility checks. Record which stores and live flows were verified. Commit each passing slice locally on main, preserving concurrent work.

Rex requested this pass and coordination with the other active dashboard agents on 2026-10-08. Nexus supplies useful contract patterns, but its operator-wide scope does not replace Ctrlplane tenant authorization.

## Implemented behavior

All 82 intents (36 queries and 46 commands) are checked against the embedded manifest before publication. Names, kinds, versions and invalidation targets must match their bindings. Commands return their declared query dependencies explicitly because the pinned Forge runtime does not apply manifest invalidations automatically.

You receive retryable UNAVAILABLE while the extension is not ready or its configured authorization provider fails. The configured policy runs once after readiness and trusted identity checks. Policy refusals remain PERMISSION_DENIED. Backend errors are logged server-side; response errors and stored diagnostic fields use safe messages.

Responses pass through explicit dashboard projections, including 49 resource and nested DTOs in responses.go. An unhandled output type fails rather than falling back to domain serialization. Managed secrets expose metadata only. Authoring sources, services, variables and references survive projection, and timestamps use UTC. User-authored environment values, configuration files and Helm values remain visible to authorized readers; this is not a general credential scanner.

Health without observations stays unknown. Uncomputed uptime and failure streaks, missing latency and absent check timestamps are null. A provider without a health probe stays unknown and cannot report a successful test. Health storage failures propagate instead of becoming unknown observations.

Implemented list stores use descending creation time and TypeID as a stable continuation key, with a lookahead row and totals counted before the cursor. Continuation does not depend on the previous anchor still existing. Workload lists expose next_cursor, and deployment/release pages merge replica results in the same global order. Recent deployments, audit and events remain explicitly bounded windows.

SQLite uses its migration executor, creates the missing datacenter prerequisite and migrates the multi-service/source columns used by its current models. Workload CRUD persists services, labels, placement, template/release references, desired replicas and pause/resume state. SQLite and PostgreSQL migrations add instance datacenter, current release and suspension fields; MongoDB retains the same fields in its documents. Datacenter filtering works on all five stores. Partial-update tests preserve unrelated template and route fields through actual storage.

Instance updates and workload updates retain tenant ownership at the storage boundary. Workload creation enforces tenant-local slug uniqueness, including through database indexes on SQLite, PostgreSQL and MongoDB. PostgreSQL lifecycle timestamps return in UTC.

## Verification on 2026-10-08

| Check | Evidence |
| --- | --- |
| Go root | Full build, tests and lint passed; lint reported zero issues. |
| Race checks | Contract, health, memory, Badger, SQLite, PostgreSQL, MongoDB and workload packages passed across the hardening passes; continuation tests exercise the shared pagination helper. |
| Continuation | Equal timestamps, two-row pages, totals, no duplicate IDs and malformed cursors checked against memory, Badger, SQLite, live PostgreSQL 17 and live MongoDB 7. Instances, templates, deployments, releases, tenants, datacenters and workloads are covered. Workload continuation also checks fractional seconds and a whole-second boundary. |
| HTTP persistence | Template name-only and route weight-only updates preserve unrelated source, service and proxy fields on all five stores. Foreign template updates are refused. Template continuation survives anchor deletion and insertion of a newer row while excluding another tenant. |
| Aggregate continuation | Workload deployment pages continue across two replicas on all five stores without duplicates or skipped records. |
| Placement and ownership | All five stores retain instance placement, current release, suspension and workload specs. Combined datacenter/provider/state/label filters exclude another tenant. Foreign reads, updates and deletes are refused. Optional IDs and lifecycle timestamps clear correctly. Workload duplicate creates return AlreadyExists. |
| Database upgrades | SQLite upgrades from version 22 while preserving an existing instance, then retains a paused workload and resumed replicas through two close/reopen cycles. PostgreSQL upgrades from version 27 while retaining existing instance/workload rows and saving the new placement and lifecycle fields. Repeated migration is checked. |
| CI | A persistence job provisions PostgreSQL 17 and MongoDB 7 and runs the HTTP/store suite with the race detector. Its exact test command passed locally against isolated containers; workflow YAML and actionlint passed. The GitHub-hosted job has not run in this local-only change. |
| Transport | Binding validation, readiness, safe failures, specific invalidation dependencies, configured policy errors and providers without probes are covered. Existing tenant and parent-ownership denial tests still pass. |
| React compatibility | Plugin typecheck, lint and all nine tests passed. No React source changed in this pass. |
| Browser | Updated Go demo and React shell rendered workload detail and health. Desktop and 390-pixel health layouts were inspected; unknown values render as absent, the page has no narrow overflow and wide tables scroll locally. Browser console errors were empty. |

The demo build passed with its local Forge replacement. Root checks use the pinned Forge version. Browser observations use a simulated provider and local development identity, and do not qualify a published consumer or production deployment. Write and pagination behavior in this pass was verified through HTTP tests; it was not repeated through browser controls.

## Reproducing the database checks

Set CTRLPLANE_TEST_POSTGRES_DSN to a PostgreSQL URL whose test role can create databases, and CTRLPLANE_TEST_MONGO_URI to a test MongoDB server. Run `go test -race -count=1 -v ./extension/contract ./store/sqlite ./store/postgres ./store/mongo`. Each live test creates and removes its own database. An absent variable skips only that backend; a configured connection or migration failure fails the test. SQLite always uses a temporary on-disk database. The persistence CI job supplies both variables so those cases run there.

The storage gaps recorded in the initial report are resolved. The local test containers were isolated from other development databases and removed after verification. New unique workload indexes reject existing duplicate tenant/slug pairs rather than deleting or choosing a record silently; resolve any such data conflict before rerunning migration.

## Environment qualification

External cloud operations, DNS verification, ACME issuance, live telemetry, remote exec and production credentials remain unverified. The earlier shared-workspace failures and browser coverage limits in MIGRATION.md still apply. We notified the Nexus and dashboard coordination chats of this approach; each contributor retains its own authorization and domain semantics.

# Ctrlplane dashboard contract hardening

The React migration already checks the authenticated tenant and configured policy. This pass preserves that boundary while making transport behavior explicit. The numbered architecture files listed in AGENTS.md are absent; app/controlplane.go, domain service interfaces, extension/contract and store implementations are the source references.

## Implementation slices

1. Validate manifest and handler names, kinds, versions and invalidations before publishing. Resolve the initialized extension at request time and return retryable unavailable while startup has not succeeded. Sanitize errors while retaining server diagnostics. Replace blanket invalidation with domain dependencies.
2. Project all domain responses onto explicit dashboard types. Preserve authoring sources and service references. Never serialize managed secret values. Keep unknown health and telemetry distinct from zero measurements.
3. Verify continuation and partial updates against memory and persistent stores. Correct ignored cursors where pagination is advertised; explicitly bound aggregate windows. Preserve unrelated fields on updates and prove tenant isolation on each path.
4. Run focused transport tests, full Go build/test/lint and React compatibility checks. Record which stores and live flows were verified. Commit each passing slice locally on main, preserving concurrent work.

Rex requested this pass and coordination with the other active dashboard agents on 2026-10-08. Nexus supplies useful contract patterns, but its operator-wide scope does not replace Ctrlplane tenant authorization.

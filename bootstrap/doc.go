// Package bootstrap manages shared infrastructure services that
// auto-deploy when a datacenter is created. Two contribution paths
// converge into a single reconciled set per datacenter:
//
//  1. Declarative — operators put a slice of BootstrapServiceSpec on
//     the Datacenter row (e.g. "every k8s cluster ships with a
//     fluent-bit DaemonSet"). Static, persisted, edited like any
//     other config.
//
//  2. Programmatic — extensions register Hook implementations via the
//     Registry; each hook self-filters by datacenter shape and
//     contributes BootstrapServiceSpec entries (e.g. the network
//     extension auto-installing cert-manager on every k8s
//     datacenter).
//
// The reconciler unions both sources, dedupes by Spec.Name (declarative
// wins on conflict), and drives the resulting set toward Running.
//
// # Tenant ownership
//
// BootstrapWorkloads are *not* tenant-scoped. They belong to the
// datacenter, are managed by the platform under synthesized system
// claims, and are addressable only via system-admin-gated read paths.
// This is the documented exception to the tenant-scoping rule in
// CLAUDE.md — the workloads represent platform infrastructure shared
// across all tenants of a datacenter, not tenant payload.
//
// # Lifecycle (eventually consistent)
//
// Datacenter.Service.Create returns synchronously after the row is
// inserted; bootstrap reconciliation does not block create. The
// reconciler worker (worker/reconciler.go) walks every datacenter on
// each tick and calls bootstrap.Service.Reconcile, which:
//
//   - inserts pending rows for desired-but-missing services and
//     calls provider.Provision;
//   - retires rows whose spec is no longer desired (Deprovision +
//     row delete);
//   - retries failed rows with an attempts counter for backoff.
//
// Idempotent: re-running Reconcile with no spec change is a no-op.
//
// # Phase 1 scope
//
// The current implementation persists to the in-memory store only.
// Phase 2 adds postgres/sqlite/mongo/badger backends and the
// datacenter-Update diff path. Phase 3 adds the dashboard panel.
package bootstrap

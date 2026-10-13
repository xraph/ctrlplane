// Package docker is a Docker-backed provider.Provider. It maps each
// ctrlplane Instance to one Docker container, named "cp-<instanceID>"
// so lookups by ID are O(1) inspect calls.
//
// ManagedDockerExecutor provides a separate single-mutation custody path. You
// supply an authenticated authority, effect verifier, protected journal and pinned
// transport. It records delegation durably before sending. A lost response stays
// unresolved, and replay never sends again. Creation retains the actual Docker ID;
// removal uses that exact ID and treats NotFound as uncertainty.
//
// FileManagedDockerJournal requires a private local POSIX directory outside
// restored controller database state. SDKManagedDockerTransport supports configured
// Unix sockets or TCP endpoints with optional verified TLS. Images must already
// be present by digest; the adapter never pulls or implicitly starts a container.
//
// These components do not implement the complete managed provider or recovery
// profile. Your host still owns authenticated enrollment, fresh writer admission,
// restore quarantine and native lifecycle integration.
package docker

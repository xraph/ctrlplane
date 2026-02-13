// Package badger provides a Badger DB implementation of the store.Store interface.
//
// Badger is an embedded key-value database optimized for read-heavy workloads
// and write-heavy workloads with good read performance. It provides ACID
// guarantees and supports transactions.
//
// This implementation uses JSON encoding for complex structures and prefix-based
// key namespacing for different entity types. It is suitable for single-node
// deployments and local development environments.
package badger

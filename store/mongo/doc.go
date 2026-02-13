// Package mongo provides a MongoDB implementation of the store.Store interface.
//
// MongoDB is a document database that stores data as flexible BSON documents.
// This implementation uses the official MongoDB Go driver v2 and maps each
// domain entity type to a dedicated collection with appropriate indexes.
// It is suitable for production deployments requiring horizontal scalability
// and flexible schema evolution.
package mongo

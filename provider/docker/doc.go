// Package docker is a Docker-backed provider.Provider. It maps each
// ctrlplane Instance to one Docker container, named "cp-<instanceID>"
// so lookups by ID are O(1) inspect calls.
package docker

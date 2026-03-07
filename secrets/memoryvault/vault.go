// Package memoryvault provides an in-memory implementation of [secrets.Vault]
// for development and testing. All data is stored in a simple map protected
// by a read-write mutex and is lost when the process exits.
package memoryvault

import (
	"context"
	"sync"

	ctrlplane "github.com/xraph/ctrlplane"
)

// Vault is an in-memory implementation of secrets.Vault for development and testing.
type Vault struct {
	mu   sync.RWMutex
	data map[string][]byte
}

// New creates a new in-memory vault.
func New() *Vault {
	return &Vault{data: make(map[string][]byte)}
}

// Store persists a secret value in memory.
func (v *Vault) Store(_ context.Context, key string, value []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	// Clone the value to prevent external mutation.
	cp := make([]byte, len(value))
	copy(cp, value)

	v.data[key] = cp

	return nil
}

// Retrieve returns a secret value from memory.
func (v *Vault) Retrieve(_ context.Context, key string) ([]byte, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	val, ok := v.data[key]
	if !ok {
		return nil, ctrlplane.ErrNotFound
	}

	// Return a copy to prevent external mutation.
	cp := make([]byte, len(val))
	copy(cp, val)

	return cp, nil
}

// Delete removes a secret from memory.
func (v *Vault) Delete(_ context.Context, key string) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if _, ok := v.data[key]; !ok {
		return ctrlplane.ErrNotFound
	}

	delete(v.data, key)

	return nil
}

// Rotate is a no-op for the in-memory vault. Real implementations would
// generate a new encryption key version and re-encrypt stored values.
func (v *Vault) Rotate(_ context.Context, _ string) error {
	return nil
}

package provider

import (
	"errors"
	"fmt"
	"maps"
	"sync"
)

// ErrProviderNotFound indicates the named provider is not registered.
var ErrProviderNotFound = errors.New("ctrlplane: provider not registered")

// Registry manages named providers. Thread-safe.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
	fallback  string
}

// NewRegistry creates an empty provider registry.
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]Provider),
	}
}

// Register adds a provider under the given name.
func (r *Registry) Register(name string, p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.providers[name] = p
}

// Get retrieves a provider by name.
func (r *Registry) Get(name string) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrProviderNotFound, name)
	}

	return p, nil
}

// Default returns the fallback provider.
func (r *Registry) Default() (Provider, error) {
	return r.Get(r.fallback)
}

// SetDefault sets the fallback provider name.
func (r *Registry) SetDefault(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.fallback = name
}

// List returns all registered provider names.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}

	return names
}

// All returns a snapshot of all registered providers.
func (r *Registry) All() map[string]Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string]Provider, len(r.providers))
	maps.Copy(result, r.providers)

	return result
}

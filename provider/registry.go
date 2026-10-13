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

// Get retrieves a provider by registry key. See Resolve for how a name
// that is not a key is matched.
func (r *Registry) Get(name string) (Provider, error) {
	_, p, err := r.Resolve(name)

	return p, err
}

// Resolve looks a provider up and returns the registry key it is held
// under along with the provider itself.
//
// The key you passed to Register is matched first. When nothing is
// registered under name, Resolve falls back to the provider whose
// Info().Name equals name, as long as exactly one does. That keeps rows
// written before instances recorded registry keys resolvable: those rows
// hold Info().Name, and for most providers the key and the name are the
// same string anyway.
func (r *Registry) Resolve(name string) (string, Provider, error) {
	r.mu.RLock()
	p, ok := r.providers[name]
	snapshot := maps.Clone(r.providers)
	r.mu.RUnlock()

	if ok {
		return name, p, nil
	}

	// Info is called outside the lock: it belongs to the provider and the
	// registry should not hold its mutex across foreign code.
	var (
		matchKey string
		match    Provider
		matches  int
	)

	for key, candidate := range snapshot {
		if candidate.Info().Name == name {
			matchKey, match = key, candidate
			matches++
		}
	}

	switch matches {
	case 0:
		return "", nil, fmt.Errorf("%w: %s", ErrProviderNotFound, name)
	case 1:
		return matchKey, match, nil
	default:
		return "", nil, fmt.Errorf("%w: %s is the name of %d registered providers; look it up by registry key",
			ErrProviderNotFound, name, matches)
	}
}

// Default returns the fallback provider.
func (r *Registry) Default() (Provider, error) {
	return r.Get(r.DefaultName())
}

// ResolveDefault returns the fallback provider and the registry key it is
// held under.
func (r *Registry) ResolveDefault() (string, Provider, error) {
	return r.Resolve(r.DefaultName())
}

// DefaultName returns the fallback provider name, or "" when none is set.
func (r *Registry) DefaultName() string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.fallback
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

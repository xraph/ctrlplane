package memory

import (
	"context"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
)

// InspectManagedOwnerProtection derives ownership while holding the persistence lock.
func (s *Store) InspectManagedOwnerProtection(ctx context.Context, key managed.OwnerKey) (managed.Protection, error) {
	if err := key.Validate(); err != nil {
		return managed.Protection{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	result := managed.Protection{}

	for rawID, t := range s.managedTargets {
		if err := ctx.Err(); err != nil {
			return managed.Protection{}, err
		}

		if t == nil {
			return managed.Protection{}, managed.ErrAuthority
		}

		if t.TenantID != key.TenantID {
			continue
		}

		instanceID, err := id.ParseWithPrefix(rawID, id.PrefixInstance)
		if err != nil {
			return managed.Protection{}, managed.ErrAuthority
		}

		p, err := managed.InspectOwnerTarget(t, managed.Key{TenantID: key.TenantID, InstanceID: instanceID}, key)
		if err != nil {
			return managed.Protection{}, err
		}

		if p.Protected {
			result = p
		}
	}

	if err := ctx.Err(); err != nil {
		return managed.Protection{}, err
	}

	return result, nil
}

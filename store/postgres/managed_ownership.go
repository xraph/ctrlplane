package postgres

import (
	"context"
	"encoding/json"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
)

type managedOwnerRow struct {
	ID   id.ID  `grove:"id"`
	Data []byte `grove:"data"`
}

// InspectManagedOwnerProtection pages the tenant index and validates every document.
// Lookup cost grows with retained targets; pages bound decoded memory, not scan cost.
func (s *Store) InspectManagedOwnerProtection(ctx context.Context, key managed.OwnerKey) (managed.Protection, error) {
	if err := key.Validate(); err != nil {
		return managed.Protection{}, err
	}

	result := managed.Protection{}
	after := ""

	for {
		var rows []managedOwnerRow

		query := s.pg.NewRaw("SELECT id,data FROM cp_managed_targets WHERE tenant_id=$1 ORDER BY id LIMIT 16", key.TenantID)
		if after != "" {
			query = s.pg.NewRaw("SELECT id,data FROM cp_managed_targets WHERE tenant_id=$1 AND id>$2 ORDER BY id LIMIT 16", key.TenantID, after)
		}

		if err := query.Scan(ctx, &rows); err != nil {
			return managed.Protection{}, err
		}

		for _, row := range rows {
			var target managed.Target
			if err := json.Unmarshal(row.Data, &target); err != nil {
				return managed.Protection{}, managed.ErrAuthority
			}

			p, err := managed.InspectOwnerTarget(&target, managed.Key{TenantID: key.TenantID, InstanceID: row.ID}, key)
			if err != nil {
				return managed.Protection{}, err
			}

			if p.Protected {
				result = p
			}

			after = row.ID.String()
		}

		if len(rows) < 16 {
			return result, nil
		}
	}
}

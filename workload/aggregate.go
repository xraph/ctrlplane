package workload

import (
	"context"
	"sort"

	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/network"
)

// ListDeployments returns every Deployment whose target instance
// is one of the workload's replicas. Each rollout is unique by
// Deploy.ID — no de-dup needed (unlike releases, which are shared
// across replicas).
//
// Pagination: opts.Cursor is interpreted PER replica (we don't
// have a stable cross-replica cursor today), and Limit is the
// max-per-replica. Total is the sum across replicas. This is
// good enough for the dashboard's small replica counts; once we
// have a workload-level deploy store, this collapses to one call.
func (s *service) ListDeployments(ctx context.Context, workloadID id.ID, opts deploy.ListOptions) (*deploy.DeployListResult, error) {
	replicas, err := s.ListInstances(ctx, workloadID)
	if err != nil {
		return nil, err
	}

	out := &deploy.DeployListResult{}

	for _, r := range replicas {
		res, err := s.deploys.ListDeployments(ctx, r.ID, opts)
		if err != nil || res == nil {
			continue
		}

		out.Items = append(out.Items, res.Items...)
		out.Total += res.Total
	}
	// Newest first across the merged set.
	sort.Slice(out.Items, func(i, j int) bool {
		return out.Items[i].CreatedAt.After(out.Items[j].CreatedAt)
	})

	return out, nil
}

// ListReleases returns the union of releases across the workload's
// replicas, deduplicated by Release.ID. Sorted by CreatedAt
// descending (newest first).
func (s *service) ListReleases(ctx context.Context, workloadID id.ID, opts deploy.ListOptions) (*deploy.ReleaseListResult, error) {
	replicas, err := s.ListInstances(ctx, workloadID)
	if err != nil {
		return nil, err
	}

	out := &deploy.ReleaseListResult{}
	seen := make(map[string]bool)

	for _, r := range replicas {
		res, err := s.deploys.ListReleases(ctx, r.ID, opts)
		if err != nil || res == nil {
			continue
		}

		for _, rel := range res.Items {
			key := rel.ID.String()
			if seen[key] {
				continue
			}

			seen[key] = true

			out.Items = append(out.Items, rel)
		}
	}

	out.Total = len(out.Items)
	sort.Slice(out.Items, func(i, j int) bool {
		return out.Items[i].CreatedAt.After(out.Items[j].CreatedAt)
	})

	return out, nil
}

// ListDomains returns every Domain attached to any of the
// workload's replicas, deduplicated by Domain.ID.
//
// Returns an empty slice (no error) when no network service is
// wired into the workload — keeps callers' rendering paths
// uniform regardless of optional dependency availability.
func (s *service) ListDomains(ctx context.Context, workloadID id.ID) ([]network.Domain, error) {
	if s.network == nil {
		return []network.Domain{}, nil
	}

	replicas, err := s.ListInstances(ctx, workloadID)
	if err != nil {
		return nil, err
	}

	var out []network.Domain

	seen := make(map[string]bool)

	for _, r := range replicas {
		ds, err := s.network.ListDomains(ctx, r.ID)
		if err != nil {
			continue
		}

		for _, d := range ds {
			key := d.ID.String()
			if seen[key] {
				continue
			}

			seen[key] = true

			out = append(out, d)
		}
	}

	return out, nil
}

// ListRoutes returns every Route attached to any of the workload's
// replicas, deduplicated by Route.ID.
func (s *service) ListRoutes(ctx context.Context, workloadID id.ID) ([]network.Route, error) {
	if s.network == nil {
		return []network.Route{}, nil
	}

	replicas, err := s.ListInstances(ctx, workloadID)
	if err != nil {
		return nil, err
	}

	var out []network.Route

	seen := make(map[string]bool)

	for _, r := range replicas {
		rs, err := s.network.ListRoutes(ctx, r.ID)
		if err != nil {
			continue
		}

		for _, route := range rs {
			key := route.ID.String()
			if seen[key] {
				continue
			}

			seen[key] = true

			out = append(out, route)
		}
	}

	return out, nil
}

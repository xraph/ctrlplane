package contract

import (
	"context"
	"fmt"
	"sort"

	"github.com/xraph/ctrlplane/app"
	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
)

// workloadCollection refuses a partial read rather than hiding failed replicas.
func workloadCollection(ctx context.Context, cp *app.CtrlPlane, target id.ID, kind string, opts deploy.ListOptions) (any, error) {
	if _, err := cp.Workloads.Get(ctx, target); err != nil {
		return nil, err
	}

	replicas, err := cp.Workloads.ListInstances(ctx, target)
	if err != nil {
		return nil, err
	}

	return instanceCollection(ctx, cp, replicas, kind, opts)
}

func instanceCollection(ctx context.Context, cp *app.CtrlPlane, instances []*instance.Instance, kind string, opts deploy.ListOptions) (any, error) {
	items := make([]any, 0)
	total := 0
	complete := true

	for _, inst := range instances {
		switch kind {
		case "deployments":
			result, err := cp.Deploys.ListDeployments(ctx, inst.ID, opts)
			if err != nil {
				return nil, fmt.Errorf("instance %s deployments: %w", inst.ID, err)
			}

			for _, row := range result.Items {
				items = append(items, row)
			}

			total += result.Total
			complete = complete && result.NextCursor == "" && len(result.Items) >= result.Total && len(result.Items) < opts.Limit
		case "releases":
			result, err := cp.Deploys.ListReleases(ctx, inst.ID, opts)
			if err != nil {
				return nil, fmt.Errorf("instance %s releases: %w", inst.ID, err)
			}

			for _, row := range result.Items {
				items = append(items, row)
			}

			total += result.Total
			complete = complete && result.NextCursor == "" && len(result.Items) >= result.Total && len(result.Items) < opts.Limit
		case "domains":
			result, err := cp.Network.ListDomains(ctx, inst.ID)
			if err != nil {
				return nil, fmt.Errorf("instance %s domains: %w", inst.ID, err)
			}

			for _, row := range result {
				items = append(items, row)
			}

			total += len(result)
		case "routes":
			result, err := cp.Network.ListRoutes(ctx, inst.ID)
			if err != nil {
				return nil, fmt.Errorf("instance %s routes: %w", inst.ID, err)
			}

			for _, row := range result {
				items = append(items, row)
			}

			total += len(result)
		}
	}

	if kind == "deployments" {
		sort.Slice(items, func(i, j int) bool {
			return items[i].(*deploy.Deployment).CreatedAt.After(items[j].(*deploy.Deployment).CreatedAt)
		})
	}

	if kind == "releases" {
		sort.Slice(items, func(i, j int) bool {
			return items[i].(*deploy.Release).CreatedAt.After(items[j].(*deploy.Release).CreatedAt)
		})
	}

	return page{Items: items, Total: total, Complete: complete}, nil
}

func recentDeployments(ctx context.Context, cp *app.CtrlPlane, in instance.ListOptions) (any, error) {
	in.Limit = limit(in.Limit)

	instances, err := cp.Instances.List(ctx, in)
	if err != nil {
		return nil, err
	}

	result, err := instanceCollection(ctx, cp, instances.Items, "deployments", deploy.ListOptions{Limit: in.Limit})
	if err != nil {
		return nil, err
	}

	out := result.(page)
	items := out.Items.([]any)

	out.Complete = out.Complete && instances.NextCursor == "" && len(instances.Items) >= instances.Total && len(instances.Items) < in.Limit
	if len(items) > in.Limit {
		out.Items = items[:in.Limit]
		out.Complete = false
	}

	return out, nil
}

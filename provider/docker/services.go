package docker

// services.go implements multi-container "Compose-project"
// orchestration directly via the Docker SDK — no `docker compose` CLI
// dependency. One ctrlplane Instance maps to one project comprising:
//
//   - a user-defined bridge network named `cp-<instanceID>-net`, which
//     gives sibling services native DNS resolution by service name
//     (no app-level config required: container `web` reaches container
//     `api` via the host name `api`);
//   - one container per ServiceSpec, named `cp-<instanceID>-<service>`
//     and aliased `<service>` on the project network;
//   - Init services (Role=Init) run to completion via init_runner.go
//     before Main/Sidecar containers are created.
//
// The legacy `cp-<instanceID>` single-container layout is the special
// case where Services has one Main entry and the project network is
// effectively unused. Both shapes route through provisionProject.

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

// projectName derives the Compose-project name (and network prefix)
// for an Instance. Stable so `docker ps --filter name=cp-<id>` lists
// every container in the project regardless of when it was created.
func projectName(instanceID id.ID) string {
	return "cp-" + instanceID.String()
}

// projectNetwork returns the user-defined bridge network name for
// service-to-service DNS within a project.
func projectNetwork(instanceID id.ID) string {
	return projectName(instanceID) + "-net"
}

// serviceContainerName returns the container name for one service
// inside a project. Convention is `cp-<instanceID>-<serviceName>` so:
//
//   - `docker ps --filter name=cp-<id>-` lists every container in the
//     project,
//   - the trailing `<serviceName>` is the network alias, so siblings
//     resolve `<serviceName>` (no fully qualified name needed).
func serviceContainerName(instanceID id.ID, serviceName string) string {
	return projectName(instanceID) + "-" + serviceName
}

// projectLabels returns the docker labels stamped on every container
// + network in a project. Used by Status/Deprovision to enumerate
// project members without having to know the service list.
func projectLabels(instanceID id.ID, tenantID, serviceName string, role provider.ServiceRole, extra map[string]string) map[string]string {
	labels := make(map[string]string, len(extra)+5)
	maps.Copy(labels, extra)

	labels["ctrlplane.instance"] = instanceID.String()
	labels["ctrlplane.project"] = projectName(instanceID)

	if tenantID != "" {
		labels["ctrlplane.tenant"] = tenantID
	}

	if serviceName != "" {
		labels["ctrlplane.service"] = serviceName
	}

	if role != "" {
		labels["ctrlplane.role"] = string(role)
	}

	return labels
}

// provisionProject creates the project's network, runs Inits to
// completion, then creates + starts every Main/Sidecar container.
// Returns the per-service container IDs so the caller can populate
// Instance.ServiceRefs.
//
// On any failure after partial creation, the caller is expected to
// invoke Deprovision to clean up — provisionProject is best-effort
// during the create phase but does NOT roll back automatically.
// Idempotency: existing containers / networks with matching names are
// removed/dropped first so re-provision overwrites cleanly.
func (p *Provider) provisionProject(ctx context.Context, req provider.ProvisionRequest) (*provider.ProvisionResult, error) {
	if err := p.ensureProjectNetwork(ctx, req.InstanceID, req.TenantID, req.Labels); err != nil {
		return nil, err
	}

	// Run Init services first. Failure here aborts the provision —
	// Main/Sidecar containers are not created, the (already-created)
	// network stays for Deprovision to reap.
	inits, mains, sidecars := classifyServices(req.Services)
	if err := p.runInits(ctx, req, inits); err != nil {
		return nil, fmt.Errorf("docker: init services: %w", err)
	}

	serviceRefs := make(map[string]string, len(mains)+len(sidecars))
	endpoints := make([]provider.Endpoint, 0)

	// Main first, then Sidecars. Sidecars typically depend on Main
	// (for shared volumes or network namespace) — starting in this
	// order makes "wait for Main healthy before sidecar" cheap if a
	// future iteration adds health-gating.
	for _, svc := range append(mains, sidecars...) {
		ref, eps, err := p.createServiceContainer(ctx, req, svc)
		if err != nil {
			return nil, fmt.Errorf("docker: service %q: %w", svc.Name, err)
		}

		serviceRefs[svc.Name] = ref

		endpoints = append(endpoints, eps...)
	}

	return &provider.ProvisionResult{
		ProviderRef: "docker:" + projectName(req.InstanceID),
		ServiceRefs: serviceRefs,
		Endpoints:   endpoints,
	}, nil
}

// classifyServices splits a service slice into init / main / sidecar
// groups, sorted by DependsOn so siblings declared after their
// dependencies still come up in the right order.
func classifyServices(services []provider.ServiceSpec) (inits, mains, sidecars []provider.ServiceSpec) {
	for _, svc := range services {
		switch svc.Role {
		case provider.RoleInit:
			inits = append(inits, svc)
		case provider.RoleSidecar:
			sidecars = append(sidecars, svc)
		default: // RoleMain or empty
			mains = append(mains, svc)
		}
	}

	sortByDeps(inits)
	sortByDeps(mains)
	sortByDeps(sidecars)

	return inits, mains, sidecars
}

// sortByDeps performs a stable topological sort on a service slice by
// DependsOn within the slice. Services that reference siblings outside
// the slice are kept in their relative order — that's expected: a
// Sidecar depending on Main is handled by classifyServices's
// "main-first, sidecar-second" sweep, not by sortByDeps.
func sortByDeps(services []provider.ServiceSpec) {
	if len(services) <= 1 {
		return
	}

	indexOf := make(map[string]int, len(services))
	for i := range services {
		indexOf[services[i].Name] = i
	}

	sort.SliceStable(services, func(i, j int) bool {
		// j depends on i → i comes first.
		return slices.Contains(services[j].DependsOn, services[i].Name)
	})
}

// ensureProjectNetwork creates the user-defined bridge network for a
// project, idempotently. If a network with the same name already
// exists (re-provisioning) we leave it in place — its connected
// containers may belong to the project we're about to refresh.
func (p *Provider) ensureProjectNetwork(ctx context.Context, instanceID id.ID, tenantID string, extraLabels map[string]string) error {
	name := projectNetwork(instanceID)

	if _, err := p.cli.NetworkInspect(ctx, name, network.InspectOptions{}); err == nil {
		return nil
	} else if !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("docker: inspect network %s: %w", name, err)
	}

	labels := projectLabels(instanceID, tenantID, "", "", extraLabels)

	if _, err := p.cli.NetworkCreate(ctx, name, network.CreateOptions{
		Driver: "bridge",
		Labels: labels,
	}); err != nil {
		return fmt.Errorf("docker: create network %s: %w", name, err)
	}

	return nil
}

// createServiceContainer creates and starts one Main or Sidecar
// container in the project. Init services use runInits instead.
func (p *Provider) createServiceContainer(ctx context.Context, req provider.ProvisionRequest, svc provider.ServiceSpec) (string, []provider.Endpoint, error) {
	name := serviceContainerName(req.InstanceID, svc.Name)

	if err := p.removeIfExists(ctx, name); err != nil {
		return "", nil, err
	}

	_ = p.pullImage(ctx, svc.Image)

	cfg, hostCfg, netCfg, err := p.buildServiceContainerConfig(req, svc)
	if err != nil {
		return "", nil, err
	}

	created, err := p.cli.ContainerCreate(ctx, cfg, hostCfg, netCfg, nil, name)
	if err != nil {
		return "", nil, fmt.Errorf("create container %s: %w", name, err)
	}

	if err := p.cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		// Best-effort cleanup so retries don't trip the unique-name check.
		_ = p.cli.ContainerRemove(ctx, created.ID, container.RemoveOptions{Force: true})

		return "", nil, fmt.Errorf("start container %s: %w", name, err)
	}

	endpoints, err := p.endpointsFor(ctx, created.ID, name)
	if err != nil {
		return "", nil, fmt.Errorf("inspect endpoints %s: %w", name, err)
	}

	// Tag every endpoint with the owning service name so per-service
	// routes can target a specific container even when two services
	// publish the same numeric port.
	for i := range endpoints {
		endpoints[i].ServiceName = svc.Name
	}

	return created.ID, endpoints, nil
}

// buildServiceContainerConfig assembles the docker create-call inputs
// for one ServiceSpec. Pulled out into its own function for use by
// both Provision (createServiceContainer) and Deploy (which recreates
// containers per the same config).
func (p *Provider) buildServiceContainerConfig(req provider.ProvisionRequest, svc provider.ServiceSpec) (*container.Config, *container.HostConfig, *network.NetworkingConfig, error) {
	envList := make([]string, 0, len(svc.Env))
	for k, v := range svc.Env {
		envList = append(envList, k+"="+v)
	}

	exposedPorts, portBindings, err := buildPortConfig(svc.Ports)
	if err != nil {
		return nil, nil, nil, err
	}

	cfg := &container.Config{
		Image:        svc.Image,
		Env:          envList,
		Labels:       projectLabels(req.InstanceID, req.TenantID, svc.Name, svc.Role, mergeStringMaps(req.Labels, svc.Annotations)),
		ExposedPorts: exposedPorts,
		Entrypoint:   svc.Command,
		Cmd:          svc.Args,
	}

	hostCfg := &container.HostConfig{
		PortBindings:  portBindings,
		NetworkMode:   container.NetworkMode(projectNetwork(req.InstanceID)),
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
	}

	if svc.Role == provider.RoleInit {
		// Init containers run-once: don't auto-restart and don't bind
		// host ports (init logic shouldn't be reachable from outside).
		hostCfg.RestartPolicy = container.RestartPolicy{Name: container.RestartPolicyDisabled}
		hostCfg.PortBindings = nil
		cfg.ExposedPorts = nil
	}

	netCfg := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			projectNetwork(req.InstanceID): {
				Aliases: []string{svc.Name},
			},
		},
	}

	return cfg, hostCfg, netCfg, nil
}

// mergeStringMaps merges b into a, returning a fresh map. Keys in b
// overwrite a (used for the project_labels + per-service annotations
// pattern where annotations are the more specific source).
func mergeStringMaps(a, b map[string]string) map[string]string {
	out := make(map[string]string, len(a)+len(b))
	maps.Copy(out, a)
	maps.Copy(out, b)

	return out
}

// listProjectContainers enumerates every container in a project via
// the project label. Used by Status and Deprovision so they don't
// need to know the current ServiceSpec slice (which may have changed
// since provision).
func (p *Provider) listProjectContainers(ctx context.Context, instanceID id.ID) ([]projectContainer, error) {
	args := filters.NewArgs()
	args.Add("label", "ctrlplane.project="+projectName(instanceID))

	containers, err := p.cli.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: args,
	})
	if err != nil {
		return nil, fmt.Errorf("docker: list project containers: %w", err)
	}

	out := make([]projectContainer, 0, len(containers))
	for _, c := range containers {
		out = append(out, projectContainer{
			ID:          c.ID,
			Name:        firstName(c.Names),
			ServiceName: c.Labels["ctrlplane.service"],
			Role:        provider.ServiceRole(c.Labels["ctrlplane.role"]),
			State:       c.State,
		})
	}

	return out, nil
}

// projectContainer is the minimal projection of a docker container
// the project-level methods need: enough to map container → service
// + check liveness, no inspect-level detail.
type projectContainer struct {
	ID          string
	Name        string
	ServiceName string
	Role        provider.ServiceRole
	State       string
}

// firstName returns the first non-leading-slash name from a docker
// container's Names slice. Docker returns names with a leading "/"
// (e.g. "/cp-foo-bar"); strip it for display.
func firstName(names []string) string {
	if len(names) == 0 {
		return ""
	}

	if names[0] != "" && names[0][0] == '/' {
		return names[0][1:]
	}

	return names[0]
}

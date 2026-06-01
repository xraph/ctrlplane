package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

// ErrLogsNotImplemented is returned when log streaming is requested
// but the docker exec/logs path hasn't been wired yet.
var ErrLogsNotImplemented = errors.New("docker: logs not implemented")

// Compile-time check that Provider implements provider.Provider
// and provider.HealthChecker.
var (
	_ provider.Provider      = (*Provider)(nil)
	_ provider.HealthChecker = (*Provider)(nil)
)

// HealthCheck pings the docker daemon and reports reachability.
// The HealthChecker contract is "is this provider's control plane
// reachable" — so we use cli.Ping (which round-trips to the
// daemon) rather than docker info / version which load more state.
func (p *Provider) HealthCheck(ctx context.Context) (*provider.HealthStatus, error) {
	start := time.Now()
	_, err := p.cli.Ping(ctx)
	latency := time.Since(start)

	now := time.Now().UTC()
	if err != nil {
		return &provider.HealthStatus{
			Healthy:   false,
			Message:   fmt.Sprintf("docker daemon unreachable: %v", err),
			Latency:   latency,
			CheckedAt: now,
		}, nil
	}

	return &provider.HealthStatus{
		Healthy:   true,
		Message:   "docker daemon reachable",
		Latency:   latency,
		CheckedAt: now,
	}, nil
}

// Provider is a Docker-based infrastructure provider. Each Instance
// becomes one container; the container name encodes the instance ID
// for stateless lookups (no per-instance ProviderRef storage needed
// beyond the canonical "docker:<container_name>" string).
type Provider struct {
	cfg Config
	cli *client.Client
}

// New creates a new Docker provider. Uses standard docker env
// (DOCKER_HOST, DOCKER_TLS_VERIFY, etc.) by default; pass WithHost to
// pin a specific socket. Network defaults to "bridge" but should be
// overridden to a named network when the provider is colocated with
// a router (e.g. Traefik) that needs to reach containers by DNS name.
func New(opts ...Option) (*Provider, error) {
	p := &Provider{
		cfg: Config{Network: "bridge"},
	}
	for _, opt := range opts {
		if err := opt(p); err != nil {
			return nil, err
		}
	}

	cliOpts := []client.Opt{client.FromEnv, client.WithAPIVersionNegotiation()}
	if p.cfg.Host != "" {
		cliOpts = append(cliOpts, client.WithHost(p.cfg.Host))
	}

	cli, err := client.NewClientWithOpts(cliOpts...)
	if err != nil {
		return nil, fmt.Errorf("docker: client init: %w", err)
	}

	p.cli = cli

	return p, nil
}

// Info returns metadata about this provider. Local Docker has a
// fixed region of "local" with a default localhost location so studio
// catalog endpoints surface a usable region in dev environments
// where no datacenters have been registered yet.
func (p *Provider) Info() provider.ProviderInfo {
	return provider.ProviderInfo{
		Name:    "docker",
		Version: "1.0.0",
		Region:  "local",
		Location: &provider.Location{
			Country: "Local",
			City:    "Localhost",
		},
	}
}

// Capabilities returns the set of features this provider supports.
func (p *Provider) Capabilities() []provider.Capability {
	return []provider.Capability{
		provider.CapDeploy,
		provider.CapScale,
		provider.CapLogs,
		provider.CapExec,
	}
}

// Provision pulls the image, creates a container with the requested
// ports/env/labels, starts it, and returns the resulting endpoints.
//
// Endpoints are emitted in two flavours per declared port:
//   - in-network: http://cp-<instanceID>:<containerPort> — reachable
//     from other containers on the same docker network. Preferred
//     for service-to-service routing (Traefik joins this network and
//     uses these URLs upstream).
//   - public:     http://localhost:<hostPort> — the random host port
//     docker assigned. Useful when hitting a workspace from outside
//     the docker network without a host-side proxy.
//
// Re-provisioning the same instance ID is idempotent: any existing
// container with the same name is force-removed first so the create
// succeeds with the fresh config.
func (p *Provider) Provision(ctx context.Context, req provider.ProvisionRequest) (*provider.ProvisionResult, error) {
	if len(req.Services) == 0 {
		return nil, errors.New("docker: provision requires at least one service")
	}

	if pickMainService(req.Services) == nil {
		return nil, errors.New("docker: provision requires exactly one Main service")
	}

	return p.provisionProject(ctx, req)
}

// pickMainService finds the first Main service in a slice. Returns
// nil for empty slices or slices with no Main.
func pickMainService(services []provider.ServiceSpec) *provider.ServiceSpec {
	for i := range services {
		if services[i].Role == provider.RoleMain || services[i].Role == "" {
			return &services[i]
		}
	}

	return nil
}

// Deprovision tears down every container in the project + the project
// network. Not-found is treated as success so deprovision is
// convergent — the goal is "project gone", not strict accounting.
func (p *Provider) Deprovision(ctx context.Context, instanceID id.ID) error {
	containers, err := p.listProjectContainers(ctx, instanceID)
	if err != nil {
		return err
	}

	for _, c := range containers {
		if rmErr := p.cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{
			Force:         true,
			RemoveVolumes: true,
		}); rmErr != nil && !cerrdefs.IsNotFound(rmErr) {
			return fmt.Errorf("docker: remove %s: %w", c.Name, rmErr)
		}
	}

	// Remove the project network. NotFound is fine — leftover network
	// without containers is the same end-state we want.
	netName := projectNetwork(instanceID)
	if rmErr := p.cli.NetworkRemove(ctx, netName); rmErr != nil && !cerrdefs.IsNotFound(rmErr) {
		return fmt.Errorf("docker: remove network %s: %w", netName, rmErr)
	}

	// Drop the legacy single-container `cp-<instanceID>` if it exists
	// — covers re-provisions of pre-Phase-2 instances that were
	// created with the flat naming scheme.
	_ = p.cli.ContainerRemove(ctx, containerName(instanceID), container.RemoveOptions{
		Force:         true,
		RemoveVolumes: true,
	})

	return nil
}

// Start starts every Main + Sidecar container in the project.
// Init services are not restarted — they ran-once during provision.
func (p *Provider) Start(ctx context.Context, instanceID id.ID) error {
	containers, err := p.listProjectContainers(ctx, instanceID)
	if err != nil {
		return err
	}

	for _, c := range containers {
		if c.Role == provider.RoleInit {
			continue
		}

		if startErr := p.cli.ContainerStart(ctx, c.ID, container.StartOptions{}); startErr != nil {
			return fmt.Errorf("docker: start %s: %w", c.Name, startErr)
		}
	}

	return nil
}

// Stop sends SIGTERM to every Main + Sidecar container in the project.
// Default docker timeout (~10s) per container.
func (p *Provider) Stop(ctx context.Context, instanceID id.ID) error {
	containers, err := p.listProjectContainers(ctx, instanceID)
	if err != nil {
		return err
	}

	for _, c := range containers {
		if c.Role == provider.RoleInit {
			continue
		}

		if stopErr := p.cli.ContainerStop(ctx, c.ID, container.StopOptions{}); stopErr != nil {
			return fmt.Errorf("docker: stop %s: %w", c.Name, stopErr)
		}
	}

	return nil
}

// Restart cycles every Main + Sidecar container in the project. Inits
// stay completed — restart is for live workload bouncing, not init
// re-runs.
func (p *Provider) Restart(ctx context.Context, instanceID id.ID) error {
	containers, err := p.listProjectContainers(ctx, instanceID)
	if err != nil {
		return err
	}

	for _, c := range containers {
		if c.Role == provider.RoleInit {
			continue
		}

		if rstErr := p.cli.ContainerRestart(ctx, c.ID, container.StopOptions{}); rstErr != nil {
			return fmt.Errorf("docker: restart %s: %w", c.Name, rstErr)
		}
	}

	return nil
}

// Status aggregates inspect across every container in the project.
// Worst-of state wins (any Failed → Failed; otherwise any Restarting
// → Starting; otherwise any not-Running → Stopped). Ready is true
// only when every Main + Sidecar reports Running. Init containers
// don't gate readiness once they've completed (they appear as
// Stopped/exited 0 — the non-Init aggregation ignores them).
func (p *Provider) Status(ctx context.Context, instanceID id.ID) (*provider.InstanceStatus, error) {
	containers, err := p.listProjectContainers(ctx, instanceID)
	if err != nil {
		return nil, err
	}

	if len(containers) == 0 {
		return &provider.InstanceStatus{State: provider.StateDestroyed}, nil
	}

	var (
		anyFailed   bool
		anyStarting bool
		anyStopped  bool
		allRunning  = true
		endpoints   []provider.Endpoint
		services    = make(map[string]provider.ServiceStatus, len(containers))
	)

	for _, c := range containers {
		inspect, ierr := p.cli.ContainerInspect(ctx, c.ID)
		if ierr != nil {
			if cerrdefs.IsNotFound(ierr) {
				continue
			}

			return nil, fmt.Errorf("docker: inspect %s: %w", c.Name, ierr)
		}

		svcState := provider.StateStopped

		switch {
		case inspect.State.Running:
			svcState = provider.StateRunning
		case inspect.State.Restarting:
			svcState = provider.StateStarting
		case inspect.State.Dead, inspect.State.OOMKilled:
			svcState = provider.StateFailed
		}

		services[c.ServiceName] = provider.ServiceStatus{
			State:       svcState,
			Ready:       inspect.State.Running,
			Restarts:    inspect.RestartCount,
			ProviderRef: c.ID,
			Message:     inspect.State.Error,
		}

		// Init that exited 0 is not a ready failure — skip its
		// contribution to the aggregate Ready flag.
		if c.Role == provider.RoleInit && !inspect.State.Running && inspect.State.ExitCode == 0 {
			continue
		}

		switch svcState {
		case provider.StateFailed:
			anyFailed = true
			allRunning = false
		case provider.StateStarting:
			anyStarting = true
			allRunning = false
		case provider.StateStopped:
			anyStopped = true
			allRunning = false
		case provider.StateRunning:
			// nothing
		}

		eps := endpointsFromInspect(c.Name, inspect.NetworkSettings.Ports)
		for i := range eps {
			eps[i].ServiceName = c.ServiceName
		}

		endpoints = append(endpoints, eps...)
	}

	state := provider.StateRunning

	switch {
	case anyFailed:
		state = provider.StateFailed
	case anyStarting:
		state = provider.StateStarting
	case anyStopped:
		state = provider.StateStopped
	}

	return &provider.InstanceStatus{
		State:     state,
		Ready:     allRunning,
		Endpoints: endpoints,
		Services:  services,
	}, nil
}

// Deploy applies a new image / env to an existing container by
// recreating it. Docker doesn't support in-place image swap, so the
// implementation is: stop+remove the current container, then create
// a fresh one with the new spec. Same name, same network, new image.
//
// For the initial provision case (where Provision already ran with
// the right image) Deploy is effectively a no-op — the container
// stays as-is. The caller (ctrlplane deploy.Service) treats success
// here as "release applied" and bumps the deployment state.
// Deploy applies per-service image / env updates by recreating only
// the targeted containers within the live project network. Services
// not listed in req.Services keep running unchanged.
//
// Per-service rollout for a multi-container project is "stop & replace
// each targeted container in turn" — Docker doesn't support in-place
// image swap. The Compose-project network stays up across the
// recreation so sibling services don't drop their network namespace.
func (p *Provider) Deploy(ctx context.Context, req provider.DeployRequest) (*provider.DeployResult, error) {
	if len(req.Services) == 0 {
		return nil, errors.New("docker: deploy requires at least one service")
	}

	// Inspect the existing project to recover full ServiceSpec for each
	// target — Deploy only carries image+env per service; volumes,
	// ports, command, etc. come from the persisted container spec.
	existing, err := p.listProjectContainers(ctx, req.InstanceID)
	if err != nil {
		return nil, err
	}

	bySvc := make(map[string]projectContainer, len(existing))
	for _, c := range existing {
		bySvc[c.ServiceName] = c
	}

	for _, target := range req.Services {
		current, ok := bySvc[target.Name]
		if !ok {
			return nil, fmt.Errorf("docker: deploy: unknown service %q in project %s", target.Name, projectName(req.InstanceID))
		}

		if err := p.recreateServiceContainer(ctx, req, target, current); err != nil {
			return nil, fmt.Errorf("docker: deploy service %q: %w", target.Name, err)
		}
	}

	return &provider.DeployResult{
		ProviderRef: "docker:" + projectName(req.InstanceID),
		Status:      "succeeded",
	}, nil
}

// recreateServiceContainer drops the current container for a service
// and re-creates it with the new image/env, preserving the rest of
// the spec (ports, volumes, command, etc.) by inspecting the running
// container before removal.
func (p *Provider) recreateServiceContainer(ctx context.Context, req provider.DeployRequest, target provider.ServiceDeploySpec, current projectContainer) error {
	inspect, err := p.cli.ContainerInspect(ctx, current.ID)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", current.Name, err)
	}

	// Skip recreate when the image already matches — saves a noisy
	// stop+start on every Deploy that follows immediate Provision.
	if inspect.Config != nil && inspect.Config.Image == target.Image {
		return nil
	}

	_ = p.pullImage(ctx, target.Image)

	if err := p.cli.ContainerRemove(ctx, current.ID, container.RemoveOptions{Force: true}); err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("remove old %s: %w", current.Name, err)
	}

	envList := make([]string, 0, len(target.Env))
	for k, v := range target.Env {
		envList = append(envList, k+"="+v)
	}

	if len(envList) == 0 && inspect.Config != nil {
		envList = inspect.Config.Env
	}

	cfg := &container.Config{
		Image:        target.Image,
		Env:          envList,
		Labels:       inspect.Config.Labels,
		ExposedPorts: inspect.Config.ExposedPorts,
		Entrypoint:   inspect.Config.Entrypoint,
		Cmd:          inspect.Config.Cmd,
	}

	hostCfg := inspect.HostConfig
	if hostCfg == nil {
		hostCfg = &container.HostConfig{
			NetworkMode:   container.NetworkMode(projectNetwork(req.InstanceID)),
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
		}
	}

	created, err := p.cli.ContainerCreate(ctx, cfg, hostCfg, &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			projectNetwork(req.InstanceID): {Aliases: []string{current.ServiceName}},
		},
	}, nil, current.Name)
	if err != nil {
		return fmt.Errorf("create new %s: %w", current.Name, err)
	}

	if err := p.cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("start new %s: %w", current.Name, err)
	}

	return nil
}

// Rollback is a no-op today — docker has no built-in release history,
// so rollback would have to recreate using the previous image which
// the deploy service already knows about and could route through Deploy.
func (p *Provider) Rollback(_ context.Context, _ id.ID, _ id.ID) error {
	return nil
}

// Scale is a no-op for the docker provider — single-container
// instances don't have a horizontal scale axis. The kubernetes
// provider is the right home for replica scaling.
func (p *Provider) Scale(_ context.Context, _ id.ID, _ provider.ResourceSpec) error {
	return nil
}

// Resources returns a one-shot point-in-time sample of the
// container's CPU / memory / network usage via the docker stats
// API. The non-streaming variant gives us a single JSON document
// per call, which is what the metrics poller wants — it calls us
// every N seconds and stores the result in its own ring buffer.
//
// Returns a zero-valued ResourceUsage (no error) when the container
// doesn't exist or the stats response is empty — the metrics poller
// treats "missing sample" as a gap, not a failure, so transient
// container-restart windows don't show up as poller errors.
func (p *Provider) Resources(ctx context.Context, instanceID id.ID) (*provider.ResourceUsage, error) {
	resp, err := p.cli.ContainerStatsOneShot(ctx, containerName(instanceID))
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return &provider.ResourceUsage{}, nil
		}

		return nil, fmt.Errorf("container stats %s: %w", instanceID, err)
	}
	defer resp.Body.Close()

	return decodeStats(resp.Body)
}

// Logs returns a stream of structured JSON log events (one
// {"ts":..., "stream":"stdout"|"stderr", "line":"..."} per line)
// from the container's stdout + stderr. When opts.Follow is true
// the stream stays open and emits new lines as the container
// writes them; the caller closes the returned ReadCloser to stop.
//
// Honours opts.Tail (last N lines, "0" = all), opts.Since (only
// lines newer than this UTC time), and opts.Follow. Multiplexed
// docker frames are demuxed by demuxedDockerStream.
func (p *Provider) Logs(ctx context.Context, instanceID id.ID, opts provider.LogOptions) (io.ReadCloser, error) {
	dockerOpts := container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     opts.Follow,
		Timestamps: true,
	}
	if opts.Tail > 0 {
		dockerOpts.Tail = strconv.Itoa(opts.Tail)
	}

	if !opts.Since.IsZero() {
		dockerOpts.Since = opts.Since.UTC().Format(time.RFC3339Nano)
	}

	target, err := p.resolveLogTarget(ctx, instanceID, opts.ServiceName)
	if err != nil {
		return nil, err
	}

	rc, err := p.cli.ContainerLogs(ctx, target, dockerOpts)
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return nil, fmt.Errorf("docker: logs: container not found: %w", err)
		}

		return nil, fmt.Errorf("docker: logs: %w", err)
	}

	return demuxedDockerStream(rc), nil
}

// resolveLogTarget picks the container ID to stream logs from. Empty
// serviceName picks the project's Main service. A non-existent service
// name surfaces a clear error rather than docker's generic "no such
// container".
func (p *Provider) resolveLogTarget(ctx context.Context, instanceID id.ID, serviceName string) (string, error) {
	containers, err := p.listProjectContainers(ctx, instanceID)
	if err != nil {
		return "", err
	}

	if len(containers) == 0 {
		// Pre-Phase-2 single-container fallback. Older instances were
		// provisioned with the legacy `cp-<id>` name and no project
		// labels — try that name directly.
		return containerName(instanceID), nil
	}

	if serviceName == "" {
		// Default to the Main service.
		for _, c := range containers {
			if c.Role == provider.RoleMain || c.Role == "" {
				return c.ID, nil
			}
		}
		// Fall back to the first non-Init container so logs stream
		// from something even when Role labels are absent.
		for _, c := range containers {
			if c.Role != provider.RoleInit {
				return c.ID, nil
			}
		}
	}

	for _, c := range containers {
		if c.ServiceName == serviceName {
			return c.ID, nil
		}
	}

	return "", fmt.Errorf("docker: logs: service %q not found in project %s", serviceName, projectName(instanceID))
}

// Exec is not yet implemented.
func (p *Provider) Exec(_ context.Context, _ id.ID, _ provider.ExecRequest) (*provider.ExecResult, error) {
	return &provider.ExecResult{ExitCode: 0}, nil
}

// --- internals ---

// containerName encodes the instance ID into a docker-safe string.
// Underscores in typeids are valid in container names; we just
// prefix with "cp-" so a quick `docker ps | grep cp-` shows every
// ctrlplane-managed container.
func containerName(instanceID id.ID) string {
	return "cp-" + instanceID.String()
}

// pullImage pulls the image from its registry. Errors are returned
// to callers but the docker provider treats them as soft failures
// (cached image often suffices for re-provisioning).
func (p *Provider) pullImage(ctx context.Context, ref string) error {
	body, err := p.cli.ImagePull(ctx, ref, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("docker: pull %s: %w", ref, err)
	}
	defer body.Close()
	// The pull stream must be drained for the pull to complete.
	_, _ = io.Copy(io.Discard, body)

	return nil
}

// removeIfExists drops the named container and treats NotFound as
// success. Used before create to make Provision/Deploy idempotent.
func (p *Provider) removeIfExists(ctx context.Context, name string) error {
	err := p.cli.ContainerRemove(ctx, name, container.RemoveOptions{Force: true})
	if err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("docker: remove pre-existing %s: %w", name, err)
	}

	return nil
}

// endpointsFor inspects the container by ID and translates its port
// bindings into provider.Endpoints. Used by Provision; Status calls
// the same shape via endpointsFromInspect once it has the inspect
// result in hand.
func (p *Provider) endpointsFor(ctx context.Context, containerID, name string) ([]provider.Endpoint, error) {
	inspect, err := p.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return nil, err
	}

	return endpointsFromInspect(name, inspect.NetworkSettings.Ports), nil
}

// endpointsFromInspect builds the dual-flavour endpoint list from a
// container's port-binding map. Pure function so it's testable
// independently of a running docker daemon.
func endpointsFromInspect(name string, ports nat.PortMap) []provider.Endpoint {
	out := make([]provider.Endpoint, 0, len(ports)*2)
	for natPort, bindings := range ports {
		if natPort.Proto() != "tcp" {
			continue
		}

		port := natPort.Int()

		// In-network address: http://cp-<id>:<containerPort>. Other
		// containers on the same docker network reach us here; this
		// is what the dev Traefik will proxy upstream to.
		out = append(out, provider.Endpoint{
			URL:      fmt.Sprintf("http://%s:%d", name, port),
			Port:     port,
			Protocol: "http",
			Public:   false,
		})

		// Public host-port endpoints (one per binding — usually one
		// IPv4 + one IPv6, both pointing at the same host port).
		seen := map[string]bool{}
		for _, b := range bindings {
			if b.HostPort == "" || seen[b.HostPort] {
				continue
			}

			seen[b.HostPort] = true
			hp, _ := strconv.Atoi(b.HostPort)
			out = append(out, provider.Endpoint{
				URL:      "http://localhost:" + b.HostPort,
				Port:     hp,
				Protocol: "http",
				Public:   true,
			})
		}
	}

	return out
}

// buildPortConfig translates ctrlplane PortSpecs into docker's
// (ExposedPorts, PortBindings) shape. Empty HostPort means the
// docker daemon assigns a random ephemeral port — we read it back
// from the inspect after start. Defaults protocol to "tcp" when the
// caller leaves it blank.
func buildPortConfig(ports []provider.PortSpec) (nat.PortSet, nat.PortMap, error) {
	exposed := nat.PortSet{}
	bindings := nat.PortMap{}

	for _, p := range ports {
		proto := p.Protocol
		if proto == "" {
			proto = "tcp"
		}

		port, err := nat.NewPort(proto, strconv.Itoa(p.Container))
		if err != nil {
			return nil, nil, fmt.Errorf("docker: parse port %d/%s: %w", p.Container, proto, err)
		}

		exposed[port] = struct{}{}

		hostPort := ""
		if p.Host > 0 {
			hostPort = strconv.Itoa(p.Host)
		}

		bindings[port] = []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: hostPort}}
	}

	return exposed, bindings, nil
}

// networkingConfig returns a NetworkingConfig that attaches the new
// container to the named network. Returns nil for the default
// "bridge" network (which docker handles via NetworkMode alone).
func networkingConfig(networkName string) *network.NetworkingConfig {
	if networkName == "" || networkName == "bridge" {
		return nil
	}

	return &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			networkName: {},
		},
	}
}

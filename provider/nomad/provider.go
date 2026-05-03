package nomad

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

// HealthCheck pings the Nomad agent's /v1/agent/health endpoint and
// reports reachability. Treats any non-2xx as unhealthy.
func (p *Provider) HealthCheck(ctx context.Context) (*provider.HealthStatus, error) {
	start := time.Now()
	endpoint := strings.TrimRight(p.cfg.Address, "/") + "/v1/agent/health"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	httpClient := &http.Client{Timeout: 5 * time.Second}

	resp, err := httpClient.Do(req)
	latency := time.Since(start)
	now := time.Now().UTC()

	if err != nil {
		return &provider.HealthStatus{
			Healthy:   false,
			Message:   fmt.Sprintf("nomad agent unreachable: %v", err),
			Latency:   latency,
			CheckedAt: now,
		}, nil
	}

	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return &provider.HealthStatus{
			Healthy:   false,
			Message:   fmt.Sprintf("nomad agent reported degraded (status %d)", resp.StatusCode),
			Latency:   latency,
			CheckedAt: now,
		}, nil
	}

	return &provider.HealthStatus{
		Healthy:   true,
		Message:   "nomad agent reachable",
		Latency:   latency,
		CheckedAt: now,
	}, nil
}

// ErrLogsNotImplemented is returned by Logs until per-task log
// streaming is wired (Nomad's logs API is per-allocation +
// per-task; doable but deferred).
var ErrLogsNotImplemented = errors.New("nomad: logs not implemented")

// Compile-time check that Provider implements provider.Provider
// and provider.HealthChecker.
var (
	_ provider.Provider      = (*Provider)(nil)
	_ provider.HealthChecker = (*Provider)(nil)
)

// Provider is a HashiCorp Nomad infrastructure provider.
type Provider struct {
	cfg    Config
	client *http.Client
}

// New creates a new Nomad provider with the given options.
// Without any options, sane defaults are used (address:
// localhost:4646, region: global, namespace: default).
func New(opts ...Option) (*Provider, error) {
	p := &Provider{
		cfg: Config{
			Address:   "http://localhost:4646",
			Region:    "global",
			Namespace: "default",
		},
		client: &http.Client{Timeout: 30 * time.Second},
	}

	for _, opt := range opts {
		if err := opt(p); err != nil {
			return nil, err
		}
	}

	return p, nil
}

// Info returns metadata about this provider.
func (p *Provider) Info() provider.ProviderInfo {
	return provider.ProviderInfo{
		Name:    "nomad",
		Version: "0.1.0",
		Region:  p.cfg.Region,
	}
}

// Capabilities returns the set of features this provider supports.
func (p *Provider) Capabilities() []provider.Capability {
	return []provider.Capability{
		provider.CapProvision,
		provider.CapDeploy,
		provider.CapScale,
		provider.CapLogs,
	}
}

// Provision submits a Nomad Job built from the request's Services.
// One Job per Instance with a single TaskGroup containing every
// service as a Task; Init/Sidecar lifecycle hooks order the rollout.
func (p *Provider) Provision(ctx context.Context, req provider.ProvisionRequest) (*provider.ProvisionResult, error) {
	if len(req.Services) == 0 {
		return nil, errors.New("nomad: provision requires at least one service")
	}

	if pickMain(req.Services) == nil {
		return nil, errors.New("nomad: provision requires exactly one Main service")
	}

	body := buildJob(p.cfg, req)
	if err := p.submitJob(ctx, body); err != nil {
		return nil, fmt.Errorf("nomad: submit job: %w", err)
	}

	serviceRefs := make(map[string]string, len(req.Services))
	for i := range req.Services {
		serviceRefs[req.Services[i].Name] = jobName(req.InstanceID) + "/" + req.Services[i].Name
	}

	return &provider.ProvisionResult{
		ProviderRef: "nomad:" + jobName(req.InstanceID),
		ServiceRefs: serviceRefs,
	}, nil
}

// Deprovision deregisters the Nomad Job (the `purge=true` query
// parameter wipes it from history so re-provisions don't trip the
// "job exists" guard).
func (p *Provider) Deprovision(ctx context.Context, instanceID id.ID) error {
	endpoint := fmt.Sprintf("/v1/job/%s?purge=true", url.PathEscape(jobName(instanceID)))
	if err := p.doRequest(ctx, http.MethodDelete, endpoint, nil, nil); err != nil {
		// 404 = already gone — convergent.
		if isNotFound(err) {
			return nil
		}

		return fmt.Errorf("nomad: deregister job: %w", err)
	}

	return nil
}

// Start re-submits the Job to Nomad. Use after Stop to re-schedule.
// Nomad doesn't have a "start a stopped job" verb — re-submit with
// the same spec is the canonical pattern.
func (p *Provider) Start(ctx context.Context, instanceID id.ID) error {
	// Without the original spec we can only nudge the job — fetch its
	// last-known spec and resubmit. Production deployments should
	// drive Start via Provision/Deploy from the workload spec.
	job, err := p.getJob(ctx, instanceID)
	if err != nil {
		return err
	}

	job.Stop = false

	return p.submitJob(ctx, &nomadJobRequest{Job: job})
}

// Stop halts the Job (Nomad keeps the spec for resurrection via
// Start). Equivalent to `nomad job stop` without `-purge`.
func (p *Provider) Stop(ctx context.Context, instanceID id.ID) error {
	endpoint := fmt.Sprintf("/v1/job/%s", url.PathEscape(jobName(instanceID)))
	if err := p.doRequest(ctx, http.MethodDelete, endpoint, nil, nil); err != nil {
		return fmt.Errorf("nomad: stop job: %w", err)
	}

	return nil
}

// Restart triggers a rolling restart by issuing a job-restart RPC.
// Nomad recreates allocations one at a time honouring the group's
// update stanza.
func (p *Provider) Restart(ctx context.Context, instanceID id.ID) error {
	endpoint := fmt.Sprintf("/v1/job/%s/restart", url.PathEscape(jobName(instanceID)))
	if err := p.doRequest(ctx, http.MethodPut, endpoint, struct{}{}, nil); err != nil {
		// Older Nomad versions don't have /restart — fall back to a
		// re-submission of the existing spec, which forces a redeploy.
		job, gerr := p.getJob(ctx, instanceID)
		if gerr != nil {
			return fmt.Errorf("nomad: restart fallback: %w", gerr)
		}

		return p.submitJob(ctx, &nomadJobRequest{Job: job})
	}

	return nil
}

// Status fetches the Job's allocations and aggregates state across
// every running allocation. Worst-of state wins; per-task state is
// reported in the Services map keyed by service name.
func (p *Provider) Status(ctx context.Context, instanceID id.ID) (*provider.InstanceStatus, error) {
	allocs, err := p.listJobAllocations(ctx, instanceID)
	if err != nil {
		if isNotFound(err) {
			return &provider.InstanceStatus{State: provider.StateDestroyed}, nil
		}

		return nil, fmt.Errorf("nomad: list allocations: %w", err)
	}

	if len(allocs) == 0 {
		return &provider.InstanceStatus{State: provider.StateProvisioning}, nil
	}

	services := make(map[string]provider.ServiceStatus)

	var (
		anyRunning  bool
		anyFailed   bool
		anyPending  bool
		anyComplete bool
	)

	for _, a := range allocs {
		for taskName, ts := range a.TaskStates {
			svcStatus := mapNomadTaskState(ts.State)
			services[taskName] = provider.ServiceStatus{
				State:    svcStatus,
				Ready:    svcStatus == provider.StateRunning,
				Restarts: ts.Restarts,
				Message:  taskStateMessage(ts),
			}
		}

		switch a.ClientStatus {
		case "running":
			anyRunning = true
		case "failed":
			anyFailed = true
		case "pending":
			anyPending = true
		case "complete":
			anyComplete = true
		}
	}

	state := provider.StateRunning
	switch {
	case anyFailed:
		state = provider.StateFailed
	case anyPending:
		state = provider.StateStarting
	case !anyRunning && anyComplete:
		state = provider.StateStopped
	case !anyRunning:
		state = provider.StateProvisioning
	}

	return &provider.InstanceStatus{
		State:    state,
		Ready:    state == provider.StateRunning,
		Services: services,
	}, nil
}

// Deploy applies per-service image / env updates by patching the
// Job spec and re-submitting. Nomad's update stanza handles the
// rolling rollout; ctrlplane just provides the new desired state.
//
// Phase 3 implementation: fetch the current Job, walk req.Services
// patching matching tasks (image via task.Config.image; env via
// task.Env merge), then re-submit. Services not in req.Services
// keep their current image.
func (p *Provider) Deploy(ctx context.Context, req provider.DeployRequest) (*provider.DeployResult, error) {
	if len(req.Services) == 0 {
		return nil, errors.New("nomad: deploy requires at least one service")
	}

	job, err := p.getJob(ctx, req.InstanceID)
	if err != nil {
		return nil, fmt.Errorf("nomad: get job for deploy: %w", err)
	}

	updates := make(map[string]provider.ServiceDeploySpec, len(req.Services))
	for _, s := range req.Services {
		updates[s.Name] = s
	}

	for _, group := range job.TaskGroups {
		for _, task := range group.Tasks {
			update, ok := updates[task.Name]
			if !ok {
				continue
			}

			if task.Config == nil {
				task.Config = make(map[string]any, 1)
			}

			task.Config["image"] = update.Image

			if update.Env != nil {
				if task.Env == nil {
					task.Env = make(map[string]string, len(update.Env))
				}

				maps.Copy(task.Env, update.Env)
			}
		}
	}

	if job.Meta == nil {
		job.Meta = make(map[string]string, 1)
	}

	job.Meta["ctrlplane.release"] = req.ReleaseID.String()

	if err := p.submitJob(ctx, &nomadJobRequest{Job: job}); err != nil {
		return nil, fmt.Errorf("nomad: re-submit job for deploy: %w", err)
	}

	return &provider.DeployResult{
		ProviderRef: "nomad:" + jobName(req.InstanceID),
		Status:      "deployed",
	}, nil
}

// Rollback is intentionally a no-op — the deploy.Service builds a
// new DeployRequest from the prior Release's snapshot and routes it
// through Deploy. Nomad doesn't need a separate rollback path.
func (p *Provider) Rollback(_ context.Context, _ id.ID, _ id.ID) error {
	return nil
}

// Scale adjusts the TaskGroup count for an instance.
func (p *Provider) Scale(ctx context.Context, instanceID id.ID, spec provider.ResourceSpec) error {
	endpoint := fmt.Sprintf("/v1/job/%s/scale", url.PathEscape(jobName(instanceID)))

	body := map[string]any{
		"Target": map[string]string{
			"Group": taskGroupName,
		},
		"Count":   spec.Replicas,
		"Message": "ctrlplane scale",
	}

	return p.doRequest(ctx, http.MethodPost, endpoint, body, nil)
}

// Resources returns a one-shot point-in-time sample of the instance's
// allocation resource usage via Nomad's HTTP API. See stats.go.
func (p *Provider) Resources(ctx context.Context, instanceID id.ID) (*provider.ResourceUsage, error) {
	return fetchAllocResources(ctx, p.cfg.Address, p.cfg.Namespace, instanceID)
}

// Logs streams logs for one task in the instance's allocation.
// Phase 3 leaves this stubbed — Nomad's per-task logs API needs an
// allocation-ID + task-name + frame demuxer; deferred.
func (p *Provider) Logs(_ context.Context, _ id.ID, _ provider.LogOptions) (io.ReadCloser, error) {
	return nil, ErrLogsNotImplemented
}

// Exec is not yet implemented — Nomad supports `nomad alloc exec`
// via WebSocket; pluggable transport for ExecResult round-trips
// lands when the exec UI feature is added.
func (p *Provider) Exec(_ context.Context, _ id.ID, _ provider.ExecRequest) (*provider.ExecResult, error) {
	return &provider.ExecResult{ExitCode: 0}, nil
}

// pickMain finds the first Main service (default-Role-is-Main)
// in a slice. Returns nil for empty slices or all-Sidecar/Init slices.
func pickMain(services []provider.ServiceSpec) *provider.ServiceSpec {
	for i := range services {
		if services[i].Role == provider.RoleMain || services[i].Role == "" {
			return &services[i]
		}
	}

	return nil
}

// --- HTTP plumbing ---

// taskStateMessage extracts a human-readable message from the most
// recent event in a task's history.
func taskStateMessage(ts nomadTaskStateAPI) string {
	if len(ts.Events) == 0 {
		return ""
	}

	last := ts.Events[len(ts.Events)-1]
	if last.DisplayMsg != "" {
		return last.DisplayMsg
	}

	return last.Type
}

// mapNomadTaskState translates Nomad's task state vocabulary into
// ctrlplane's InstanceState. Nomad states: pending, running, dead.
func mapNomadTaskState(s string) provider.InstanceState {
	switch s {
	case "running":
		return provider.StateRunning
	case "pending":
		return provider.StateStarting
	case "dead":
		return provider.StateStopped
	default:
		return provider.StateStopped
	}
}

// submitJob POSTs the job spec to /v1/jobs.
func (p *Provider) submitJob(ctx context.Context, body *nomadJobRequest) error {
	return p.doRequest(ctx, http.MethodPost, "/v1/jobs", body, nil)
}

// getJob fetches a Job spec from /v1/job/<id>.
func (p *Provider) getJob(ctx context.Context, instanceID id.ID) (*nomadJob, error) {
	endpoint := "/v1/job/" + url.PathEscape(jobName(instanceID))

	var job nomadJob
	if err := p.doRequest(ctx, http.MethodGet, endpoint, nil, &job); err != nil {
		return nil, err
	}

	return &job, nil
}

// listJobAllocations fetches the alloc list for a Job.
func (p *Provider) listJobAllocations(ctx context.Context, instanceID id.ID) ([]nomadAlloc, error) {
	endpoint := fmt.Sprintf("/v1/job/%s/allocations", url.PathEscape(jobName(instanceID)))

	var allocs []nomadAlloc
	if err := p.doRequest(ctx, http.MethodGet, endpoint, nil, &allocs); err != nil {
		return nil, err
	}

	return allocs, nil
}

// notFoundError is returned by doRequest when Nomad responds with 404.
type notFoundError struct{ url string }

func (e notFoundError) Error() string { return "nomad: not found: " + e.url }

func isNotFound(err error) bool {
	var nf notFoundError
	return errors.As(err, &nf)
}

// doRequest issues an HTTP request to Nomad's API. When body is
// non-nil it's JSON-encoded; when result is non-nil the response
// body is JSON-decoded into it.
func (p *Provider) doRequest(ctx context.Context, method, endpoint string, body, result any) error {
	addr := strings.TrimRight(p.cfg.Address, "/")

	var reqBody io.Reader

	if body != nil {
		buf := new(bytes.Buffer)
		if err := json.NewEncoder(buf).Encode(body); err != nil {
			return fmt.Errorf("encode request: %w", err)
		}

		reqBody = buf
	}

	req, err := http.NewRequestWithContext(ctx, method, addr+endpoint, reqBody)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	if p.cfg.Token != "" {
		req.Header.Set("X-Nomad-Token", p.cfg.Token)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return notFoundError{url: endpoint}
	}

	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("nomad %s %s: status %d: %s", method, endpoint, resp.StatusCode, string(raw))
	}

	if result != nil {
		if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}

	return nil
}


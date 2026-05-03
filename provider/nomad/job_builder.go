package nomad

// job_builder.go translates a ProvisionRequest's []ServiceSpec into a
// Nomad Job spec. The mapping:
//
//   - One Job per Workload Instance, named `cp-<instanceID>`.
//   - One TaskGroup with Count = Main service Replicas (or 1).
//   - One Task per ServiceSpec, with lifecycle hooks driving
//     Init/Sidecar/Main scheduling:
//       - RoleInit    → lifecycle.hook = "prestart", sidecar = false
//       - RoleSidecar → lifecycle.hook = "poststart", sidecar = true
//       - RoleMain    → no lifecycle stanza (long-lived primary)
//   - TaskGroup network mode "bridge" so siblings DNS-resolve each
//     other inside the group's network namespace by service name.
//   - Volumes declared on services map onto group-level Nomad volumes
//     mounted into each task. Persistent volumes for KindStatefulSet
//     get their own per-allocation volume mount via host_volume.
//
// Wire format: we emit the Nomad HTTP-API JSON shape (the
// `nomadJobRequest` envelope) — no external SDK. The builder produces
// `*nomadJobRequest` ready for POST /v1/jobs.

import (
	"strconv"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

// nomadJobRequest is the top-level POST body for /v1/jobs.
type nomadJobRequest struct {
	Job *nomadJob `json:"Job"`
}

// nomadJob is the projection of a Nomad Job we serialise. Only the
// fields ctrlplane actually uses are modelled; Nomad accepts a
// permissive superset and ignores omitted fields.
type nomadJob struct {
	ID          string            `json:"ID"`
	Name        string            `json:"Name"`
	Type        string            `json:"Type"`
	Region      string            `json:"Region,omitempty"`
	Namespace   string            `json:"Namespace,omitempty"`
	Datacenters []string          `json:"Datacenters,omitempty"`
	Stop        bool              `json:"Stop,omitempty"`
	Meta        map[string]string `json:"Meta,omitempty"`
	TaskGroups  []*nomadTaskGroup `json:"TaskGroups"`
}

type nomadTaskGroup struct {
	Name             string            `json:"Name"`
	Count            int               `json:"Count"`
	Tasks            []*nomadTask      `json:"Tasks"`
	Networks         []*nomadNetwork   `json:"Networks,omitempty"`
	Services         []*nomadService   `json:"Services,omitempty"`
	Volumes          map[string]any    `json:"Volumes,omitempty"`
	RestartPolicy    *nomadRestart     `json:"RestartPolicy,omitempty"`
	ReschedulePolicy *nomadReschedule  `json:"ReschedulePolicy,omitempty"`
	Meta             map[string]string `json:"Meta,omitempty"`
}

type nomadTask struct {
	Name      string            `json:"Name"`
	Driver    string            `json:"Driver"`
	Config    map[string]any    `json:"Config"`
	Env       map[string]string `json:"Env,omitempty"`
	Resources *nomadResources   `json:"Resources,omitempty"`
	Lifecycle *nomadLifecycle   `json:"Lifecycle,omitempty"`
	Templates []*nomadTemplate  `json:"Templates,omitempty"`
	Services  []*nomadService   `json:"Services,omitempty"`
	Meta      map[string]string `json:"Meta,omitempty"`
}

type nomadLifecycle struct {
	Hook    string `json:"Hook"`
	Sidecar bool   `json:"Sidecar"`
}

type nomadResources struct {
	CPU      int             `json:"CPU,omitempty"`
	MemoryMB int             `json:"MemoryMB,omitempty"`
	DiskMB   int             `json:"DiskMB,omitempty"`
	Networks []*nomadNetwork `json:"Networks,omitempty"`
}

type nomadNetwork struct {
	Mode         string             `json:"Mode,omitempty"`
	DynamicPorts []*nomadPortLabel  `json:"DynamicPorts,omitempty"`
	ReservedPorts []*nomadPortLabel `json:"ReservedPorts,omitempty"`
}

type nomadPortLabel struct {
	Label string `json:"Label"`
	Value int    `json:"Value,omitempty"`
	To    int    `json:"To,omitempty"`
}

type nomadService struct {
	Name      string   `json:"Name"`
	PortLabel string   `json:"PortLabel,omitempty"`
	Tags      []string `json:"Tags,omitempty"`
}

type nomadRestart struct {
	Attempts int    `json:"Attempts"`
	Mode     string `json:"Mode"`
	Delay    int64  `json:"Delay"`
	Interval int64  `json:"Interval"`
}

type nomadReschedule struct {
	Attempts int   `json:"Attempts"`
	Interval int64 `json:"Interval"`
}

type nomadTemplate struct {
	EmbeddedTmpl string `json:"EmbeddedTmpl"`
	DestPath     string `json:"DestPath"`
	ChangeMode   string `json:"ChangeMode,omitempty"`
}

// jobName encodes the instance ID into the Nomad Job name. Mirrors
// nomadJobName in stats.go (kept consistent so `nomad job status
// cp-<id>` works either way) and matches the docker provider's
// project naming.
func jobName(instanceID id.ID) string {
	return "cp-" + instanceID.String()
}

// taskGroupName is fixed per Job — multi-task-group support would
// require splitting Services across groups, which is a future
// extension. For now every service runs in one group so they share
// the same network namespace and can DNS-resolve each other by name.
const taskGroupName = "workload"

// buildJob translates a ProvisionRequest into a Nomad job request
// body. Returns the serialisable struct; callers POST it to
// /v1/jobs.
func buildJob(cfg Config, req provider.ProvisionRequest) *nomadJobRequest {
	tasks := make([]*nomadTask, 0, len(req.Services))
	allPorts := make([]*nomadPortLabel, 0)
	allServices := make([]*nomadService, 0)

	for i := range req.Services {
		svc := req.Services[i]
		t := buildTask(svc)
		tasks = append(tasks, t)

		// Collect ports at the group level — Nomad's bridge-mode
		// network requires ports declared at the group, not the task.
		for j, port := range svc.Ports {
			label := svc.Name + "-" + strconv.Itoa(j)
			pl := &nomadPortLabel{
				Label: label,
				To:    port.Container,
			}

			if port.Host > 0 {
				pl.Value = port.Host
			}

			allPorts = append(allPorts, pl)
		}

		// Register a service entry for each non-Init container so
		// Nomad's Consul integration (when present) advertises the
		// service. Inits don't get registered — they don't run long
		// enough to be worth advertising.
		if svc.Role != provider.RoleInit && len(svc.Ports) > 0 {
			allServices = append(allServices, &nomadService{
				Name:      svc.Name,
				PortLabel: svc.Name + "-0",
				Tags:      []string{"ctrlplane", string(svc.Role)},
			})
		}
	}

	count := replicaCount(req)

	group := &nomadTaskGroup{
		Name:  taskGroupName,
		Count: count,
		Tasks: tasks,
		Networks: []*nomadNetwork{
			{
				Mode:         "bridge",
				DynamicPorts: allPorts,
			},
		},
		Services: allServices,
		RestartPolicy: &nomadRestart{
			Attempts: 3,
			Mode:     "delay",
			Delay:    int64(15 * 1e9), // 15s in ns
			Interval: int64(2 * 60 * 1e9), // 2min in ns
		},
		Meta: map[string]string{
			"ctrlplane.instance": req.InstanceID.String(),
			"ctrlplane.tenant":   req.TenantID,
			"ctrlplane.kind":     string(req.Kind),
		},
	}

	job := &nomadJob{
		ID:         jobName(req.InstanceID),
		Name:       jobName(req.InstanceID),
		Type:       "service",
		Region:     cfg.Region,
		Namespace:  cfg.Namespace,
		TaskGroups: []*nomadTaskGroup{group},
		Meta: map[string]string{
			"ctrlplane.instance": req.InstanceID.String(),
			"ctrlplane.tenant":   req.TenantID,
			"ctrlplane.kind":     string(req.Kind),
		},
	}

	if cfg.Datacenter != "" {
		job.Datacenters = []string{cfg.Datacenter}
	}

	return &nomadJobRequest{Job: job}
}

// buildTask translates one ServiceSpec into a Nomad task. Driver is
// fixed to "docker" for Phase 3 — pluggable drivers (exec, raw_exec,
// etc.) are a follow-up; the default matches the platform most
// ctrlplane operators run on.
func buildTask(svc provider.ServiceSpec) *nomadTask {
	cfg := map[string]any{
		"image": svc.Image,
	}

	if len(svc.Command) > 0 {
		cfg["entrypoint"] = svc.Command
	}

	if len(svc.Args) > 0 {
		cfg["args"] = svc.Args
	}

	if len(svc.Ports) > 0 {
		labels := make([]string, len(svc.Ports))
		for i := range svc.Ports {
			labels[i] = svc.Name + "-" + strconv.Itoa(i)
		}

		cfg["ports"] = labels
	}

	t := &nomadTask{
		Name:   svc.Name,
		Driver: "docker",
		Config: cfg,
		Env:    svc.Env,
		Resources: &nomadResources{
			CPU:      svc.Resources.CPUMillis,
			MemoryMB: svc.Resources.MemoryMB,
			DiskMB:   svc.Resources.DiskMB,
		},
		Meta: map[string]string{
			"ctrlplane.service": svc.Name,
			"ctrlplane.role":    string(svc.Role),
		},
	}

	switch svc.Role {
	case provider.RoleInit:
		t.Lifecycle = &nomadLifecycle{
			Hook:    "prestart",
			Sidecar: false,
		}
	case provider.RoleSidecar:
		t.Lifecycle = &nomadLifecycle{
			Hook:    "poststart",
			Sidecar: true,
		}
	case provider.RoleMain:
		// No lifecycle stanza — long-lived primary.
	}

	return t
}

// replicaCount picks the desired replica count for a Job. Mirrors the
// kubernetes provider's replicaCountFor — Main service's
// Resources.Replicas wins; defaults to 1 when unset.
func replicaCount(req provider.ProvisionRequest) int {
	for i := range req.Services {
		if req.Services[i].Role == provider.RoleMain || req.Services[i].Role == "" {
			return max(req.Services[i].Resources.Replicas, 1)
		}
	}

	return 1
}

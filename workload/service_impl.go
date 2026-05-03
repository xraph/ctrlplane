package workload

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"time"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/health"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/metrics"
	"github.com/xraph/ctrlplane/network"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/template"
)

// service is the concrete Service implementation. Wires Workload
// CRUD into the matching Instance/Deploy services so a Scale call
// (workload-level) cascades into per-replica Provision/Deprovision
// (instance-level).
type service struct {
	store     Store
	instances instance.Service
	deploys   deploy.Service
	templates template.Service
	health    health.Service
	metrics   metrics.Service
	network   network.Service
	events    event.Bus
	auth      auth.Provider
}

// NewService wires the workload service. The instance + deploy +
// health services are mandatory; templates, metrics + network are
// optional (passing nil yields workload-level read methods that
// return empty results for those subsystems, and FromTemplateID is
// rejected). The composition layer is expected to call
// templates.SetWorkloadReader(NewSpecReader(...)) so the template
// service can fork from existing workloads.
func NewService(store Store, instances instance.Service, deploys deploy.Service, templates template.Service, healthSvc health.Service, metricsSvc metrics.Service, networkSvc network.Service, events event.Bus, authProvider auth.Provider) *service { //nolint:revive // unexported return matches deploy.NewService
	return &service{
		store:     store,
		instances: instances,
		deploys:   deploys,
		templates: templates,
		health:    healthSvc,
		metrics:   metricsSvc,
		network:   networkSvc,
		events:    events,
		auth:      authProvider,
	}
}

// Create persists a Workload and provisions Replicas Instance
// replicas. Returns the persisted Workload — caller can ListInstances
// for the full replica set if needed. On partial failure (some
// replicas created, some failed), the Workload is left in
// StateFailed and the caller can retry Scale to reach the desired
// count.
func (s *service) Create(ctx context.Context, req CreateRequest) (*Workload, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("create workload: %w", err)
	}

	if strings.TrimSpace(req.Name) == "" {
		return nil, errors.New("create workload: name is required")
	}

	if !req.FromTemplateID.IsNil() {
		if s.templates == nil {
			return nil, errors.New("create workload: from_template_id requires a template service")
		}

		tmpl, terr := s.templates.Get(ctx, req.FromTemplateID)
		if terr != nil {
			return nil, fmt.Errorf("create workload: load template %s: %w", req.FromTemplateID, terr)
		}

		req = applyTemplateDefaults(req, tmpl)
	}

	if err := validateServices(req.Services); err != nil {
		return nil, fmt.Errorf("create workload: %w", err)
	}

	kind := req.Kind
	if kind == "" {
		kind = provider.KindDeployment
	}

	replicas := req.Replicas
	if replicas <= 0 {
		replicas = 1
	}

	w := NewWorkload()
	w.TenantID = claims.TenantID
	w.Name = req.Name
	w.Slug = slugify(req.Name)
	w.DatacenterID = req.DatacenterID
	w.Region = req.Region
	w.ProviderName = req.ProviderName
	w.TemplateID = req.FromTemplateID
	w.Kind = kind
	w.Services = req.Services
	w.Labels = req.Labels
	w.ReplicaCount = replicas
	w.State = StateProvisioning

	if err := s.store.InsertWorkload(ctx, w); err != nil {
		return nil, fmt.Errorf("create workload: insert: %w", err)
	}

	// Provision replicas. On any failure we mark the Workload as
	// failed but keep the Instances that did come up — operators can
	// inspect, then either retry Scale or Delete to clean up.
	for i := 0; i < replicas; i++ {
		if _, err := s.spawnReplica(ctx, w, i); err != nil {
			w.State = StateFailed
			_ = s.store.UpdateWorkload(ctx, w)

			return nil, fmt.Errorf("create workload: spawn replica %d: %w", i, err)
		}
	}

	w.State = StateActive
	if err := s.store.UpdateWorkload(ctx, w); err != nil {
		return nil, fmt.Errorf("create workload: update state: %w", err)
	}

	_ = s.events.Publish(ctx, event.NewEvent(event.WorkloadCreated, claims.TenantID).
		WithActor(claims.SubjectID).
		WithWorkload(w.ID).
		WithPayload(map[string]any{
			"workload_id":   w.ID.String(),
			"replicas":      replicas,
			"service_count": len(w.Services),
			"kind":          string(w.Kind),
			"template_id":   w.TemplateID.String(),
		}))

	return w, nil
}

// validateServices enforces multi-service invariants on a request.
// Mirrors template.validateServices but lives here to avoid an import
// cycle (template → workload would not be allowed).
func validateServices(services []provider.ServiceSpec) error {
	if len(services) == 0 {
		return errors.New("services: at least one service is required")
	}

	names := make(map[string]struct{}, len(services))
	mainCount := 0

	for i := range services {
		svc := &services[i]

		if strings.TrimSpace(svc.Name) == "" {
			return fmt.Errorf("services[%d]: name is required", i)
		}

		if strings.TrimSpace(svc.Image) == "" {
			return fmt.Errorf("services[%d] (%s): image is required", i, svc.Name)
		}

		if _, dup := names[svc.Name]; dup {
			return fmt.Errorf("services[%d]: duplicate service name %q", i, svc.Name)
		}

		names[svc.Name] = struct{}{}

		if svc.Role == "" {
			svc.Role = provider.RoleMain
		}

		if svc.Role == provider.RoleMain {
			mainCount++
		}
	}

	if mainCount != 1 {
		return fmt.Errorf("services: exactly one Main service required, found %d", mainCount)
	}

	for i := range services {
		svc := &services[i]
		for _, dep := range svc.DependsOn {
			if _, ok := names[dep]; !ok {
				return fmt.Errorf("services[%d] (%s): depends_on references unknown service %q", i, svc.Name, dep)
			}
		}
	}

	return nil
}

// Get returns a Workload by ID, scoped to the caller's tenant.
func (s *service) Get(ctx context.Context, workloadID id.ID) (*Workload, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("get workload: %w", err)
	}

	w, err := s.store.GetWorkloadByID(ctx, claims.TenantID, workloadID)
	if err != nil {
		return nil, fmt.Errorf("get workload: %w", err)
	}

	return w, nil
}

// GetBySlug looks up a workload by URL-safe slug within the
// caller's tenant.
func (s *service) GetBySlug(ctx context.Context, slug string) (*Workload, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("get workload by slug: %w", err)
	}

	w, err := s.store.GetWorkloadBySlug(ctx, claims.TenantID, slug)
	if err != nil {
		return nil, fmt.Errorf("get workload by slug: %w", err)
	}

	return w, nil
}

// List returns all workloads visible to the caller. Empty
// claims.TenantID is the cross-tenant convention used by admin
// dashboards (matches the instance/deploy stores).
func (s *service) List(ctx context.Context, opts ListOptions) (*ListResult, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("list workloads: %w", err)
	}

	res, err := s.store.ListWorkloads(ctx, claims.TenantID, opts)
	if err != nil {
		return nil, fmt.Errorf("list workloads: %w", err)
	}

	return res, nil
}

// Update mutates the Workload spec. Doesn't touch running replicas
// — for that, follow up with Deploy. Image changes flow through
// Deploy, not Update, so callers that want to roll out a new image
// build a DeployRequest with the changed Services.
func (s *service) Update(ctx context.Context, workloadID id.ID, req UpdateRequest) (*Workload, error) {
	w, err := s.Get(ctx, workloadID)
	if err != nil {
		return nil, err
	}

	if req.Name != nil && strings.TrimSpace(*req.Name) != "" {
		w.Name = *req.Name
	}

	if req.Services != nil {
		if vErr := validateServices(req.Services); vErr != nil {
			return nil, fmt.Errorf("update workload: %w", vErr)
		}

		w.Services = req.Services
	}

	if req.Labels != nil {
		w.Labels = req.Labels
	}

	w.UpdatedAt = time.Now().UTC()

	if err := s.store.UpdateWorkload(ctx, w); err != nil {
		return nil, fmt.Errorf("update workload: %w", err)
	}

	claims, _ := auth.RequireClaims(ctx)

	subjectID := ""
	if claims != nil {
		subjectID = claims.SubjectID
	}

	_ = s.events.Publish(ctx, event.NewEvent(event.WorkloadUpdated, w.TenantID).
		WithActor(subjectID).
		WithWorkload(w.ID).
		WithPayload(map[string]any{
			"workload_id":   w.ID.String(),
			"service_count": len(w.Services),
		}))

	return w, nil
}

// Scale grows or shrinks the replica set to match `replicas`.
// Growing spawns new Instances at the next ReplicaIndex slots.
// Shrinking deprovisions the highest-indexed Instances first
// (LIFO — newest replicas removed first, oldest preserved).
func (s *service) Scale(ctx context.Context, workloadID id.ID, replicas int) (*Workload, error) {
	if replicas < 0 {
		return nil, errors.New("scale workload: replicas must be >= 0")
	}

	w, err := s.Get(ctx, workloadID)
	if err != nil {
		return nil, err
	}

	if err := ValidateTransition(w.State, StateScaling); err != nil {
		return nil, fmt.Errorf("scale workload: %w", err)
	}

	prevState := w.State

	w.State = StateScaling
	if err := s.store.UpdateWorkload(ctx, w); err != nil {
		return nil, fmt.Errorf("scale workload: state update: %w", err)
	}

	current, err := s.ListInstances(ctx, workloadID)
	if err != nil {
		return nil, err
	}

	switch {
	case replicas > len(current):
		// Grow — spawn new replicas at the trailing indices. We
		// pick indices based on the highest existing ReplicaIndex
		// rather than the count, so a workload that grew from
		// 3 → 1 → 3 doesn't reuse old container names that may
		// still be in docker's removal queue.
		nextIdx := nextReplicaIndex(current)

		need := replicas - len(current)
		for i := range need {
			if _, err := s.spawnReplica(ctx, w, nextIdx+i); err != nil {
				w.State = StateFailed
				_ = s.store.UpdateWorkload(ctx, w)

				return nil, fmt.Errorf("scale workload: spawn replica %d: %w", nextIdx+i, err)
			}
		}
	case replicas < len(current):
		// Shrink — deprovision highest-indexed replicas first.
		toRemove := len(current) - replicas
		// current is sorted by ReplicaIndex ascending; remove tail.
		victims := current[len(current)-toRemove:]
		for _, v := range victims {
			if err := s.instances.Delete(ctx, v.ID); err != nil {
				w.State = StateFailed
				_ = s.store.UpdateWorkload(ctx, w)

				return nil, fmt.Errorf("scale workload: delete replica %s: %w", v.ID, err)
			}
		}
	}

	w.ReplicaCount = replicas

	switch replicas {
	case 0:
		w.State = StatePaused
	default:
		// Return to whichever resting state we came from when the
		// scale was a no-op, otherwise back to Active.
		if prevState == StatePaused {
			w.State = StateActive
		} else {
			w.State = StateActive
		}
	}

	if err := s.store.UpdateWorkload(ctx, w); err != nil {
		return nil, fmt.Errorf("scale workload: final update: %w", err)
	}

	claims, _ := auth.RequireClaims(ctx)

	subjectID := ""
	if claims != nil {
		subjectID = claims.SubjectID
	}

	evtType := event.WorkloadScaled
	if replicas == 0 {
		evtType = event.WorkloadPaused
	} else if prevState == StatePaused {
		evtType = event.WorkloadResumed
	}

	_ = s.events.Publish(ctx, event.NewEvent(evtType, w.TenantID).
		WithActor(subjectID).
		WithWorkload(w.ID).
		WithPayload(map[string]any{
			"workload_id": w.ID.String(),
			"replicas":    replicas,
		}))

	return w, nil
}

// Pause scales to zero, retaining the spec and the prior replica
// count so a later Resume restores it. We stamp PreviousReplicas
// before delegating to Scale because Scale itself zeros the count
// on the way through StatePaused.
//
// A no-op when the workload is already paused (ReplicaCount=0):
// PreviousReplicas would otherwise get clobbered with 0 and a
// future Resume would lose the original intent.
func (s *service) Pause(ctx context.Context, workloadID id.ID) error {
	w, err := s.Get(ctx, workloadID)
	if err != nil {
		return err
	}

	if w.ReplicaCount > 0 {
		w.PreviousReplicas = w.ReplicaCount
		if err := s.store.UpdateWorkload(ctx, w); err != nil {
			return fmt.Errorf("pause workload: stamp previous replicas: %w", err)
		}
	}

	_, err = s.Scale(ctx, workloadID, 0)

	return err
}

// Resume scales back to the replica count saved by Pause. Falls
// back to the current ReplicaCount (handles workloads that were
// never paused or had Pause stamp zero), then to 1.
func (s *service) Resume(ctx context.Context, workloadID id.ID) error {
	w, err := s.Get(ctx, workloadID)
	if err != nil {
		return err
	}

	target := w.PreviousReplicas
	if target == 0 {
		target = w.ReplicaCount
	}

	if target == 0 {
		target = 1
	}

	_, err = s.Scale(ctx, workloadID, target)

	return err
}

// Restart performs an in-place restart of every replica. Each
// replica's container is stopped + started again by the underlying
// provider (docker ContainerRestart, k8s Pod restart) — no
// deprovision, no replica-count change, no new container IDs.
//
// Fail-fast on the first replica error so the user sees a clear
// failure rather than a half-restarted workload — the workload
// stays in StateActive throughout, so the caller can retry.
func (s *service) Restart(ctx context.Context, workloadID id.ID) error {
	w, err := s.Get(ctx, workloadID)
	if err != nil {
		return err
	}

	replicas, err := s.ListInstances(ctx, workloadID)
	if err != nil {
		return fmt.Errorf("restart workload: list replicas: %w", err)
	}

	for _, r := range replicas {
		if err := s.instances.Restart(ctx, r.ID); err != nil {
			return fmt.Errorf("restart workload: restart replica %s: %w", r.ID, err)
		}
	}

	claims, _ := auth.RequireClaims(ctx)

	subjectID := ""
	if claims != nil {
		subjectID = claims.SubjectID
	}

	_ = s.events.Publish(ctx, event.NewEvent(event.WorkloadRestarted, w.TenantID).
		WithActor(subjectID).
		WithWorkload(w.ID).
		WithPayload(map[string]any{
			"workload_id":   w.ID.String(),
			"replica_count": len(replicas),
		}))

	return nil
}

// Deploy rolls out a new Release. req.Services lists only services
// being changed in this rollout — services not listed inherit their
// snapshot from the prior Release (Releases stay self-contained).
//
// Workload.Services is updated in-place to reflect the new images on
// the targeted services so subsequent reads (and any newly-spawned
// replicas) see the post-deploy spec.
func (s *service) Deploy(ctx context.Context, workloadID id.ID, req DeployRequest) (*deploy.Deployment, error) {
	if len(req.Services) == 0 {
		return nil, errors.New("deploy workload: at least one service required")
	}

	w, err := s.Get(ctx, workloadID)
	if err != nil {
		return nil, err
	}

	if err := ValidateTransition(w.State, StateDeploying); err != nil {
		return nil, fmt.Errorf("deploy workload: %w", err)
	}

	// Apply per-service image/env overrides to the workload's own spec.
	// Services not listed in req keep their current spec.
	known := make(map[string]int, len(w.Services))
	for i := range w.Services {
		known[w.Services[i].Name] = i
	}

	for _, sd := range req.Services {
		idx, ok := known[sd.Name]
		if !ok {
			return nil, fmt.Errorf("deploy workload: unknown service %q", sd.Name)
		}

		w.Services[idx].Image = sd.Image
		if sd.Env != nil {
			w.Services[idx].Env = sd.Env
		}

		if sd.HealthCheck != nil {
			w.Services[idx].HealthCheck = sd.HealthCheck
		}
	}

	w.State = StateDeploying
	if err := s.store.UpdateWorkload(ctx, w); err != nil {
		return nil, fmt.Errorf("deploy workload: state update: %w", err)
	}

	replicas, err := s.ListInstances(ctx, workloadID)
	if err != nil {
		return nil, err
	}

	if len(replicas) == 0 {
		w.State = StateActive
		_ = s.store.UpdateWorkload(ctx, w)

		return nil, errors.New("deploy workload: no replicas to update")
	}

	// Trigger one Deploy per replica with the partial Services slice.
	// Return the first deployment as the "primary" record callers poll.
	var first *deploy.Deployment

	for _, r := range replicas {
		dep, derr := s.deploys.Deploy(ctx, deploy.DeployRequest{
			InstanceID: r.ID,
			Services:   req.Services,
			Strategy:   req.Strategy,
			Notes:      req.Notes,
		})
		if derr != nil {
			w.State = StateFailed
			_ = s.store.UpdateWorkload(ctx, w)

			return nil, fmt.Errorf("deploy workload: deploy replica %s: %w", r.ID, derr)
		}

		if first == nil {
			first = dep
		}
	}

	w.State = StateActive
	if first != nil {
		w.CurrentReleaseID = first.ReleaseID
	}

	if err := s.store.UpdateWorkload(ctx, w); err != nil {
		return nil, fmt.Errorf("deploy workload: final update: %w", err)
	}

	claims, _ := auth.RequireClaims(ctx)

	subjectID := ""
	if claims != nil {
		subjectID = claims.SubjectID
	}

	releaseID := ""
	if first != nil {
		releaseID = first.ReleaseID.String()
	}

	deployedNames := make([]string, len(req.Services))
	for i := range req.Services {
		deployedNames[i] = req.Services[i].Name
	}

	_ = s.events.Publish(ctx, event.NewEvent(event.WorkloadDeployed, w.TenantID).
		WithActor(subjectID).
		WithWorkload(w.ID).
		WithPayload(map[string]any{
			"workload_id":       w.ID.String(),
			"services_deployed": deployedNames,
			"strategy":          req.Strategy,
			"release_id":        releaseID,
			"replicas":          len(replicas),
		}))

	return first, nil
}

// Delete tears down every replica and removes the Workload record.
// Fail-loud per-replica: if any replica delete fails (provider
// unreachable, container stuck, etc.) the workload row is LEFT
// IN PLACE in StateDestroying, the failed replica IDs are wrapped
// into the returned error, and replica_delete_failed events are
// emitted so observers can act on it. This makes Delete safely
// retryable — re-running it picks up the still-existing replicas
// and tries again.
//
// History: an earlier version was best-effort and proceeded to
// DeleteWorkload even when replicas failed. That left orphan
// Instance rows + actual containers alive, with the parent
// Workload row gone — there was no handle to clean them up by.
// Don't reintroduce the swallow.
func (s *service) Delete(ctx context.Context, workloadID id.ID) error {
	w, err := s.Get(ctx, workloadID)
	if err != nil {
		return err
	}

	w.State = StateDestroying
	_ = s.store.UpdateWorkload(ctx, w)

	replicas, err := s.ListInstances(ctx, workloadID)
	if err != nil {
		return fmt.Errorf("delete workload: list replicas: %w", err)
	}

	claims, _ := auth.RequireClaims(ctx)

	subjectID := ""
	if claims != nil {
		subjectID = claims.SubjectID
	}

	var failed []string

	for _, r := range replicas {
		if delErr := s.instances.Delete(ctx, r.ID); delErr != nil {
			_ = s.events.Publish(ctx, event.NewEvent(event.WorkloadFailed, w.TenantID).
				WithActor(subjectID).
				WithWorkload(w.ID).
				WithPayload(map[string]any{
					"workload_id": w.ID.String(),
					"instance_id": r.ID.String(),
					"reason":      "replica_delete_failed",
					"error":       delErr.Error(),
				}))
			failed = append(failed, fmt.Sprintf("%s: %v", r.ID, delErr))
		}
	}

	if len(failed) > 0 {
		// Mark the workload as Failed so a follow-up retry sees a
		// consistent state. Leave the row in place — the caller
		// can re-run Delete to clean up the still-existing
		// replicas.
		w.State = StateFailed
		_ = s.store.UpdateWorkload(ctx, w)

		return fmt.Errorf("delete workload %s: %d replica(s) failed: %s",
			workloadID, len(failed), strings.Join(failed, "; "))
	}

	if err := s.store.DeleteWorkload(ctx, w.TenantID, w.ID); err != nil {
		return fmt.Errorf("delete workload: %w", err)
	}

	_ = s.events.Publish(ctx, event.NewEvent(event.WorkloadDeleted, w.TenantID).
		WithActor(subjectID).
		WithWorkload(w.ID).
		WithPayload(map[string]any{
			"workload_id":   w.ID.String(),
			"replica_count": len(replicas),
		}))

	return nil
}

// ListInstances returns the workload's replicas ordered by
// ReplicaIndex ascending.
//
// Belt-and-braces filtering: we ask the store via the
// `ctrlplane.workload=<id>` label, but every store backend
// silently ignored ListOptions.Label until recently — and the
// stub backends (pg/sqlite/badger) still do. Without the local
// post-filter, this returns every Active instance in the tenant
// and workload.GetHealth ends up reporting the entire tenant as
// each component's "replicas". The post-filter guarantees
// correctness regardless of backend; mongo + memory layer their
// own filter on top so we don't ship N tenant rows just to drop
// most of them locally.
func (s *service) ListInstances(ctx context.Context, workloadID id.ID) ([]*instance.Instance, error) {
	res, err := s.instances.List(ctx, instance.ListOptions{
		Label: "ctrlplane.workload=" + workloadID.String(),
		Limit: 1000,
	})
	if err != nil {
		return nil, fmt.Errorf("list instances for workload %s: %w", workloadID, err)
	}

	expected := workloadID.String()

	out := make([]*instance.Instance, 0, len(res.Items))
	for _, inst := range res.Items {
		if inst != nil && inst.Labels["ctrlplane.workload"] == expected {
			out = append(out, inst)
		}
	}

	sortByReplicaIndex(out)

	return out, nil
}

// --- internals ---

// spawnReplica creates one Instance for the workload at the given
// replica index. The label `ctrlplane.workload=<wid>` and
// `ctrlplane.replica_index=<idx>` are stamped so the instance can
// be found by ListInstances.
//
// Idempotent on the (tenant, slug) namespace: if an instance with
// the would-be slug already exists (e.g. left over from a half-
// failed prior scale where Provision errored after the row was
// inserted, or — pre this commit — the mongo store dropped the
// workload label so the row was invisible to ListInstances), we
// reuse it instead of erroring on the unique-index collision. If
// the existing instance belongs to a different workload (label
// mismatch) we surface a clear error rather than overwriting it.
//
// The reuse path skips a fresh Provision call — the underlying
// container may or may not exist, depending on what happened on
// the prior attempt. Callers that want strictly fresh state should
// Delete the workload's replicas first; for the common
// retry-after-failed-DC-pick path (the case this guards), reusing
// the row and letting subsequent Restart/health checks reconcile
// the live container is the right behaviour.
func (s *service) spawnReplica(ctx context.Context, w *Workload, replicaIndex int) (*instance.Instance, error) {
	labels := make(map[string]string, len(w.Labels)+2)
	maps.Copy(labels, w.Labels)

	labels["ctrlplane.workload"] = w.ID.String()
	labels["ctrlplane.replica_index"] = strconv.Itoa(replicaIndex)

	name := fmt.Sprintf("%s-%d", w.Slug, replicaIndex)
	slug := slugify(name)

	if existing, err := s.instances.GetBySlug(ctx, slug); err == nil && existing != nil {
		ownerID := existing.Labels["ctrlplane.workload"]
		// Empty ownerID covers legacy rows from before labels were
		// persisted — those are ours by construction (no other
		// workload could have produced this slug under our tenant)
		// so we adopt them.
		if ownerID == "" || ownerID == w.ID.String() {
			return existing, nil
		}

		return nil, fmt.Errorf("spawn replica %d: slug %q already owned by workload %s",
			replicaIndex, slug, ownerID)
	} else if err != nil && !errors.Is(err, ctrlplane.ErrNotFound) {
		return nil, fmt.Errorf("spawn replica %d: dedupe lookup: %w", replicaIndex, err)
	}

	inst, err := s.instances.Create(ctx, instance.CreateRequest{
		Name:         name,
		DatacenterID: w.DatacenterID,
		ProviderName: w.ProviderName,
		Region:       w.Region,
		Kind:         w.Kind,
		Services:     w.Services,
		Labels:       labels,
	})
	if err != nil {
		return nil, err
	}

	// Record the v1 Release + a synthetic completed Deployment for
	// the freshly-provisioned replica. Without this the dashboard's
	// Deployments page stays empty after Create, rollback has no v1
	// target, and partial deploys silently drop services not in the
	// request because there's no prior Release to inherit from.
	//
	// Idempotent: RecordInitial is a no-op when a Release already
	// exists, which covers spawnReplica's adoption path (existing
	// instance with our slug + workload label) safely.
	//
	// Best-effort: a failed RecordInitial does not roll back the
	// just-provisioned container. The cron's reconciler is the
	// long-term backstop; for now an operator can re-trigger by
	// calling Deploy explicitly. Surfacing the error here would
	// turn a benign "release row missing" into a hard Create
	// failure that leaks the running instance.
	if s.deploys != nil {
		if _, recErr := s.deploys.RecordInitial(ctx, inst.ID); recErr != nil {
			_ = s.events.Publish(ctx, event.NewEvent(event.WorkloadFailed, w.TenantID).
				WithWorkload(w.ID).
				WithInstance(inst.ID).
				WithPayload(map[string]any{
					"reason": "initial_release_record_failed",
					"error":  recErr.Error(),
				}))
		}
	}

	return inst, nil
}

// nextReplicaIndex returns one past the highest existing ReplicaIndex
// in the slice. Used to allocate unique slots when scaling up after
// a previous scale-down.
func nextReplicaIndex(replicas []*instance.Instance) int {
	max := -1

	for _, r := range replicas {
		idx := readReplicaIndex(r)
		if idx > max {
			max = idx
		}
	}

	return max + 1
}

// readReplicaIndex pulls the replica index off the label, defaulting
// to 0 when missing (defensive — every replica spawned via
// spawnReplica gets the label).
func readReplicaIndex(inst *instance.Instance) int {
	if inst == nil {
		return 0
	}

	raw, ok := inst.Labels["ctrlplane.replica_index"]
	if !ok {
		return 0
	}

	var n int

	_, _ = fmt.Sscanf(raw, "%d", &n)

	return n
}

func sortByReplicaIndex(items []*instance.Instance) {
	// Bubble sort is fine — N is small (replicas per workload, single digits)
	// and avoids pulling in sort.Slice + a closure.
	for i := range items {
		for j := i + 1; j < len(items); j++ {
			if readReplicaIndex(items[i]) > readReplicaIndex(items[j]) {
				items[i], items[j] = items[j], items[i]
			}
		}
	}
}

// slugify produces a URL-safe slug from a name. Mirrors the helper
// in datacenter/service_impl.go to keep the package self-contained.
func slugify(name string) string {
	out := strings.ToLower(name)
	out = strings.ReplaceAll(out, " ", "-")
	out = strings.ReplaceAll(out, "_", "-")

	return out
}

// Compile-time assertion the service satisfies the interface.
var _ Service = (*service)(nil)

// Compile-time hint that ctrlplane sentinels are exercised — keeps
// goimports from removing the import if the compile-time error
// paths get refactored away.
var _ = ctrlplane.ErrNotFound

// Compile-time hint to keep provider import live; ResourceSpec is
// used in Workload but goimports may mark it indirect.
var _ provider.ResourceSpec

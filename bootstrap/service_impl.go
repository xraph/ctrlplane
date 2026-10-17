package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"

	gometrics "github.com/xraph/go-utils/metrics"
	"go.opentelemetry.io/otel/trace"

	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/internal/obs"
	"github.com/xraph/ctrlplane/provider"
)

// service is the concrete bootstrap.Service. The reconciler worker is
// the only caller; tests construct it directly with fakes.
type service struct {
	store     Store
	providers *provider.Registry
	hooks     *Registry
	events    event.Bus
	metrics   *obs.Metrics
}

// NewService wires a bootstrap service. All four dependencies are
// required:
//
//   - store: persists BootstrapWorkload rows (memory-backed in Phase
//     1; persistent backends land in Phase 2).
//   - providers: dispatch target for Provision / Deprovision.
//   - hooks: programmatic-contribution path; Hooks().Services(...)
//     called every reconcile tick.
//   - events: reconcile lifecycle events surface here for the audit
//     log + dashboard. Reuses event.DeployStarted / DeploySucceeded /
//     DeployFailed with payload.kind = "bootstrap".
//
//nolint:revive // unexported return matches workload + deploy NewService.
func NewService(store Store, providers *provider.Registry, hooks *Registry, events event.Bus) *service {
	if hooks == nil {
		hooks = NewRegistry()
	}

	return &service{
		store:     store,
		providers: providers,
		hooks:     hooks,
		events:    events,
	}
}

// SetMetrics counts datacenter reconciles and bootstrap provisions and
// retirements, and times their provider calls, on m. A nil m records
// nothing. In a Forge app the ctrlplane extension passes app.Metrics() in
// for you.
func (s *service) SetMetrics(m gometrics.Metrics) {
	s.metrics = obs.NewMetrics(m)
}

// reconcileTally counts what one Reconcile did, for its span.
type reconcileTally struct {
	provisioned, retired, failed int
}

// Reconcile is the single entry point for driving a datacenter's
// bootstrap state forward. The flow:
//
//  1. Build the desired set: union(declared, hook contributions),
//     deduped by Spec.Name. Declarative wins on Name conflict — operator
//     intent always overrides hook defaults.
//  2. Load the current rows for this datacenter.
//  3. Diff the two sets:
//     - desired-only entries → insert pending row, then Provision.
//     - current-only entries → mark retiring, Deprovision, delete row.
//     - intersection where the spec body changed → tear down the old
//     row and re-insert. Phase 1 chooses tear-down over in-place
//     update because the multi-service Deploy path is tenant-scoped
//     today.
//  4. Drive each Pending / Failed row through Provision; record state.
//
// Errors from individual specs are logged + emitted as events but do
// not abort the rest of the reconcile — one broken hook or one stuck
// provider call cannot blind-spot the rest of the datacenter.
func (s *service) Reconcile(ctx context.Context, dc DatacenterInfo, declared []BootstrapServiceSpec) error {
	ctx, span := obs.StartChild(ctx, "reconcile datacenter",
		trace.WithAttributes(obs.AttrDatacenterID.String(dc.ID.String())))
	defer span.End()

	var tally reconcileTally

	err := s.reconcile(ctx, dc, declared, &tally)

	span.SetAttributes(
		obs.AttrBootstrapProvisioned.Int(tally.provisioned),
		obs.AttrBootstrapRetired.Int(tally.retired),
		obs.AttrBootstrapFailed.Int(tally.failed),
	)
	obs.Fail(span, err)
	s.metrics.DatacenterReconciled(err)

	return err
}

func (s *service) reconcile(ctx context.Context, dc DatacenterInfo, declared []BootstrapServiceSpec, tally *reconcileTally) error {
	desired, err := s.buildDesired(ctx, dc, declared)
	if err != nil {
		return fmt.Errorf("bootstrap reconcile: build desired: %w", err)
	}

	current, err := s.store.ListBootstraps(ctx, dc.ID)
	if err != nil {
		return fmt.Errorf("bootstrap reconcile: list current: %w", err)
	}

	currentByName := make(map[string]*BootstrapWorkload, len(current))
	for _, bw := range current {
		currentByName[bw.Name] = bw
	}

	desiredByName := make(map[string]BootstrapServiceSpec, len(desired))
	for _, spec := range desired {
		desiredByName[spec.Name] = spec
	}

	trace.SpanFromContext(ctx).SetAttributes(obs.AttrBootstrapDesired.Int(len(desiredByName)))

	pctx, prov, err := s.providers.Acquire(ctx, dc.ProviderName)
	if err != nil {
		return fmt.Errorf("bootstrap reconcile: provider %s: %w", dc.ProviderName, err)
	}

	// Pass 1 — retire current rows whose desired entry vanished or
	// whose body changed. Body-changed retirements re-create on Pass
	// 2; missing-entry retirements leave the desired row deleted.
	for name, bw := range currentByName {
		desiredSpec, stillDesired := desiredByName[name]
		if stillDesired && !specChanged(bw, desiredSpec) {
			continue
		}

		if s.retire(pctx, prov, bw) {
			tally.retired++
		} else {
			tally.failed++
		}
	}

	// Pass 2 — install desired entries that don't have a matching
	// current row (either never existed or were just retired due to
	// spec drift).
	for name, spec := range desiredByName {
		bw, exists := currentByName[name]
		if exists && bw.State != StateRetired && !specChanged(bw, spec) {
			// Already installed at the right body — only retry if
			// pending / failed.
			if bw.State == StatePending || bw.State == StateFailed {
				tally.count(s.provision(pctx, prov, dc, bw))
			}

			continue
		}

		fresh := newRowFromSpec(dc, spec)
		if err := s.store.InsertBootstrap(ctx, fresh); err != nil {
			s.emitFailed(ctx, dc, fresh, fmt.Sprintf("insert row: %v", err))

			tally.failed++

			continue
		}

		tally.count(s.provision(pctx, prov, dc, fresh))
	}

	return nil
}

// count tallies one provision attempt.
func (t *reconcileTally) count(ok bool) {
	if ok {
		t.provisioned++
	} else {
		t.failed++
	}
}

// buildDesired unions declarative + hook-contributed specs.
// Declared wins on Name conflict; subsequent hooks contributing the
// same Name as an earlier hook also lose to the earlier one (registry
// iteration order is stable per Hooks() snapshot).
func (s *service) buildDesired(ctx context.Context, dc DatacenterInfo, declared []BootstrapServiceSpec) ([]BootstrapServiceSpec, error) {
	out := make([]BootstrapServiceSpec, 0, len(declared))
	seen := make(map[string]struct{}, len(declared))

	for _, spec := range declared {
		if spec.Name == "" {
			continue
		}

		if _, dup := seen[spec.Name]; dup {
			continue
		}

		seen[spec.Name] = struct{}{}
		out = append(out, spec)
	}

	for _, h := range s.hooks.Hooks() {
		contributed, hookErr := s.runHook(ctx, h, dc)
		if hookErr != nil {
			// A broken hook is a runtime issue, not a fatal one —
			// log + emit a payload-tagged event so operators see
			// it; continue with the remaining hooks.
			s.emitHookFailure(ctx, dc, h.Name(), hookErr)

			continue
		}

		for _, spec := range contributed {
			if spec.Name == "" {
				continue
			}

			if _, dup := seen[spec.Name]; dup {
				continue
			}

			seen[spec.Name] = struct{}{}
			out = append(out, spec)
		}
	}

	return out, nil
}

// runHook asks one hook for its contributions. A hook is foreign code that
// may call out to other systems, so it gets a span named after the hook.
func (s *service) runHook(ctx context.Context, h Hook, dc DatacenterInfo) ([]BootstrapServiceSpec, error) {
	ctx, span := obs.StartChild(ctx, "run bootstrap hook",
		trace.WithAttributes(obs.AttrBootstrapHook.String(h.Name())))
	defer span.End()

	contributed, err := h.Services(ctx, dc)
	obs.Fail(span, err)

	return contributed, err
}

// provision drives a row through Pending/Failed → Provisioning → Running.
// On error, the row stays in StateFailed with Attempts incremented;
// the next tick retries. Persistence is best-effort within the
// reconcile call — a failed Update does not abort the rest of the
// reconcile, since the next tick will load the stale row and reach
// the same conclusion anyway.
//
// It reports whether the provider call succeeded.
func (s *service) provision(ctx context.Context, prov provider.Provider, dc DatacenterInfo, bw *BootstrapWorkload) bool {
	ctx, span := obs.StartChild(ctx, "provision bootstrap workload",
		trace.WithAttributes(obs.AttrBootstrapID.String(bw.ID.String())))
	defer span.End()

	bw.State = StateProvisioning
	bw.LastError = ""

	if err := s.store.UpdateBootstrap(ctx, bw); err != nil {
		// Roll the in-memory state back so the caller's view
		// matches what's persisted.
		bw.State = StatePending

		obs.Fail(span, err)
		s.metrics.BootstrapAction(obs.ActionProvision, err)

		return false
	}

	pctx, call := s.metrics.StartProviderCall(ctx, provider.TypeName(prov), obs.OpProvision,
		obs.AttrInstanceID.String(bw.ID.String()))
	res, err := prov.Provision(pctx, provider.ProvisionRequest{
		InstanceID: bw.ID,
		TenantID:   "", // platform-owned; no tenant
		Name:       bw.Name,
		Kind:       bw.Kind,
		Services:   bw.Services,
		Labels:     bw.Labels,
	})
	call.End(err)

	obs.Fail(span, err)
	s.metrics.BootstrapAction(obs.ActionProvision, err)

	if err != nil {
		bw.State = StateFailed
		bw.LastError = err.Error()
		bw.Attempts++

		_ = s.store.UpdateBootstrap(ctx, bw)
		s.emitFailed(ctx, dc, bw, err.Error())

		return false
	}

	bw.State = StateRunning
	bw.ProviderRef = res.ProviderRef
	bw.ServiceRefs = res.ServiceRefs
	bw.LastError = ""

	_ = s.store.UpdateBootstrap(ctx, bw)
	s.emitRunning(ctx, dc, bw)

	return true
}

// retire walks a row through Running → Retiring → Retired then
// deletes it. Best-effort on Deprovision: providers treat
// "already gone" as success (the convergent-Deprovision contract
// shipped earlier), so a successful return safely removes the row.
// On error the row stays in Retiring; next tick retries.
//
// It reports whether the provider call succeeded.
func (s *service) retire(ctx context.Context, prov provider.Provider, bw *BootstrapWorkload) bool {
	ctx, span := obs.StartChild(ctx, "retire bootstrap workload",
		trace.WithAttributes(obs.AttrBootstrapID.String(bw.ID.String())))
	defer span.End()

	bw.State = StateRetiring
	_ = s.store.UpdateBootstrap(ctx, bw)

	pctx, call := s.metrics.StartProviderCall(ctx, provider.TypeName(prov), obs.OpDeprovision,
		obs.AttrInstanceID.String(bw.ID.String()))
	err := prov.Deprovision(pctx, bw.ID)
	call.End(err)

	obs.Fail(span, err)
	s.metrics.BootstrapAction(obs.ActionRetire, err)

	if err != nil {
		bw.LastError = err.Error()
		bw.Attempts++
		_ = s.store.UpdateBootstrap(ctx, bw)

		return false
	}

	bw.State = StateRetired
	_ = s.store.UpdateBootstrap(ctx, bw)
	_ = s.store.DeleteBootstrap(ctx, bw.ID)

	return true
}

// ListByDatacenter returns the bootstrap workloads attached to a
// datacenter. Read-only, no auth gate at the service layer (the
// dashboard panel handler that adds in Phase 3 will gate on
// system:admin claims).
func (s *service) ListByDatacenter(ctx context.Context, datacenterID id.ID) ([]*BootstrapWorkload, error) {
	res, err := s.store.ListBootstraps(ctx, datacenterID)
	if err != nil {
		return nil, fmt.Errorf("bootstrap list: %w", err)
	}

	return res, nil
}

// Get returns a single bootstrap workload by ID.
func (s *service) Get(ctx context.Context, bootstrapID id.ID) (*BootstrapWorkload, error) {
	bw, err := s.store.GetBootstrap(ctx, bootstrapID)
	if err != nil {
		return nil, fmt.Errorf("bootstrap get: %w", err)
	}

	return bw, nil
}

// --- helpers ---

// newRowFromSpec mints a BootstrapWorkload row from a spec. Stamps
// system labels so the GC worker's label-based exclusion ignores it
// even though the row carries the workload-style `ctrlplane.system`
// marker.
func newRowFromSpec(dc DatacenterInfo, spec BootstrapServiceSpec) *BootstrapWorkload {
	kind := spec.Kind
	if kind == "" {
		kind = provider.KindDeployment
	}

	labels := make(map[string]string, len(spec.Labels)+3)
	maps.Copy(labels, spec.Labels)

	labels["ctrlplane.system"] = "true"
	labels["ctrlplane.datacenter"] = dc.ID.String()
	labels["ctrlplane.bootstrap"] = spec.Name

	bw := NewBootstrapWorkload()
	bw.DatacenterID = dc.ID
	bw.Name = spec.Name
	bw.Kind = kind
	bw.Services = spec.Services
	bw.Labels = labels

	return bw
}

// specChanged reports whether the desired spec body differs from the
// row's persisted body. Compares Kind + Services via JSON marshal —
// a coarse but correct equality check that doesn't have to track
// every field individually. Phase 2 will replace this with a proper
// diff once we have a content-hash on the spec.
func specChanged(bw *BootstrapWorkload, desired BootstrapServiceSpec) bool {
	desiredKind := desired.Kind
	if desiredKind == "" {
		desiredKind = provider.KindDeployment
	}

	if bw.Kind != desiredKind {
		return true
	}

	bwBody, err := json.Marshal(bw.Services)
	if err != nil {
		return true
	}

	desiredBody, err := json.Marshal(desired.Services)
	if err != nil {
		return true
	}

	return string(bwBody) != string(desiredBody)
}

// emitRunning fires a DeploySucceeded event tagged kind=bootstrap so
// operators can grep the audit log for platform installs.
func (s *service) emitRunning(ctx context.Context, dc DatacenterInfo, bw *BootstrapWorkload) {
	if s.events == nil {
		return
	}

	_ = s.events.Publish(ctx, event.NewEvent(event.DeploySucceeded, "").
		WithActor("system:bootstrap").
		WithPayload(map[string]any{
			"kind":          "bootstrap",
			"datacenter_id": dc.ID.String(),
			"bootstrap_id":  bw.ID.String(),
			"name":          bw.Name,
			"provider_ref":  bw.ProviderRef,
		}))
}

// emitFailed fires a DeployFailed event so operators see the
// reconcile failure in the audit log without having to read the
// reconciler logs.
func (s *service) emitFailed(ctx context.Context, dc DatacenterInfo, bw *BootstrapWorkload, msg string) {
	if s.events == nil {
		return
	}

	_ = s.events.Publish(ctx, event.NewEvent(event.DeployFailed, "").
		WithActor("system:bootstrap").
		WithPayload(map[string]any{
			"kind":          "bootstrap",
			"datacenter_id": dc.ID.String(),
			"bootstrap_id":  bw.ID.String(),
			"name":          bw.Name,
			"attempts":      bw.Attempts,
			"error":         msg,
		}))
}

// emitHookFailure surfaces a broken Hook on the audit stream. The
// hook's contributions for this tick are dropped; other hooks +
// declarative specs continue to feed the desired set.
func (s *service) emitHookFailure(ctx context.Context, dc DatacenterInfo, hookName string, hookErr error) {
	if s.events == nil {
		return
	}

	_ = s.events.Publish(ctx, event.NewEvent(event.DeployFailed, "").
		WithActor("system:bootstrap").
		WithPayload(map[string]any{
			"kind":          "bootstrap.hook",
			"datacenter_id": dc.ID.String(),
			"hook":          hookName,
			"error":         hookErr.Error(),
		}))
}

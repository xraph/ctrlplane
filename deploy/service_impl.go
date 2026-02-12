package deploy

import (
	"context"
	"fmt"
	"time"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/provider"
)

// service is the concrete implementation of Service.
type service struct {
	store      Store
	instStore  instance.Store
	providers  *provider.Registry
	events     event.Bus
	auth       auth.Provider
	strategies map[string]Strategy
}

// NewService creates a deploy service with the given dependencies.
func NewService(
	store Store,
	instStore instance.Store,
	providers *provider.Registry,
	events event.Bus,
	authProvider auth.Provider,
) *service {
	return &service{
		store:      store,
		instStore:  instStore,
		providers:  providers,
		events:     events,
		auth:       authProvider,
		strategies: make(map[string]Strategy),
	}
}

// RegisterStrategy adds a deployment strategy to the service.
func (s *service) RegisterStrategy(st Strategy) {
	s.strategies[st.Name()] = st
}

// Deploy creates a new release and deploys it to the instance.
func (s *service) Deploy(ctx context.Context, req DeployRequest) (*Deployment, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("deploy: authenticate: %w", err)
	}

	// Verify the instance exists.
	inst, err := s.instStore.GetByID(ctx, claims.TenantID, req.InstanceID)
	if err != nil {
		return nil, fmt.Errorf("deploy: get instance %s: %w", req.InstanceID, err)
	}

	// Determine the next release version.
	version, err := s.store.NextReleaseVersion(ctx, claims.TenantID, req.InstanceID)
	if err != nil {
		return nil, fmt.Errorf("deploy: next release version: %w", err)
	}

	// Create the immutable release snapshot.
	rel := &Release{
		Entity:     ctrlplane.NewEntity(id.PrefixRelease),
		TenantID:   claims.TenantID,
		InstanceID: req.InstanceID,
		Version:    version,
		Image:      req.Image,
		Env:        req.Env,
		Notes:      req.Notes,
		CommitSHA:  req.CommitSHA,
		Active:     true,
	}

	if err := s.store.InsertRelease(ctx, rel); err != nil {
		return nil, fmt.Errorf("deploy: insert release: %w", err)
	}

	// Choose the deployment strategy.
	strategy := req.Strategy
	if strategy == "" {
		strategy = "rolling"
	}

	// Create the deployment record.
	dep := &Deployment{
		Entity:     ctrlplane.NewEntity(id.PrefixDeployment),
		TenantID:   claims.TenantID,
		InstanceID: req.InstanceID,
		ReleaseID:  rel.ID,
		State:      DeployPending,
		Strategy:   strategy,
		Image:      req.Image,
		Env:        req.Env,
		Initiator:  claims.SubjectID,
	}

	if err := s.store.InsertDeployment(ctx, dep); err != nil {
		return nil, fmt.Errorf("deploy: insert deployment: %w", err)
	}

	// Publish the deploy-started event.
	_ = s.events.Publish(ctx, event.NewEvent(event.DeployStarted, claims.TenantID).
		WithInstance(req.InstanceID).
		WithActor(claims.SubjectID).
		WithPayload(map[string]any{
			"deployment_id": dep.ID.String(),
			"release_id":    rel.ID.String(),
			"image":         req.Image,
		}))

	// Look up the strategy implementation.
	st, ok := s.strategies[strategy]
	if !ok {
		return nil, fmt.Errorf("deploy: unknown strategy %q: %w", strategy, ctrlplane.ErrDeploymentFailed)
	}

	// Resolve the infrastructure provider for this instance.
	prov, err := s.providers.Get(inst.ProviderName)
	if err != nil {
		return nil, fmt.Errorf("deploy: get provider %s: %w", inst.ProviderName, err)
	}

	// Transition to running state.
	now := time.Now().UTC()
	dep.State = DeployRunning
	dep.StartedAt = &now

	if err := s.store.UpdateDeployment(ctx, dep); err != nil {
		return nil, fmt.Errorf("deploy: update deployment to running: %w", err)
	}

	// Execute the deployment strategy.
	execErr := st.Execute(ctx, StrategyParams{
		Deployment: dep,
		Provider:   prov,
		OnProgress: func(string, int, string) {},
	})

	finished := time.Now().UTC()
	dep.FinishedAt = &finished

	if execErr != nil {
		dep.State = DeployFailed
		dep.Error = execErr.Error()

		if updateErr := s.store.UpdateDeployment(ctx, dep); updateErr != nil {
			return nil, fmt.Errorf("deploy: update deployment after failure: %w", updateErr)
		}

		_ = s.events.Publish(ctx, event.NewEvent(event.DeployFailed, claims.TenantID).
			WithInstance(req.InstanceID).
			WithActor(claims.SubjectID).
			WithPayload(map[string]any{
				"deployment_id": dep.ID.String(),
				"error":         execErr.Error(),
			}))

		return dep, fmt.Errorf("deploy: strategy execute: %w", ctrlplane.ErrDeploymentFailed)
	}

	dep.State = DeploySucceeded

	if err := s.store.UpdateDeployment(ctx, dep); err != nil {
		return nil, fmt.Errorf("deploy: update deployment after success: %w", err)
	}

	_ = s.events.Publish(ctx, event.NewEvent(event.DeploySucceeded, claims.TenantID).
		WithInstance(req.InstanceID).
		WithActor(claims.SubjectID).
		WithPayload(map[string]any{
			"deployment_id": dep.ID.String(),
			"release_id":    rel.ID.String(),
		}))

	return dep, nil
}

// Rollback reverts to a specific release by creating a new deployment.
func (s *service) Rollback(ctx context.Context, instanceID id.ID, releaseID id.ID) (*Deployment, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("rollback: authenticate: %w", err)
	}

	// Verify the instance exists.
	inst, err := s.instStore.GetByID(ctx, claims.TenantID, instanceID)
	if err != nil {
		return nil, fmt.Errorf("rollback: get instance %s: %w", instanceID, err)
	}

	// Retrieve the original release to roll back to.
	rel, err := s.store.GetRelease(ctx, claims.TenantID, releaseID)
	if err != nil {
		return nil, fmt.Errorf("rollback: get release %s: %w", releaseID, err)
	}

	// Create a rollback deployment using the recreate strategy.
	dep := &Deployment{
		Entity:     ctrlplane.NewEntity(id.PrefixDeployment),
		TenantID:   claims.TenantID,
		InstanceID: instanceID,
		ReleaseID:  releaseID,
		State:      DeployPending,
		Strategy:   "recreate",
		Image:      rel.Image,
		Env:        rel.Env,
		Initiator:  claims.SubjectID,
	}

	if err := s.store.InsertDeployment(ctx, dep); err != nil {
		return nil, fmt.Errorf("rollback: insert deployment: %w", err)
	}

	// Publish the deploy-started event.
	_ = s.events.Publish(ctx, event.NewEvent(event.DeployStarted, claims.TenantID).
		WithInstance(instanceID).
		WithActor(claims.SubjectID).
		WithPayload(map[string]any{
			"deployment_id": dep.ID.String(),
			"release_id":    releaseID.String(),
			"rollback":      true,
		}))

	// Look up the recreate strategy.
	st, ok := s.strategies["recreate"]
	if !ok {
		return nil, fmt.Errorf("rollback: unknown strategy %q: %w", "recreate", ctrlplane.ErrDeploymentFailed)
	}

	// Resolve the infrastructure provider.
	prov, err := s.providers.Get(inst.ProviderName)
	if err != nil {
		return nil, fmt.Errorf("rollback: get provider %s: %w", inst.ProviderName, err)
	}

	// Transition to running state.
	now := time.Now().UTC()
	dep.State = DeployRunning
	dep.StartedAt = &now

	if err := s.store.UpdateDeployment(ctx, dep); err != nil {
		return nil, fmt.Errorf("rollback: update deployment to running: %w", err)
	}

	// Execute the rollback deployment.
	execErr := st.Execute(ctx, StrategyParams{
		Deployment: dep,
		Provider:   prov,
		OnProgress: func(string, int, string) {},
	})

	finished := time.Now().UTC()
	dep.FinishedAt = &finished

	if execErr != nil {
		dep.State = DeployFailed
		dep.Error = execErr.Error()

		if updateErr := s.store.UpdateDeployment(ctx, dep); updateErr != nil {
			return nil, fmt.Errorf("rollback: update deployment after failure: %w", updateErr)
		}

		_ = s.events.Publish(ctx, event.NewEvent(event.DeployFailed, claims.TenantID).
			WithInstance(instanceID).
			WithActor(claims.SubjectID).
			WithPayload(map[string]any{
				"deployment_id": dep.ID.String(),
				"error":         execErr.Error(),
			}))

		return dep, fmt.Errorf("rollback: strategy execute: %w", ctrlplane.ErrDeploymentFailed)
	}

	dep.State = DeploySucceeded

	if err := s.store.UpdateDeployment(ctx, dep); err != nil {
		return nil, fmt.Errorf("rollback: update deployment after success: %w", err)
	}

	// Publish the rolled-back event on success.
	_ = s.events.Publish(ctx, event.NewEvent(event.DeployRolledBack, claims.TenantID).
		WithInstance(instanceID).
		WithActor(claims.SubjectID).
		WithPayload(map[string]any{
			"deployment_id": dep.ID.String(),
			"release_id":    releaseID.String(),
		}))

	return dep, nil
}

// Cancel aborts an in-progress deployment.
func (s *service) Cancel(ctx context.Context, deploymentID id.ID) error {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return fmt.Errorf("cancel: authenticate: %w", err)
	}

	dep, err := s.store.GetDeployment(ctx, claims.TenantID, deploymentID)
	if err != nil {
		return fmt.Errorf("cancel: get deployment %s: %w", deploymentID, err)
	}

	if dep.State != DeployPending && dep.State != DeployRunning {
		return fmt.Errorf("cancel: deployment in state %s: %w", dep.State, ctrlplane.ErrInvalidState)
	}

	now := time.Now().UTC()
	dep.State = DeployCancelled
	dep.FinishedAt = &now

	if err := s.store.UpdateDeployment(ctx, dep); err != nil {
		return fmt.Errorf("cancel: update deployment: %w", err)
	}

	return nil
}

// GetDeployment returns a specific deployment.
func (s *service) GetDeployment(ctx context.Context, deploymentID id.ID) (*Deployment, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("get deployment: authenticate: %w", err)
	}

	dep, err := s.store.GetDeployment(ctx, claims.TenantID, deploymentID)
	if err != nil {
		return nil, fmt.Errorf("get deployment %s: %w", deploymentID, err)
	}

	return dep, nil
}

// ListDeployments lists deployments for an instance.
func (s *service) ListDeployments(ctx context.Context, instanceID id.ID, opts ListOptions) (*DeployListResult, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("list deployments: authenticate: %w", err)
	}

	result, err := s.store.ListDeployments(ctx, claims.TenantID, instanceID, opts)
	if err != nil {
		return nil, fmt.Errorf("list deployments for instance %s: %w", instanceID, err)
	}

	return result, nil
}

// GetRelease returns a specific release.
func (s *service) GetRelease(ctx context.Context, releaseID id.ID) (*Release, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("get release: authenticate: %w", err)
	}

	rel, err := s.store.GetRelease(ctx, claims.TenantID, releaseID)
	if err != nil {
		return nil, fmt.Errorf("get release %s: %w", releaseID, err)
	}

	return rel, nil
}

// ListReleases lists releases for an instance.
func (s *service) ListReleases(ctx context.Context, instanceID id.ID, opts ListOptions) (*ReleaseListResult, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("list releases: authenticate: %w", err)
	}

	result, err := s.store.ListReleases(ctx, claims.TenantID, instanceID, opts)
	if err != nil {
		return nil, fmt.Errorf("list releases for instance %s: %w", instanceID, err)
	}

	return result, nil
}

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
	"github.com/xraph/ctrlplane/secrets"
)

// service is the concrete implementation of Service.
type service struct {
	store      Store
	instStore  instance.Store
	providers  *provider.Registry
	events     event.Bus
	auth       auth.Provider
	vault      secrets.Vault
	strategies map[string]Strategy
}

// NewService creates a deploy service with the given dependencies.
func NewService(
	store Store,
	instStore instance.Store,
	providers *provider.Registry,
	events event.Bus,
	authProvider auth.Provider,
	vault secrets.Vault,
) *service {
	return &service{
		store:      store,
		instStore:  instStore,
		providers:  providers,
		events:     events,
		auth:       authProvider,
		vault:      vault,
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

	// Store config files from template in vault (if deploying from template with config files).
	if s.vault != nil && len(req.ConfigFiles) > 0 {
		for _, cf := range req.ConfigFiles {
			vaultKey := fmt.Sprintf("%s/%s/%s", claims.TenantID, req.InstanceID, cf.Name)

			if err := s.vault.Store(ctx, vaultKey, []byte(cf.Content)); err != nil {
				return nil, fmt.Errorf("deploy: store config file %q in vault: %w", cf.Name, err)
			}
		}
	}

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

// --- Template CRUD ---

// CreateTemplate creates a new reusable deployment template.
func (s *service) CreateTemplate(ctx context.Context, req CreateTemplateRequest) (*Template, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("create template: authenticate: %w", err)
	}

	strategy := req.Strategy
	if strategy == "" {
		strategy = "rolling"
	}

	tmpl := &Template{
		Entity:      ctrlplane.NewEntity(id.PrefixTemplate),
		TenantID:    claims.TenantID,
		Name:        req.Name,
		Description: req.Description,
		Image:       req.Image,
		Strategy:    strategy,
		Resources:   req.Resources,
		Ports:       req.Ports,
		Volumes:     req.Volumes,
		HealthCheck: req.HealthCheck,
		Env:         req.Env,
		Secrets:     req.Secrets,
		ConfigFiles: req.ConfigFiles,
		Labels:      req.Labels,
		Annotations: req.Annotations,
		CommitSHA:   req.CommitSHA,
		Notes:       req.Notes,
	}

	if err := s.store.InsertTemplate(ctx, tmpl); err != nil {
		return nil, fmt.Errorf("create template: insert: %w", err)
	}

	return tmpl, nil
}

// GetTemplate returns a specific deployment template.
func (s *service) GetTemplate(ctx context.Context, templateID id.ID) (*Template, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("get template: authenticate: %w", err)
	}

	tmpl, err := s.store.GetTemplate(ctx, claims.TenantID, templateID)
	if err != nil {
		return nil, fmt.Errorf("get template %s: %w", templateID, err)
	}

	return tmpl, nil
}

// UpdateTemplate updates an existing deployment template.
func (s *service) UpdateTemplate(ctx context.Context, templateID id.ID, req UpdateTemplateRequest) (*Template, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("update template: authenticate: %w", err)
	}

	tmpl, err := s.store.GetTemplate(ctx, claims.TenantID, templateID)
	if err != nil {
		return nil, fmt.Errorf("update template: get %s: %w", templateID, err)
	}

	if req.Name != nil {
		tmpl.Name = *req.Name
	}

	if req.Description != nil {
		tmpl.Description = *req.Description
	}

	if req.Image != nil {
		tmpl.Image = *req.Image
	}

	if req.Strategy != nil {
		tmpl.Strategy = *req.Strategy
	}

	if req.Resources != nil {
		tmpl.Resources = *req.Resources
	}

	if req.Ports != nil {
		tmpl.Ports = req.Ports
	}

	if req.Volumes != nil {
		tmpl.Volumes = req.Volumes
	}

	if req.HealthCheck != nil {
		tmpl.HealthCheck = req.HealthCheck
	}

	if req.Env != nil {
		tmpl.Env = req.Env
	}

	if req.Secrets != nil {
		tmpl.Secrets = req.Secrets
	}

	if req.ConfigFiles != nil {
		tmpl.ConfigFiles = req.ConfigFiles
	}

	if req.Labels != nil {
		tmpl.Labels = req.Labels
	}

	if req.Annotations != nil {
		tmpl.Annotations = req.Annotations
	}

	if req.CommitSHA != nil {
		tmpl.CommitSHA = *req.CommitSHA
	}

	if req.Notes != nil {
		tmpl.Notes = *req.Notes
	}

	if err := s.store.UpdateTemplate(ctx, tmpl); err != nil {
		return nil, fmt.Errorf("update template %s: %w", templateID, err)
	}

	return tmpl, nil
}

// DeleteTemplate removes a deployment template.
func (s *service) DeleteTemplate(ctx context.Context, templateID id.ID) error {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return fmt.Errorf("delete template: authenticate: %w", err)
	}

	if err := s.store.DeleteTemplate(ctx, claims.TenantID, templateID); err != nil {
		return fmt.Errorf("delete template %s: %w", templateID, err)
	}

	return nil
}

// ListTemplates lists deployment templates for the current tenant.
func (s *service) ListTemplates(ctx context.Context, opts ListOptions) (*TemplateListResult, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("list templates: authenticate: %w", err)
	}

	result, err := s.store.ListTemplates(ctx, claims.TenantID, opts)
	if err != nil {
		return nil, fmt.Errorf("list templates: %w", err)
	}

	return result, nil
}

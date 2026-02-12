package health

import (
	"context"
	"fmt"
	"time"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/id"
)

// service implements the Service interface.
type service struct {
	store    Store
	events   event.Bus
	auth     auth.Provider
	checkers map[CheckType]Checker
}

// NewService creates a new health service.
func NewService(store Store, events event.Bus, auth auth.Provider) Service {
	return &service{
		store:    store,
		events:   events,
		auth:     auth,
		checkers: make(map[CheckType]Checker),
	}
}

// Configure adds or updates a health check for an instance.
func (s *service) Configure(ctx context.Context, req ConfigureRequest) (*HealthCheck, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("configure health check: %w", err)
	}

	check := &HealthCheck{
		Entity:     ctrlplane.NewEntity(id.PrefixHealthCheck),
		TenantID:   claims.TenantID,
		InstanceID: req.InstanceID,
		Name:       req.Name,
		Type:       req.Type,
		Target:     req.Target,
		Interval:   req.Interval,
		Timeout:    req.Timeout,
		Retries:    req.Retries,
		Enabled:    true,
	}

	if err := s.store.InsertCheck(ctx, check); err != nil {
		return nil, fmt.Errorf("configure health check: insert: %w", err)
	}

	return check, nil
}

// Remove deletes a health check.
func (s *service) Remove(ctx context.Context, checkID id.ID) error {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return fmt.Errorf("remove health check: %w", err)
	}

	_, err = s.store.GetCheck(ctx, claims.TenantID, checkID)
	if err != nil {
		return fmt.Errorf("remove health check: get: %w", err)
	}

	if err := s.store.DeleteCheck(ctx, claims.TenantID, checkID); err != nil {
		return fmt.Errorf("remove health check: delete: %w", err)
	}

	return nil
}

// GetHealth returns aggregate health for an instance.
func (s *service) GetHealth(ctx context.Context, instanceID id.ID) (*InstanceHealth, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("get health: %w", err)
	}

	checks, err := s.store.ListChecks(ctx, claims.TenantID, instanceID)
	if err != nil {
		return nil, fmt.Errorf("get health: list checks: %w", err)
	}

	if len(checks) == 0 {
		return &InstanceHealth{
			InstanceID: instanceID,
			Status:     StatusUnknown,
			Checks:     []CheckSummary{},
		}, nil
	}

	summaries := make([]CheckSummary, 0, len(checks))
	healthyCount := 0
	failingCount := 0

	var lastChecked time.Time

	for _, check := range checks {
		result, err := s.store.GetLatestResult(ctx, claims.TenantID, check.ID)

		summary := CheckSummary{
			CheckID: check.ID,
			Name:    check.Name,
		}

		if err != nil || result == nil {
			summary.Status = StatusUnknown
		} else {
			summary.Status = result.Status
			summary.Latency = result.Latency
			summary.LastResult = result

			if result.CheckedAt.After(lastChecked) {
				lastChecked = result.CheckedAt
			}

			if result.Status == StatusHealthy {
				healthyCount++
			} else {
				failingCount++
			}
		}

		summaries = append(summaries, summary)
	}

	// Determine aggregate status.
	var status Status

	switch {
	case healthyCount == len(checks):
		status = StatusHealthy
	case failingCount == len(checks):
		status = StatusUnhealthy
	default:
		status = StatusDegraded
	}

	return &InstanceHealth{
		InstanceID:  instanceID,
		Status:      status,
		Checks:      summaries,
		LastChecked: lastChecked,
	}, nil
}

// GetHistory returns check results over time.
func (s *service) GetHistory(ctx context.Context, checkID id.ID, opts HistoryOptions) ([]HealthResult, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("get history: %w", err)
	}

	// Retrieve the check to get the tenant ID for scoping.
	_, err = s.store.GetCheck(ctx, claims.TenantID, checkID)
	if err != nil {
		return nil, fmt.Errorf("get history: get check: %w", err)
	}

	results, err := s.store.ListResults(ctx, claims.TenantID, checkID, opts)
	if err != nil {
		return nil, fmt.Errorf("get history: list results: %w", err)
	}

	return results, nil
}

// ListChecks returns all checks for an instance.
func (s *service) ListChecks(ctx context.Context, instanceID id.ID) ([]HealthCheck, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("list checks: %w", err)
	}

	checks, err := s.store.ListChecks(ctx, claims.TenantID, instanceID)
	if err != nil {
		return nil, fmt.Errorf("list checks: %w", err)
	}

	return checks, nil
}

// RunCheck executes a one-off health check.
func (s *service) RunCheck(ctx context.Context, checkID id.ID) (*HealthResult, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("run check: %w", err)
	}

	check, err := s.store.GetCheck(ctx, claims.TenantID, checkID)
	if err != nil {
		return nil, fmt.Errorf("run check: get check: %w", err)
	}

	checker, ok := s.checkers[check.Type]
	if !ok {
		return nil, fmt.Errorf("run check: no checker registered for type %q", check.Type)
	}

	result, err := checker.Check(ctx, check)
	if err != nil {
		return nil, fmt.Errorf("run check: execute: %w", err)
	}

	if err := s.store.InsertResult(ctx, result); err != nil {
		return nil, fmt.Errorf("run check: insert result: %w", err)
	}

	return result, nil
}

// RegisterChecker adds a custom checker type.
func (s *service) RegisterChecker(checker Checker) {
	s.checkers[checker.Type()] = checker
}

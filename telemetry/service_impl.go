package telemetry

import (
	"context"
	"fmt"

	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/id"
)

// service implements the Service interface.
type service struct {
	store      Store
	auth       auth.Provider
	collectors []Collector
}

// NewService creates a new telemetry service.
func NewService(store Store, auth auth.Provider) Service {
	return &service{
		store:      store,
		auth:       auth,
		collectors: make([]Collector, 0),
	}
}

// PushMetrics ingests metric data points.
func (s *service) PushMetrics(ctx context.Context, metrics []Metric) error {
	if err := s.store.InsertMetrics(ctx, metrics); err != nil {
		return fmt.Errorf("push metrics: %w", err)
	}

	return nil
}

// QueryMetrics returns metrics matching the query.
func (s *service) QueryMetrics(ctx context.Context, q MetricQuery) ([]Metric, error) {
	results, err := s.store.QueryMetrics(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("query metrics: %w", err)
	}

	return results, nil
}

// PushLogs ingests log entries.
func (s *service) PushLogs(ctx context.Context, logs []LogEntry) error {
	if err := s.store.InsertLogs(ctx, logs); err != nil {
		return fmt.Errorf("push logs: %w", err)
	}

	return nil
}

// QueryLogs returns log entries matching the query.
func (s *service) QueryLogs(ctx context.Context, q LogQuery) ([]LogEntry, error) {
	results, err := s.store.QueryLogs(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("query logs: %w", err)
	}

	return results, nil
}

// PushTraces ingests trace spans.
func (s *service) PushTraces(ctx context.Context, traces []Trace) error {
	if err := s.store.InsertTraces(ctx, traces); err != nil {
		return fmt.Errorf("push traces: %w", err)
	}

	return nil
}

// QueryTraces returns traces matching the query.
func (s *service) QueryTraces(ctx context.Context, q TraceQuery) ([]Trace, error) {
	results, err := s.store.QueryTraces(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("query traces: %w", err)
	}

	return results, nil
}

// GetCurrentResources returns the latest resource snapshot for an instance.
func (s *service) GetCurrentResources(ctx context.Context, instanceID id.ID) (*ResourceSnapshot, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("get current resources: %w", err)
	}

	snap, err := s.store.GetLatestResourceSnapshot(ctx, claims.TenantID, instanceID)
	if err != nil {
		return nil, fmt.Errorf("get current resources: %w", err)
	}

	return snap, nil
}

// GetResourceHistory returns resource snapshots over a time range.
func (s *service) GetResourceHistory(ctx context.Context, instanceID id.ID, opts TimeRange) ([]ResourceSnapshot, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("get resource history: %w", err)
	}

	snapshots, err := s.store.ListResourceSnapshots(ctx, claims.TenantID, instanceID, opts)
	if err != nil {
		return nil, fmt.Errorf("get resource history: %w", err)
	}

	return snapshots, nil
}

// GetDashboard returns a pre-aggregated view of instance telemetry.
func (s *service) GetDashboard(ctx context.Context, instanceID id.ID) (*DashboardData, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("get dashboard: %w", err)
	}

	snap, err := s.store.GetLatestResourceSnapshot(ctx, claims.TenantID, instanceID)
	if err != nil {
		return nil, fmt.Errorf("get dashboard: get resources: %w", err)
	}

	return &DashboardData{
		InstanceID: instanceID,
		Resources:  snap,
	}, nil
}

// RegisterCollector adds a custom telemetry collector.
func (s *service) RegisterCollector(collector Collector) {
	s.collectors = append(s.collectors, collector)
}

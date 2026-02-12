package memory

import (
	"context"
	"fmt"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/telemetry"
)

func (s *Store) InsertMetrics(_ context.Context, metrics []telemetry.Metric) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.metrics = append(s.metrics, metrics...)

	return nil
}

func (s *Store) QueryMetrics(_ context.Context, q telemetry.MetricQuery) ([]telemetry.Metric, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	instKey := idStr(q.InstanceID)

	var result []telemetry.Metric

	for _, m := range s.metrics {
		if idStr(m.InstanceID) != instKey {
			continue
		}

		if q.Name != "" && m.Name != q.Name {
			continue
		}

		if !q.Since.IsZero() && m.Timestamp.Before(q.Since) {
			continue
		}

		if !q.Until.IsZero() && m.Timestamp.After(q.Until) {
			continue
		}

		result = append(result, m)
	}

	if q.Limit > 0 && len(result) > q.Limit {
		result = result[:q.Limit]
	}

	return result, nil
}

func (s *Store) InsertLogs(_ context.Context, logs []telemetry.LogEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.logs = append(s.logs, logs...)

	return nil
}

func (s *Store) QueryLogs(_ context.Context, q telemetry.LogQuery) ([]telemetry.LogEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	instKey := idStr(q.InstanceID)

	var result []telemetry.LogEntry

	for _, l := range s.logs {
		if idStr(l.InstanceID) != instKey {
			continue
		}

		if q.Level != "" && l.Level != q.Level {
			continue
		}

		if !q.Since.IsZero() && l.Timestamp.Before(q.Since) {
			continue
		}

		if !q.Until.IsZero() && l.Timestamp.After(q.Until) {
			continue
		}

		result = append(result, l)
	}

	if q.Limit > 0 && len(result) > q.Limit {
		result = result[:q.Limit]
	}

	return result, nil
}

func (s *Store) InsertTraces(_ context.Context, traces []telemetry.Trace) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.traces = append(s.traces, traces...)

	return nil
}

func (s *Store) QueryTraces(_ context.Context, q telemetry.TraceQuery) ([]telemetry.Trace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	instKey := idStr(q.InstanceID)

	var result []telemetry.Trace

	for _, t := range s.traces {
		if idStr(t.InstanceID) != instKey {
			continue
		}

		if q.TraceID != "" && t.TraceID != q.TraceID {
			continue
		}

		if q.Operation != "" && t.Operation != q.Operation {
			continue
		}

		if !q.Since.IsZero() && t.Timestamp.Before(q.Since) {
			continue
		}

		if !q.Until.IsZero() && t.Timestamp.After(q.Until) {
			continue
		}

		result = append(result, t)
	}

	if q.Limit > 0 && len(result) > q.Limit {
		result = result[:q.Limit]
	}

	return result, nil
}

func (s *Store) InsertResourceSnapshot(_ context.Context, snap *telemetry.ResourceSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.resourceSnapshots = append(s.resourceSnapshots, *snap)

	return nil
}

func (s *Store) GetLatestResourceSnapshot(_ context.Context, tenantID string, instanceID id.ID) (*telemetry.ResourceSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	instKey := idStr(instanceID)

	for i := len(s.resourceSnapshots) - 1; i >= 0; i-- {
		snap := s.resourceSnapshots[i]
		if idStr(snap.InstanceID) == instKey && snap.TenantID == tenantID {
			return &snap, nil
		}
	}

	return nil, fmt.Errorf("%w: no resource snapshot for instance %s", ctrlplane.ErrNotFound, instanceID)
}

func (s *Store) ListResourceSnapshots(_ context.Context, tenantID string, instanceID id.ID, opts telemetry.TimeRange) ([]telemetry.ResourceSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	instKey := idStr(instanceID)

	var result []telemetry.ResourceSnapshot

	for _, snap := range s.resourceSnapshots {
		if idStr(snap.InstanceID) != instKey || snap.TenantID != tenantID {
			continue
		}

		if !opts.Since.IsZero() && snap.Timestamp.Before(opts.Since) {
			continue
		}

		if !opts.Until.IsZero() && snap.Timestamp.After(opts.Until) {
			continue
		}

		result = append(result, snap)
	}

	return result, nil
}

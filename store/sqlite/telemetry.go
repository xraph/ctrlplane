package sqlite

import (
	"context"
	"fmt"
	"time"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/telemetry"
)

func (s *Store) InsertMetrics(ctx context.Context, metrics []telemetry.Metric) error {
	if len(metrics) == 0 {
		return nil
	}

	models := make([]metricModel, 0, len(metrics))
	for i := range metrics {
		models = append(models, *toMetricModel(&metrics[i]))
	}

	_, err := s.sdb.NewInsert(&models).Exec(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: insert metrics failed: %w", err)
	}

	return nil
}

func (s *Store) QueryMetrics(ctx context.Context, q telemetry.MetricQuery) ([]telemetry.Metric, error) {
	var models []metricModel

	query := s.sdb.NewSelect(&models)

	if q.InstanceID.String() != "" {
		query = query.Where("instance_id = ?", q.InstanceID.String())
	}

	if q.Name != "" {
		query = query.Where("name = ?", q.Name)
	}

	if !q.Since.IsZero() {
		query = query.Where("timestamp >= ?", q.Since)
	}

	if !q.Until.IsZero() {
		query = query.Where("timestamp <= ?", q.Until)
	}

	query = query.OrderExpr("timestamp DESC")

	if q.Limit > 0 {
		query = query.Limit(q.Limit)
	}

	if err := query.Scan(ctx); err != nil {
		return nil, fmt.Errorf("sqlite: query metrics failed: %w", err)
	}

	items := make([]telemetry.Metric, 0, len(models))
	for _, model := range models {
		m := telemetry.Metric{
			TenantID:   model.TenantID,
			InstanceID: id.MustParse(model.InstanceID),
			Name:       model.Name,
			Type:       telemetry.MetricType(model.Type),
			Value:      model.Value,
			Timestamp:  model.Timestamp,
		}
		items = append(items, m)
	}

	return items, nil
}

func (s *Store) InsertLogs(ctx context.Context, logs []telemetry.LogEntry) error {
	if len(logs) == 0 {
		return nil
	}

	models := make([]logEntryModel, 0, len(logs))
	for i := range logs {
		models = append(models, *toLogEntryModel(&logs[i]))
	}

	_, err := s.sdb.NewInsert(&models).Exec(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: insert logs failed: %w", err)
	}

	return nil
}

func (s *Store) QueryLogs(ctx context.Context, q telemetry.LogQuery) ([]telemetry.LogEntry, error) {
	var models []logEntryModel

	query := s.sdb.NewSelect(&models)

	if q.InstanceID.String() != "" {
		query = query.Where("instance_id = ?", q.InstanceID.String())
	}

	if q.Level != "" {
		query = query.Where("level = ?", q.Level)
	}

	if !q.Since.IsZero() {
		query = query.Where("timestamp >= ?", q.Since)
	}

	if !q.Until.IsZero() {
		query = query.Where("timestamp <= ?", q.Until)
	}

	query = query.OrderExpr("timestamp DESC")

	if q.Limit > 0 {
		query = query.Limit(q.Limit)
	}

	if err := query.Scan(ctx); err != nil {
		return nil, fmt.Errorf("sqlite: query logs failed: %w", err)
	}

	items := make([]telemetry.LogEntry, 0, len(models))
	for _, model := range models {
		entry := telemetry.LogEntry{
			TenantID:   model.TenantID,
			InstanceID: id.MustParse(model.InstanceID),
			Level:      model.Level,
			Message:    model.Message,
			Source:     model.Source,
			Timestamp:  model.Timestamp,
		}
		items = append(items, entry)
	}

	return items, nil
}

func (s *Store) InsertTraces(ctx context.Context, traces []telemetry.Trace) error {
	if len(traces) == 0 {
		return nil
	}

	models := make([]traceModel, 0, len(traces))
	for i := range traces {
		models = append(models, *toTraceModel(&traces[i]))
	}

	_, err := s.sdb.NewInsert(&models).Exec(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: insert traces failed: %w", err)
	}

	return nil
}

func (s *Store) QueryTraces(ctx context.Context, q telemetry.TraceQuery) ([]telemetry.Trace, error) {
	var models []traceModel

	query := s.sdb.NewSelect(&models)

	if q.InstanceID.String() != "" {
		query = query.Where("instance_id = ?", q.InstanceID.String())
	}

	if q.TraceID != "" {
		query = query.Where("trace_id = ?", q.TraceID)
	}

	if q.Operation != "" {
		query = query.Where("operation = ?", q.Operation)
	}

	if !q.Since.IsZero() {
		query = query.Where("timestamp >= ?", q.Since)
	}

	if !q.Until.IsZero() {
		query = query.Where("timestamp <= ?", q.Until)
	}

	query = query.OrderExpr("timestamp DESC")

	if q.Limit > 0 {
		query = query.Limit(q.Limit)
	}

	if err := query.Scan(ctx); err != nil {
		return nil, fmt.Errorf("sqlite: query traces failed: %w", err)
	}

	items := make([]telemetry.Trace, 0, len(models))
	for _, model := range models {
		trace := telemetry.Trace{
			TenantID:   model.TenantID,
			InstanceID: id.MustParse(model.InstanceID),
			TraceID:    model.TraceID,
			SpanID:     model.SpanID,
			ParentID:   model.ParentID,
			Operation:  model.Operation,
			Duration:   time.Duration(model.Duration),
			Status:     model.Status,
			Timestamp:  model.Timestamp,
		}
		items = append(items, trace)
	}

	return items, nil
}

func (s *Store) InsertResourceSnapshot(ctx context.Context, snap *telemetry.ResourceSnapshot) error {
	model := toResourceSnapshotModel(snap)

	_, err := s.sdb.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: insert resource snapshot failed: %w", err)
	}

	return nil
}

func (s *Store) GetLatestResourceSnapshot(ctx context.Context, tenantID string, instanceID id.ID) (*telemetry.ResourceSnapshot, error) {
	var model resourceSnapshotModel

	err := s.sdb.NewSelect(&model).
		Where("tenant_id = ? AND instance_id = ?", tenantID, instanceID.String()).
		OrderExpr("timestamp DESC").
		Limit(1).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf("%w: no snapshots for instance %s", ctrlplane.ErrNotFound, instanceID)
		}

		return nil, fmt.Errorf("sqlite: get latest resource snapshot failed: %w", err)
	}

	snap := &telemetry.ResourceSnapshot{
		TenantID:      model.TenantID,
		InstanceID:    id.MustParse(model.InstanceID),
		CPUPercent:    model.CPUPercent,
		MemoryUsedMB:  model.MemoryUsedMB,
		MemoryLimitMB: model.MemoryLimitMB,
		DiskUsedMB:    model.DiskUsedMB,
		NetworkInMB:   model.NetworkInMB,
		NetworkOutMB:  model.NetworkOutMB,
		Timestamp:     model.Timestamp,
	}

	return snap, nil
}

func (s *Store) ListResourceSnapshots(ctx context.Context, tenantID string, instanceID id.ID, opts telemetry.TimeRange) ([]telemetry.ResourceSnapshot, error) {
	var models []resourceSnapshotModel

	q := s.sdb.NewSelect(&models).
		Where("tenant_id = ? AND instance_id = ?", tenantID, instanceID.String())

	if !opts.Since.IsZero() {
		q = q.Where("timestamp >= ?", opts.Since)
	}

	if !opts.Until.IsZero() {
		q = q.Where("timestamp <= ?", opts.Until)
	}

	q = q.OrderExpr("timestamp DESC")

	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("sqlite: list resource snapshots failed: %w", err)
	}

	items := make([]telemetry.ResourceSnapshot, 0, len(models))
	for _, model := range models {
		snap := telemetry.ResourceSnapshot{
			TenantID:      model.TenantID,
			InstanceID:    id.MustParse(model.InstanceID),
			CPUPercent:    model.CPUPercent,
			MemoryUsedMB:  model.MemoryUsedMB,
			MemoryLimitMB: model.MemoryLimitMB,
			DiskUsedMB:    model.DiskUsedMB,
			NetworkInMB:   model.NetworkInMB,
			NetworkOutMB:  model.NetworkOutMB,
			Timestamp:     model.Timestamp,
		}
		items = append(items, snap)
	}

	return items, nil
}

package postgres

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

	_, err := s.pg.NewInsert(&models).Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: insert metrics failed: %w", err)
	}

	return nil
}

func (s *Store) QueryMetrics(ctx context.Context, q telemetry.MetricQuery) ([]telemetry.Metric, error) {
	var models []metricModel

	query := s.pg.NewSelect(&models)

	argIdx := 0
	if q.InstanceID.String() != "" {
		argIdx++
		query = query.Where(fmt.Sprintf("instance_id = $%d", argIdx), q.InstanceID.String())
	}

	if q.Name != "" {
		argIdx++
		query = query.Where(fmt.Sprintf("name = $%d", argIdx), q.Name)
	}

	if !q.Since.IsZero() {
		argIdx++
		query = query.Where(fmt.Sprintf("timestamp >= $%d", argIdx), q.Since)
	}

	if !q.Until.IsZero() {
		argIdx++
		query = query.Where(fmt.Sprintf("timestamp <= $%d", argIdx), q.Until)
	}

	query = query.OrderExpr("timestamp DESC")

	if q.Limit > 0 {
		query = query.Limit(q.Limit)
	}

	if err := query.Scan(ctx); err != nil {
		return nil, fmt.Errorf("postgres: query metrics failed: %w", err)
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

	_, err := s.pg.NewInsert(&models).Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: insert logs failed: %w", err)
	}

	return nil
}

func (s *Store) QueryLogs(ctx context.Context, q telemetry.LogQuery) ([]telemetry.LogEntry, error) {
	var models []logEntryModel

	query := s.pg.NewSelect(&models)

	argIdx := 0
	if q.InstanceID.String() != "" {
		argIdx++
		query = query.Where(fmt.Sprintf("instance_id = $%d", argIdx), q.InstanceID.String())
	}

	if q.Level != "" {
		argIdx++
		query = query.Where(fmt.Sprintf("level = $%d", argIdx), q.Level)
	}

	if !q.Since.IsZero() {
		argIdx++
		query = query.Where(fmt.Sprintf("timestamp >= $%d", argIdx), q.Since)
	}

	if !q.Until.IsZero() {
		argIdx++
		query = query.Where(fmt.Sprintf("timestamp <= $%d", argIdx), q.Until)
	}

	query = query.OrderExpr("timestamp DESC")

	if q.Limit > 0 {
		query = query.Limit(q.Limit)
	}

	if err := query.Scan(ctx); err != nil {
		return nil, fmt.Errorf("postgres: query logs failed: %w", err)
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

	_, err := s.pg.NewInsert(&models).Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: insert traces failed: %w", err)
	}

	return nil
}

func (s *Store) QueryTraces(ctx context.Context, q telemetry.TraceQuery) ([]telemetry.Trace, error) {
	var models []traceModel

	query := s.pg.NewSelect(&models)

	argIdx := 0
	if q.InstanceID.String() != "" {
		argIdx++
		query = query.Where(fmt.Sprintf("instance_id = $%d", argIdx), q.InstanceID.String())
	}

	if q.TraceID != "" {
		argIdx++
		query = query.Where(fmt.Sprintf("trace_id = $%d", argIdx), q.TraceID)
	}

	if q.Operation != "" {
		argIdx++
		query = query.Where(fmt.Sprintf("operation = $%d", argIdx), q.Operation)
	}

	if !q.Since.IsZero() {
		argIdx++
		query = query.Where(fmt.Sprintf("timestamp >= $%d", argIdx), q.Since)
	}

	if !q.Until.IsZero() {
		argIdx++
		query = query.Where(fmt.Sprintf("timestamp <= $%d", argIdx), q.Until)
	}

	query = query.OrderExpr("timestamp DESC")

	if q.Limit > 0 {
		query = query.Limit(q.Limit)
	}

	if err := query.Scan(ctx); err != nil {
		return nil, fmt.Errorf("postgres: query traces failed: %w", err)
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

	_, err := s.pg.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: insert resource snapshot failed: %w", err)
	}

	return nil
}

func (s *Store) GetLatestResourceSnapshot(ctx context.Context, tenantID string, instanceID id.ID) (*telemetry.ResourceSnapshot, error) {
	var model resourceSnapshotModel

	err := s.pg.NewSelect(&model).
		Where("tenant_id = $1 AND instance_id = $2", tenantID, instanceID.String()).
		OrderExpr("timestamp DESC").
		Limit(1).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf("%w: no snapshots for instance %s", ctrlplane.ErrNotFound, instanceID)
		}

		return nil, fmt.Errorf("postgres: get latest resource snapshot failed: %w", err)
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

	q := s.pg.NewSelect(&models).
		Where("tenant_id = $1 AND instance_id = $2", tenantID, instanceID.String())

	argIdx := 2
	if !opts.Since.IsZero() {
		argIdx++
		q = q.Where(fmt.Sprintf("timestamp >= $%d", argIdx), opts.Since)
	}

	if !opts.Until.IsZero() {
		argIdx++
		q = q.Where(fmt.Sprintf("timestamp <= $%d", argIdx), opts.Until)
	}

	q = q.OrderExpr("timestamp DESC")

	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("postgres: list resource snapshots failed: %w", err)
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

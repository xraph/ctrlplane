package mongo

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/telemetry"
)

func (s *Store) InsertMetrics(ctx context.Context, metrics []telemetry.Metric) error {
	if len(metrics) == 0 {
		return nil
	}

	models := make([]*metricModel, 0, len(metrics))
	for i := range metrics {
		models = append(models, toMetricModel(&metrics[i]))
	}

	_, err := s.mdb.NewInsert(&models).Exec(ctx)
	if err != nil {
		return fmt.Errorf("mongo: insert metrics failed: %w", err)
	}

	return nil
}

func (s *Store) QueryMetrics(ctx context.Context, q telemetry.MetricQuery) ([]telemetry.Metric, error) {
	var models []metricModel

	f := bson.M{}
	if q.InstanceID.String() != "" {
		f["instance_id"] = q.InstanceID.String()
	}

	if q.Name != "" {
		f["name"] = q.Name
	}

	if !q.Since.IsZero() {
		f["timestamp"] = bson.M{"$gte": q.Since}
	}

	if !q.Until.IsZero() {
		if existing, ok := f["timestamp"]; ok {
			existing.(bson.M)["$lte"] = q.Until
		} else {
			f["timestamp"] = bson.M{"$lte": q.Until}
		}
	}

	query := s.mdb.NewFind(&models).
		Filter(f).
		Sort(bson.D{{Key: "timestamp", Value: -1}})

	if q.Limit > 0 {
		query = query.Limit(int64(q.Limit))
	}

	if err := query.Scan(ctx); err != nil {
		return nil, fmt.Errorf("mongo: query metrics failed: %w", err)
	}

	items := make([]telemetry.Metric, 0, len(models))
	for _, model := range models {
		items = append(items, fromMetricModel(&model))
	}

	return items, nil
}

func (s *Store) InsertLogs(ctx context.Context, logs []telemetry.LogEntry) error {
	if len(logs) == 0 {
		return nil
	}

	models := make([]*logEntryModel, 0, len(logs))
	for i := range logs {
		models = append(models, toLogEntryModel(&logs[i]))
	}

	_, err := s.mdb.NewInsert(&models).Exec(ctx)
	if err != nil {
		return fmt.Errorf("mongo: insert logs failed: %w", err)
	}

	return nil
}

func (s *Store) QueryLogs(ctx context.Context, q telemetry.LogQuery) ([]telemetry.LogEntry, error) {
	var models []logEntryModel

	f := bson.M{}
	if q.InstanceID.String() != "" {
		f["instance_id"] = q.InstanceID.String()
	}

	if q.Level != "" {
		f["level"] = q.Level
	}

	if !q.Since.IsZero() {
		f["timestamp"] = bson.M{"$gte": q.Since}
	}

	if !q.Until.IsZero() {
		if existing, ok := f["timestamp"]; ok {
			existing.(bson.M)["$lte"] = q.Until
		} else {
			f["timestamp"] = bson.M{"$lte": q.Until}
		}
	}

	query := s.mdb.NewFind(&models).
		Filter(f).
		Sort(bson.D{{Key: "timestamp", Value: -1}})

	if q.Limit > 0 {
		query = query.Limit(int64(q.Limit))
	}

	if err := query.Scan(ctx); err != nil {
		return nil, fmt.Errorf("mongo: query logs failed: %w", err)
	}

	items := make([]telemetry.LogEntry, 0, len(models))
	for _, model := range models {
		items = append(items, fromLogEntryModel(&model))
	}

	return items, nil
}

func (s *Store) InsertTraces(ctx context.Context, traces []telemetry.Trace) error {
	if len(traces) == 0 {
		return nil
	}

	models := make([]*traceModel, 0, len(traces))
	for i := range traces {
		models = append(models, toTraceModel(&traces[i]))
	}

	_, err := s.mdb.NewInsert(&models).Exec(ctx)
	if err != nil {
		return fmt.Errorf("mongo: insert traces failed: %w", err)
	}

	return nil
}

func (s *Store) QueryTraces(ctx context.Context, q telemetry.TraceQuery) ([]telemetry.Trace, error) {
	var models []traceModel

	f := bson.M{}
	if q.InstanceID.String() != "" {
		f["instance_id"] = q.InstanceID.String()
	}

	if q.TraceID != "" {
		f["trace_id"] = q.TraceID
	}

	if q.Operation != "" {
		f["operation"] = q.Operation
	}

	if !q.Since.IsZero() {
		f["timestamp"] = bson.M{"$gte": q.Since}
	}

	if !q.Until.IsZero() {
		if existing, ok := f["timestamp"]; ok {
			existing.(bson.M)["$lte"] = q.Until
		} else {
			f["timestamp"] = bson.M{"$lte": q.Until}
		}
	}

	query := s.mdb.NewFind(&models).
		Filter(f).
		Sort(bson.D{{Key: "timestamp", Value: -1}})

	if q.Limit > 0 {
		query = query.Limit(int64(q.Limit))
	}

	if err := query.Scan(ctx); err != nil {
		return nil, fmt.Errorf("mongo: query traces failed: %w", err)
	}

	items := make([]telemetry.Trace, 0, len(models))
	for _, model := range models {
		items = append(items, fromTraceModel(&model))
	}

	return items, nil
}

func (s *Store) InsertResourceSnapshot(ctx context.Context, snap *telemetry.ResourceSnapshot) error {
	model := toResourceSnapshotModel(snap)

	_, err := s.mdb.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("mongo: insert resource snapshot failed: %w", err)
	}

	return nil
}

func (s *Store) GetLatestResourceSnapshot(ctx context.Context, tenantID string, instanceID id.ID) (*telemetry.ResourceSnapshot, error) {
	var model resourceSnapshotModel

	err := s.mdb.NewFind(&model).
		Filter(bson.M{"tenant_id": tenantID, "instance_id": instanceID.String()}).
		Sort(bson.D{{Key: "timestamp", Value: -1}}).
		Limit(1).
		Scan(ctx)
	if err != nil {
		if isNoDocuments(err) {
			return nil, fmt.Errorf("%w: no snapshots for instance %s", ctrlplane.ErrNotFound, instanceID)
		}

		return nil, fmt.Errorf("mongo: get latest resource snapshot failed: %w", err)
	}

	snap := fromResourceSnapshotModel(&model)

	return &snap, nil
}

func (s *Store) ListResourceSnapshots(ctx context.Context, tenantID string, instanceID id.ID, opts telemetry.TimeRange) ([]telemetry.ResourceSnapshot, error) {
	var models []resourceSnapshotModel

	f := bson.M{"tenant_id": tenantID, "instance_id": instanceID.String()}

	if !opts.Since.IsZero() {
		f["timestamp"] = bson.M{"$gte": opts.Since}
	}

	if !opts.Until.IsZero() {
		if existing, ok := f["timestamp"]; ok {
			existing.(bson.M)["$lte"] = opts.Until
		} else {
			f["timestamp"] = bson.M{"$lte": opts.Until}
		}
	}

	err := s.mdb.NewFind(&models).
		Filter(f).
		Sort(bson.D{{Key: "timestamp", Value: -1}}).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("mongo: list resource snapshots failed: %w", err)
	}

	items := make([]telemetry.ResourceSnapshot, 0, len(models))
	for _, model := range models {
		items = append(items, fromResourceSnapshotModel(&model))
	}

	return items, nil
}

package mongo

import (
	"context"
	"errors"
	"fmt"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/telemetry"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// InsertMetrics persists metric data points.
func (s *Store) InsertMetrics(ctx context.Context, metrics []telemetry.Metric) error {
	if len(metrics) == 0 {
		return nil
	}

	docs := make([]any, 0, len(metrics))

	for i := range metrics {
		docs = append(docs, toMetricModel(&metrics[i]))
	}

	_, err := s.col(colMetrics).InsertMany(ctx, docs)
	if err != nil {
		return fmt.Errorf("mongo: insert metrics: %w", err)
	}

	return nil
}

// QueryMetrics returns metrics matching the query parameters.
func (s *Store) QueryMetrics(ctx context.Context, q telemetry.MetricQuery) ([]telemetry.Metric, error) {
	filter := bson.D{
		{Key: "instance_id", Value: idStr(q.InstanceID)},
	}

	if q.Name != "" {
		filter = append(filter, bson.E{Key: "name", Value: q.Name})
	}

	if !q.Since.IsZero() {
		filter = append(filter, bson.E{Key: "timestamp", Value: bson.D{{Key: "$gte", Value: q.Since}}})
	}

	if !q.Until.IsZero() {
		filter = append(filter, bson.E{Key: "timestamp", Value: bson.D{{Key: "$lte", Value: q.Until}}})
	}

	findOpts := options.Find().
		SetSort(bson.D{{Key: "timestamp", Value: -1}})

	if q.Limit > 0 {
		findOpts.SetLimit(int64(q.Limit))
	}

	cursor, err := s.col(colMetrics).Find(ctx, filter, findOpts)
	if err != nil {
		return nil, fmt.Errorf("mongo: query metrics: %w", err)
	}
	defer cursor.Close(ctx)

	items := make([]telemetry.Metric, 0)

	for cursor.Next(ctx) {
		var m metricModel

		if err := cursor.Decode(&m); err != nil {
			return nil, fmt.Errorf("mongo: query metrics decode: %w", err)
		}

		items = append(items, fromMetricModel(&m))
	}

	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("mongo: query metrics cursor: %w", err)
	}

	return items, nil
}

// InsertLogs persists log entries.
func (s *Store) InsertLogs(ctx context.Context, logs []telemetry.LogEntry) error {
	if len(logs) == 0 {
		return nil
	}

	docs := make([]any, 0, len(logs))

	for i := range logs {
		docs = append(docs, toLogEntryModel(&logs[i]))
	}

	_, err := s.col(colLogs).InsertMany(ctx, docs)
	if err != nil {
		return fmt.Errorf("mongo: insert logs: %w", err)
	}

	return nil
}

// QueryLogs returns log entries matching the query parameters.
func (s *Store) QueryLogs(ctx context.Context, q telemetry.LogQuery) ([]telemetry.LogEntry, error) {
	filter := bson.D{
		{Key: "instance_id", Value: idStr(q.InstanceID)},
	}

	if q.Level != "" {
		filter = append(filter, bson.E{Key: "level", Value: q.Level})
	}

	if q.Search != "" {
		filter = append(filter, bson.E{Key: "message", Value: bson.D{{Key: "$regex", Value: q.Search}, {Key: "$options", Value: "i"}}})
	}

	if !q.Since.IsZero() {
		filter = append(filter, bson.E{Key: "timestamp", Value: bson.D{{Key: "$gte", Value: q.Since}}})
	}

	if !q.Until.IsZero() {
		filter = append(filter, bson.E{Key: "timestamp", Value: bson.D{{Key: "$lte", Value: q.Until}}})
	}

	findOpts := options.Find().
		SetSort(bson.D{{Key: "timestamp", Value: -1}})

	if q.Limit > 0 {
		findOpts.SetLimit(int64(q.Limit))
	}

	cursor, err := s.col(colLogs).Find(ctx, filter, findOpts)
	if err != nil {
		return nil, fmt.Errorf("mongo: query logs: %w", err)
	}
	defer cursor.Close(ctx)

	items := make([]telemetry.LogEntry, 0)

	for cursor.Next(ctx) {
		var m logEntryModel

		if err := cursor.Decode(&m); err != nil {
			return nil, fmt.Errorf("mongo: query logs decode: %w", err)
		}

		items = append(items, fromLogEntryModel(&m))
	}

	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("mongo: query logs cursor: %w", err)
	}

	return items, nil
}

// InsertTraces persists trace spans.
func (s *Store) InsertTraces(ctx context.Context, traces []telemetry.Trace) error {
	if len(traces) == 0 {
		return nil
	}

	docs := make([]any, 0, len(traces))

	for i := range traces {
		docs = append(docs, toTraceModel(&traces[i]))
	}

	_, err := s.col(colTraces).InsertMany(ctx, docs)
	if err != nil {
		return fmt.Errorf("mongo: insert traces: %w", err)
	}

	return nil
}

// QueryTraces returns traces matching the query parameters.
func (s *Store) QueryTraces(ctx context.Context, q telemetry.TraceQuery) ([]telemetry.Trace, error) {
	filter := bson.D{
		{Key: "instance_id", Value: idStr(q.InstanceID)},
	}

	if q.TraceID != "" {
		filter = append(filter, bson.E{Key: "trace_id", Value: q.TraceID})
	}

	if q.Operation != "" {
		filter = append(filter, bson.E{Key: "operation", Value: q.Operation})
	}

	if !q.Since.IsZero() {
		filter = append(filter, bson.E{Key: "timestamp", Value: bson.D{{Key: "$gte", Value: q.Since}}})
	}

	if !q.Until.IsZero() {
		filter = append(filter, bson.E{Key: "timestamp", Value: bson.D{{Key: "$lte", Value: q.Until}}})
	}

	findOpts := options.Find().
		SetSort(bson.D{{Key: "timestamp", Value: -1}})

	if q.Limit > 0 {
		findOpts.SetLimit(int64(q.Limit))
	}

	cursor, err := s.col(colTraces).Find(ctx, filter, findOpts)
	if err != nil {
		return nil, fmt.Errorf("mongo: query traces: %w", err)
	}
	defer cursor.Close(ctx)

	items := make([]telemetry.Trace, 0)

	for cursor.Next(ctx) {
		var m traceModel

		if err := cursor.Decode(&m); err != nil {
			return nil, fmt.Errorf("mongo: query traces decode: %w", err)
		}

		items = append(items, fromTraceModel(&m))
	}

	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("mongo: query traces cursor: %w", err)
	}

	return items, nil
}

// InsertResourceSnapshot persists a resource snapshot.
func (s *Store) InsertResourceSnapshot(ctx context.Context, snap *telemetry.ResourceSnapshot) error {
	m := toResourceSnapshotModel(snap)

	_, err := s.col(colResourceSnapshots).InsertOne(ctx, m)
	if err != nil {
		return fmt.Errorf("mongo: insert resource snapshot: %w", err)
	}

	return nil
}

// GetLatestResourceSnapshot returns the most recent snapshot for an instance.
func (s *Store) GetLatestResourceSnapshot(ctx context.Context, tenantID string, instanceID id.ID) (*telemetry.ResourceSnapshot, error) {
	filter := bson.D{
		{Key: "tenant_id", Value: tenantID},
		{Key: "instance_id", Value: idStr(instanceID)},
	}

	findOpts := options.FindOne().
		SetSort(bson.D{{Key: "timestamp", Value: -1}})

	var m resourceSnapshotModel

	err := s.col(colResourceSnapshots).FindOne(ctx, filter, findOpts).Decode(&m)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, fmt.Errorf("mongo: get latest resource snapshot: %w: %s", ctrlplane.ErrNotFound, instanceID)
		}

		return nil, fmt.Errorf("mongo: get latest resource snapshot: %w", err)
	}

	result := fromResourceSnapshotModel(&m)

	return &result, nil
}

// ListResourceSnapshots returns snapshots for an instance within a time range.
func (s *Store) ListResourceSnapshots(ctx context.Context, tenantID string, instanceID id.ID, opts telemetry.TimeRange) ([]telemetry.ResourceSnapshot, error) {
	filter := bson.D{
		{Key: "tenant_id", Value: tenantID},
		{Key: "instance_id", Value: idStr(instanceID)},
	}

	if !opts.Since.IsZero() {
		filter = append(filter, bson.E{Key: "timestamp", Value: bson.D{{Key: "$gte", Value: opts.Since}}})
	}

	if !opts.Until.IsZero() {
		filter = append(filter, bson.E{Key: "timestamp", Value: bson.D{{Key: "$lte", Value: opts.Until}}})
	}

	findOpts := options.Find().
		SetSort(bson.D{{Key: "timestamp", Value: -1}})

	cursor, err := s.col(colResourceSnapshots).Find(ctx, filter, findOpts)
	if err != nil {
		return nil, fmt.Errorf("mongo: list resource snapshots: %w", err)
	}
	defer cursor.Close(ctx)

	items := make([]telemetry.ResourceSnapshot, 0)

	for cursor.Next(ctx) {
		var m resourceSnapshotModel

		if err := cursor.Decode(&m); err != nil {
			return nil, fmt.Errorf("mongo: list resource snapshots decode: %w", err)
		}

		items = append(items, fromResourceSnapshotModel(&m))
	}

	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("mongo: list resource snapshots cursor: %w", err)
	}

	return items, nil
}

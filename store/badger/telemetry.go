package badger

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/dgraph-io/badger/v4"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/telemetry"
)

func (s *Store) InsertMetrics(_ context.Context, metrics []telemetry.Metric) error {
	return s.db.Update(func(txn *badger.Txn) error {
		for _, m := range metrics {
			key := fmt.Sprintf("%s%s:%s:%s", prefixMetric, idStr(m.InstanceID), m.Name, m.Timestamp.Format("2006-01-02T15:04:05.999999999Z07:00"))

			if err := s.set(txn, key, m); err != nil {
				return err
			}
		}

		return nil
	})
}

func (s *Store) QueryMetrics(_ context.Context, q telemetry.MetricQuery) ([]telemetry.Metric, error) {
	var items []telemetry.Metric

	err := s.db.View(func(txn *badger.Txn) error {
		prefix := prefixMetric

		if q.InstanceID.String() != "" {
			prefix = prefixMetric + idStr(q.InstanceID) + ":"
		}

		return s.iterate(txn, prefix, func(_ string, val []byte) error {
			var m telemetry.Metric
			if err := json.Unmarshal(val, &m); err != nil {
				return fmt.Errorf("badger: json unmarshal failed: %w", err)
			}

			if q.InstanceID.String() != "" && m.InstanceID != q.InstanceID {
				return nil
			}

			if q.Name != "" && m.Name != q.Name {
				return nil
			}

			if !q.Since.IsZero() && m.Timestamp.Before(q.Since) {
				return nil
			}

			if !q.Until.IsZero() && m.Timestamp.After(q.Until) {
				return nil
			}

			items = append(items, m)

			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	if q.Limit > 0 && len(items) > q.Limit {
		items = items[:q.Limit]
	}

	return items, nil
}

func (s *Store) InsertLogs(_ context.Context, logs []telemetry.LogEntry) error {
	return s.db.Update(func(txn *badger.Txn) error {
		for _, log := range logs {
			key := fmt.Sprintf("%s%s:%s", prefixLog, idStr(log.InstanceID), log.Timestamp.Format("2006-01-02T15:04:05.999999999Z07:00"))

			if err := s.set(txn, key, log); err != nil {
				return err
			}
		}

		return nil
	})
}

func (s *Store) QueryLogs(_ context.Context, q telemetry.LogQuery) ([]telemetry.LogEntry, error) {
	var items []telemetry.LogEntry

	err := s.db.View(func(txn *badger.Txn) error {
		prefix := prefixLog

		if q.InstanceID.String() != "" {
			prefix = prefixLog + idStr(q.InstanceID) + ":"
		}

		return s.iterate(txn, prefix, func(_ string, val []byte) error {
			var log telemetry.LogEntry
			if err := json.Unmarshal(val, &log); err != nil {
				return fmt.Errorf("badger: json unmarshal failed: %w", err)
			}

			if q.InstanceID.String() != "" && log.InstanceID != q.InstanceID {
				return nil
			}

			if q.Level != "" && log.Level != q.Level {
				return nil
			}

			if !q.Since.IsZero() && log.Timestamp.Before(q.Since) {
				return nil
			}

			if !q.Until.IsZero() && log.Timestamp.After(q.Until) {
				return nil
			}

			items = append(items, log)

			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	if q.Limit > 0 && len(items) > q.Limit {
		items = items[:q.Limit]
	}

	return items, nil
}

func (s *Store) InsertTraces(_ context.Context, traces []telemetry.Trace) error {
	return s.db.Update(func(txn *badger.Txn) error {
		for _, trace := range traces {
			key := fmt.Sprintf("%s%s:%s:%s", prefixTrace, idStr(trace.InstanceID), trace.TraceID, trace.Timestamp.Format("2006-01-02T15:04:05.999999999Z07:00"))

			if err := s.set(txn, key, trace); err != nil {
				return err
			}
		}

		return nil
	})
}

func (s *Store) QueryTraces(_ context.Context, q telemetry.TraceQuery) ([]telemetry.Trace, error) {
	var items []telemetry.Trace

	err := s.db.View(func(txn *badger.Txn) error {
		prefix := prefixTrace

		if q.InstanceID.String() != "" {
			prefix = prefixTrace + idStr(q.InstanceID) + ":"
		}

		return s.iterate(txn, prefix, func(_ string, val []byte) error {
			var trace telemetry.Trace
			if err := json.Unmarshal(val, &trace); err != nil {
				return fmt.Errorf("badger: json unmarshal failed: %w", err)
			}

			if q.InstanceID.String() != "" && trace.InstanceID != q.InstanceID {
				return nil
			}

			if q.TraceID != "" && trace.TraceID != q.TraceID {
				return nil
			}

			if q.Operation != "" && trace.Operation != q.Operation {
				return nil
			}

			if !q.Since.IsZero() && trace.Timestamp.Before(q.Since) {
				return nil
			}

			if !q.Until.IsZero() && trace.Timestamp.After(q.Until) {
				return nil
			}

			items = append(items, trace)

			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	if q.Limit > 0 && len(items) > q.Limit {
		items = items[:q.Limit]
	}

	return items, nil
}

func (s *Store) InsertResourceSnapshot(_ context.Context, snap *telemetry.ResourceSnapshot) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := fmt.Sprintf("%s%s:%s", prefixResourceSnap, idStr(snap.InstanceID), snap.Timestamp.Format("2006-01-02T15:04:05.999999999Z07:00"))

		return s.set(txn, key, snap)
	})
}

func (s *Store) GetLatestResourceSnapshot(_ context.Context, tenantID string, instanceID id.ID) (*telemetry.ResourceSnapshot, error) {
	var latest *telemetry.ResourceSnapshot

	err := s.db.View(func(txn *badger.Txn) error {
		prefix := prefixResourceSnap + idStr(instanceID) + ":"

		return s.iterate(txn, prefix, func(_ string, val []byte) error {
			var snap telemetry.ResourceSnapshot
			if err := json.Unmarshal(val, &snap); err != nil {
				return fmt.Errorf("badger: json unmarshal failed: %w", err)
			}

			if snap.TenantID != tenantID {
				return nil
			}

			if latest == nil || snap.Timestamp.After(latest.Timestamp) {
				latest = &snap
			}

			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	if latest == nil {
		return nil, fmt.Errorf("%w: no snapshots for instance %s", ctrlplane.ErrNotFound, instanceID)
	}

	return latest, nil
}

func (s *Store) ListResourceSnapshots(_ context.Context, tenantID string, instanceID id.ID, opts telemetry.TimeRange) ([]telemetry.ResourceSnapshot, error) {
	var items []telemetry.ResourceSnapshot

	err := s.db.View(func(txn *badger.Txn) error {
		prefix := prefixResourceSnap + idStr(instanceID) + ":"

		return s.iterate(txn, prefix, func(_ string, val []byte) error {
			var snap telemetry.ResourceSnapshot
			if err := json.Unmarshal(val, &snap); err != nil {
				return fmt.Errorf("badger: json unmarshal failed: %w", err)
			}

			if snap.TenantID != tenantID {
				return nil
			}

			if !opts.Since.IsZero() && snap.Timestamp.Before(opts.Since) {
				return nil
			}

			if !opts.Until.IsZero() && snap.Timestamp.After(opts.Until) {
				return nil
			}

			items = append(items, snap)

			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].Timestamp.After(items[j].Timestamp)
	})

	return items, nil
}

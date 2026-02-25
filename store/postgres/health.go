package postgres

import (
	"context"
	"fmt"
	"time"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/health"
	"github.com/xraph/ctrlplane/id"
)

func (s *Store) InsertCheck(ctx context.Context, check *health.HealthCheck) error {
	model := toHealthCheckModel(check)

	_, err := s.pg.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: insert health check failed: %w", err)
	}

	return nil
}

func (s *Store) GetCheck(ctx context.Context, tenantID string, checkID id.ID) (*health.HealthCheck, error) {
	var model healthCheckModel

	err := s.pg.NewSelect(&model).
		Where("id = $1 AND tenant_id = $2", checkID.String(), tenantID).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf("%w: health check %s", ctrlplane.ErrNotFound, checkID)
		}

		return nil, fmt.Errorf("postgres: get health check failed: %w", err)
	}

	check := &health.HealthCheck{
		Entity: ctrlplane.Entity{
			ID:        id.MustParse(model.ID),
			CreatedAt: model.CreatedAt,
			UpdatedAt: model.UpdatedAt,
		},
		TenantID:   model.TenantID,
		InstanceID: id.MustParse(model.InstanceID),
		Name:       model.Name,
		Type:       health.CheckType(model.Type),
		Enabled:    model.Enabled,
		Interval:   time.Duration(model.Interval),
		Timeout:    time.Duration(model.Timeout),
	}

	return check, nil
}

func (s *Store) ListChecks(ctx context.Context, tenantID string, instanceID id.ID) ([]health.HealthCheck, error) {
	var models []healthCheckModel

	err := s.pg.NewSelect(&models).
		Where("tenant_id = $1 AND instance_id = $2", tenantID, instanceID.String()).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: list health checks failed: %w", err)
	}

	items := make([]health.HealthCheck, 0, len(models))
	for _, model := range models {
		check := health.HealthCheck{
			Entity: ctrlplane.Entity{
				ID:        id.MustParse(model.ID),
				CreatedAt: model.CreatedAt,
				UpdatedAt: model.UpdatedAt,
			},
			TenantID:   model.TenantID,
			InstanceID: id.MustParse(model.InstanceID),
			Name:       model.Name,
			Type:       health.CheckType(model.Type),
			Enabled:    model.Enabled,
			Interval:   time.Duration(model.Interval),
			Timeout:    time.Duration(model.Timeout),
		}
		items = append(items, check)
	}

	return items, nil
}

func (s *Store) UpdateCheck(ctx context.Context, check *health.HealthCheck) error {
	check.UpdatedAt = now()
	model := toHealthCheckModel(check)

	res, err := s.pg.NewUpdate(model).WherePK().Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: update health check failed: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("postgres: rows affected check failed: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("%w: health check %s", ctrlplane.ErrNotFound, check.ID)
	}

	return nil
}

func (s *Store) DeleteCheck(ctx context.Context, tenantID string, checkID id.ID) error {
	res, err := s.pg.NewDelete((*healthCheckModel)(nil)).
		Where("id = $1 AND tenant_id = $2", checkID.String(), tenantID).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: delete health check failed: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("postgres: rows affected check failed: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("%w: health check %s", ctrlplane.ErrNotFound, checkID)
	}

	return nil
}

func (s *Store) InsertResult(ctx context.Context, result *health.HealthResult) error {
	model := toHealthResultModel(result)

	_, err := s.pg.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: insert health result failed: %w", err)
	}

	return nil
}

func (s *Store) ListResults(ctx context.Context, tenantID string, checkID id.ID, opts health.HistoryOptions) ([]health.HealthResult, error) {
	var models []healthResultModel

	q := s.pg.NewSelect(&models).
		Where("check_id = $1", checkID.String())

	argIdx := 1
	if !opts.Since.IsZero() {
		argIdx++
		q = q.Where(fmt.Sprintf("checked_at >= $%d", argIdx), opts.Since)
	}

	if !opts.Until.IsZero() {
		argIdx++
		q = q.Where(fmt.Sprintf("checked_at <= $%d", argIdx), opts.Until)
	}

	q = q.OrderExpr("checked_at DESC")

	if opts.Limit > 0 {
		q = q.Limit(opts.Limit)
	}

	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("postgres: list health results failed: %w", err)
	}

	items := make([]health.HealthResult, 0, len(models))
	for _, model := range models {
		result := health.HealthResult{
			CheckID:    id.MustParse(model.CheckID),
			InstanceID: id.MustParse(model.InstanceID),
			TenantID:   model.TenantID,
			Status:     health.Status(model.Status),
			Latency:    time.Duration(model.Latency),
			Message:    model.Message,
			StatusCode: model.StatusCode,
			CheckedAt:  model.CheckedAt,
		}
		items = append(items, result)
	}

	return items, nil
}

func (s *Store) GetLatestResult(ctx context.Context, tenantID string, checkID id.ID) (*health.HealthResult, error) {
	var model healthResultModel

	err := s.pg.NewSelect(&model).
		Where("check_id = $1", checkID.String()).
		OrderExpr("checked_at DESC").
		Limit(1).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf("%w: no results for check %s", ctrlplane.ErrNotFound, checkID)
		}

		return nil, fmt.Errorf("postgres: get latest result failed: %w", err)
	}

	result := &health.HealthResult{
		CheckID:    id.MustParse(model.CheckID),
		InstanceID: id.MustParse(model.InstanceID),
		TenantID:   model.TenantID,
		Status:     health.Status(model.Status),
		Latency:    time.Duration(model.Latency),
		Message:    model.Message,
		StatusCode: model.StatusCode,
		CheckedAt:  model.CheckedAt,
	}

	return result, nil
}

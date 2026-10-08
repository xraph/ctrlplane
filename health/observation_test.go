package health

import (
	"context"
	"errors"
	"testing"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/id"
)

type observationStore struct {
	Store

	statuses []Status
	failure  error
}

func (s *observationStore) ListChecks(context.Context, string, id.ID) ([]HealthCheck, error) {
	checks := make([]HealthCheck, 0, len(s.statuses))
	for range s.statuses {
		checks = append(checks, HealthCheck{Entity: ctrlplane.NewEntity(id.PrefixHealthCheck)})
	}

	return checks, nil
}
func (s *observationStore) GetLatestResult(context.Context, string, id.ID) (*HealthResult, error) {
	if s.failure != nil {
		return nil, s.failure
	}

	status := s.statuses[0]
	s.statuses = s.statuses[1:]

	if status == "" {
		return nil, ctrlplane.ErrNotFound
	}

	return &HealthResult{Status: status}, nil
}
func TestHealthDistinguishesMissingObservationAndStoreFailure(t *testing.T) {
	for _, test := range []struct {
		name     string
		statuses []Status
		failure  error
		want     Status
	}{
		{"pending", []Status{"", ""}, nil, StatusUnknown},
		{"observed unknown", []Status{StatusUnknown, StatusUnknown}, nil, StatusUnknown},
		{"healthy", []Status{StatusHealthy, StatusHealthy}, nil, StatusHealthy},
		{"partial", []Status{StatusHealthy, StatusUnknown}, nil, StatusDegraded},
		{"unhealthy", []Status{StatusUnhealthy, StatusUnhealthy}, nil, StatusUnhealthy},
		{"store failure", []Status{StatusUnknown}, ctrlplane.ErrProviderUnavail, StatusUnknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			backing := &observationStore{statuses: test.statuses, failure: test.failure}
			svc := NewService(backing, nil, &auth.NoopProvider{})
			ctx := auth.WithClaims(context.Background(), &auth.Claims{TenantID: "alpha", SubjectID: "operator"})

			got, err := svc.GetHealth(ctx, id.New(id.PrefixInstance))
			if test.failure != nil {
				if !errors.Is(err, test.failure) {
					t.Fatalf("store failure hidden as observation: %v", err)
				}

				return
			}

			if err != nil || got.Status != test.want {
				t.Fatalf("health: %+v %v", got, err)
			}
		})
	}
}

package postgres

import (
	"context"
	"errors"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/workload"
)

// errWorkloadPgUnsupported keeps the postgres backend compiling
// against the new aggregate Store interface while the real
// implementation is in flight. Twinos runs on mongo today; pg gets
// real workload schema + queries as a follow-up commit before any
// pg-backed deployment exercises this surface.
var errWorkloadPgUnsupported = errors.New("postgres: workload store not implemented yet (use mongo backend)")

func (s *Store) InsertWorkload(_ context.Context, _ *workload.Workload) error {
	return errWorkloadPgUnsupported
}

func (s *Store) GetWorkloadByID(_ context.Context, _ string, _ id.ID) (*workload.Workload, error) {
	return nil, errWorkloadPgUnsupported
}

func (s *Store) GetWorkloadBySlug(_ context.Context, _, _ string) (*workload.Workload, error) {
	return nil, errWorkloadPgUnsupported
}

func (s *Store) ListWorkloads(_ context.Context, _ string, _ workload.ListOptions) (*workload.ListResult, error) {
	return nil, errWorkloadPgUnsupported
}

func (s *Store) UpdateWorkload(_ context.Context, _ *workload.Workload) error {
	return errWorkloadPgUnsupported
}

func (s *Store) DeleteWorkload(_ context.Context, _ string, _ id.ID) error {
	return errWorkloadPgUnsupported
}

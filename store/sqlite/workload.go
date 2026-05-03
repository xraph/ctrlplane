package sqlite

import (
	"context"
	"errors"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/workload"
)

// errWorkloadSqliteUnsupported keeps the sqlite backend compiling
// against the new aggregate Store interface; real implementation
// follows once a sqlite-backed deployment needs it.
var errWorkloadSqliteUnsupported = errors.New("sqlite: workload store not implemented yet (use mongo backend)")

func (s *Store) InsertWorkload(_ context.Context, _ *workload.Workload) error {
	return errWorkloadSqliteUnsupported
}

func (s *Store) GetWorkloadByID(_ context.Context, _ string, _ id.ID) (*workload.Workload, error) {
	return nil, errWorkloadSqliteUnsupported
}

func (s *Store) GetWorkloadBySlug(_ context.Context, _, _ string) (*workload.Workload, error) {
	return nil, errWorkloadSqliteUnsupported
}

func (s *Store) ListWorkloads(_ context.Context, _ string, _ workload.ListOptions) (*workload.ListResult, error) {
	return nil, errWorkloadSqliteUnsupported
}

func (s *Store) UpdateWorkload(_ context.Context, _ *workload.Workload) error {
	return errWorkloadSqliteUnsupported
}

func (s *Store) DeleteWorkload(_ context.Context, _ string, _ id.ID) error {
	return errWorkloadSqliteUnsupported
}

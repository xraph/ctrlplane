package badger

import (
	"context"
	"errors"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/workload"
)

// errWorkloadBadgerUnsupported keeps the badger backend compiling
// against the new aggregate Store interface; real implementation
// follows once a badger-backed deployment needs it.
var errWorkloadBadgerUnsupported = errors.New("badger: workload store not implemented yet (use mongo backend)")

func (s *Store) InsertWorkload(_ context.Context, _ *workload.Workload) error {
	return errWorkloadBadgerUnsupported
}

func (s *Store) GetWorkloadByID(_ context.Context, _ string, _ id.ID) (*workload.Workload, error) {
	return nil, errWorkloadBadgerUnsupported
}

func (s *Store) GetWorkloadBySlug(_ context.Context, _, _ string) (*workload.Workload, error) {
	return nil, errWorkloadBadgerUnsupported
}

func (s *Store) ListWorkloads(_ context.Context, _ string, _ workload.ListOptions) (*workload.ListResult, error) {
	return nil, errWorkloadBadgerUnsupported
}

func (s *Store) UpdateWorkload(_ context.Context, _ *workload.Workload) error {
	return errWorkloadBadgerUnsupported
}

func (s *Store) DeleteWorkload(_ context.Context, _ string, _ id.ID) error {
	return errWorkloadBadgerUnsupported
}

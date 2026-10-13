//go:build !linux && !darwin

package docker

import (
	"context"

	"github.com/xraph/ctrlplane/managed"
)

// FileManagedDockerJournal has no adapter on this operating system.
type FileManagedDockerJournal struct{}

// OpenFileManagedDockerJournal refuses unsupported locking/durability semantics.
func OpenFileManagedDockerJournal(_ string) (*FileManagedDockerJournal, error) {
	return nil, managed.ErrUnsupported
}

// Load refuses unsupported custody storage.
func (*FileManagedDockerJournal) Load(_ context.Context, _ string) ([]byte, error) {
	return nil, managed.ErrUnsupported
}

// CompareAndSwap refuses unsupported custody storage.
func (*FileManagedDockerJournal) CompareAndSwap(_ context.Context, _ string, _, _ []byte) (bool, error) {
	return false, managed.ErrUnsupported
}

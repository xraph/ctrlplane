//go:build linux || darwin

package docker

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/xraph/ctrlplane/managed"
)

// FileManagedDockerJournal is a local POSIX custody adapter. Its directory must be
// pre-created, owned by the executor UID, mode 0700, on a filesystem supporting flock,
// atomic same-directory rename and file/directory fsync. It is not an NFS adapter.
// The trusted launcher must protect its storage and quarantine snapshot restore. This
// adapter cannot detect privileged rollback or provide cross-host writer admission.
// There is no deletion API. Closed and delegated rows survive executor restart.
type FileManagedDockerJournal struct{ directory string }

// OpenFileManagedDockerJournal opens an existing protected directory. It neither
// creates deployment storage nor enrolls an executor or enables a managed profile.
func OpenFileManagedDockerJournal(directory string) (*FileManagedDockerJournal, error) {
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}

	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil || resolved != absolute {
		return nil, managed.ErrUnsupported
	}

	info, err := os.Lstat(absolute)
	if err != nil {
		return nil, err
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode().Perm() != 0700 || int64(stat.Uid) != int64(os.Geteuid()) {
		return nil, managed.ErrUnsupported
	}

	return &FileManagedDockerJournal{directory: absolute}, nil
}

func (j *FileManagedDockerJournal) locked(ctx context.Context, key string, action func() error) error {
	if !dockerObjectID(key) {
		return managed.ErrInvalid
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	// The shared lock file is never renamed or removed. Reopen uses the same inode.
	fd, err := syscall.Open(filepath.Join(j.directory, "journal.lock"), syscall.O_RDWR|syscall.O_CREAT|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}

	lock := os.NewFile(uintptr(fd), "journal.lock")
	defer lock.Close()

	info, err := lock.Stat()
	if err != nil {
		return err
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int64(stat.Uid) != int64(os.Geteuid()) || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return managed.ErrUnsupported
	}

	for {
		err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}

		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) && !errors.Is(err, syscall.EINTR) {
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}

	defer syscall.Flock(fd, syscall.LOCK_UN) //nolint:errcheck // Closing the owned descriptor also releases this process lock.

	if err = ctx.Err(); err != nil {
		return err
	}

	return action()
}

func (j *FileManagedDockerJournal) read(key string) ([]byte, error) {
	fd, err := syscall.Open(filepath.Join(j.directory, key+".json"), syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if errors.Is(err, syscall.ENOENT) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	file := os.NewFile(uintptr(fd), key)
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int64(stat.Uid) != int64(os.Geteuid()) || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() == 0 || info.Size() > ManagedDockerJournalLimit {
		return nil, managed.ErrInvalid
	}

	raw, err := io.ReadAll(io.LimitReader(file, ManagedDockerJournalLimit+1))
	if err != nil {
		return nil, err
	}

	if len(raw) > ManagedDockerJournalLimit {
		return nil, managed.ErrCapacity
	}

	return raw, nil
}

// Load returns an owned bounded snapshot under the same process-shared lock as CAS.
func (j *FileManagedDockerJournal) Load(ctx context.Context, key string) ([]byte, error) {
	var raw []byte

	err := j.locked(ctx, key, func() error {
		var readErr error

		raw, readErr = j.read(key)

		return readErr
	})

	return raw, err
}

// CompareAndSwap syncs bytes, atomically renames, and syncs the directory before success.
// Cancellation or I/O error after rename may leave the transition retained. The caller
// must recover by reading and must not infer that a failed claim permits a send.
func (j *FileManagedDockerJournal) CompareAndSwap(ctx context.Context, key string, expected, next []byte) (bool, error) {
	expected = bytes.Clone(expected)

	next = bytes.Clone(next)
	if len(expected) > ManagedDockerJournalLimit || len(next) == 0 || len(next) > ManagedDockerJournalLimit {
		return false, managed.ErrCapacity
	}

	changed := false
	err := j.locked(ctx, key, func() error {
		current, err := j.read(key)
		if err != nil {
			return err
		}

		if !bytes.Equal(current, expected) {
			return nil
		}

		temp, err := os.CreateTemp(j.directory, "pending-")
		if err != nil {
			return err
		}

		tempName := temp.Name()
		defer os.Remove(tempName)
		defer temp.Close()

		if _, err = temp.Write(next); err != nil {
			return err
		}

		if err = temp.Sync(); err != nil {
			return err
		}

		if err = temp.Close(); err != nil {
			return err
		}

		if err = ctx.Err(); err != nil {
			return err
		}

		if err = os.Rename(tempName, filepath.Join(j.directory, key+".json")); err != nil {
			return err
		}

		directory, err := os.Open(j.directory)
		if err != nil {
			return err
		}

		syncErr := directory.Sync()
		closeErr := directory.Close()

		if syncErr != nil {
			return syncErr
		}

		if closeErr != nil {
			return closeErr
		}

		if err = ctx.Err(); err != nil {
			return err
		}

		changed = true

		return nil
	})

	return changed, err
}

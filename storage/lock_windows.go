//go:build windows

package storage

import (
	"errors"
	"os"
)

// lockFile takes an exclusive lock on the database.
//
// Windows has no flock. LockFileEx is the equivalent and is reached through
// the kernel32 DLL rather than syscall, which does not expose it. The handle
// must stay open for the lock to hold, so the *os.File is returned and the
// caller keeps it for the lifetime of the process.
//
// The whole file is locked rather than a byte range: the database is the only
// thing this process guards, and a range lock on a file another process also
// opens whole would not exclude it.
func lockFile(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := lockExclusive(file); err != nil {
		file.Close()
		return nil, errors.New("another harness process owns this database")
	}
	return file, nil
}

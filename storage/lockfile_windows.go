//go:build windows

package storage

import (
	"os"

	"golang.org/x/sys/windows"
)

// LOCKFILE_EXCLUSIVE_LOCK takes the lock for this process alone, and
// LOCKFILE_FAIL_IMMEDIATELY turns a contended lock into an error instead of a
// wait: the harness refuses to share a database, and blocking would hang the
// second process with no way to tell what it is waiting for.
const (
	lockfileExclusiveLock   = windows.LOCKFILE_EXCLUSIVE_LOCK
	lockfileFailImmediately = windows.LOCKFILE_FAIL_IMMEDIATELY
	// wholeFile is the length of the range to lock, given twice because the
	// API splits it into a low and a high dword. Both all-ones is the whole
	// file from any offset.
	wholeFile = ^uint32(0)
)

// lockExclusive takes a whole-file exclusive lock, failing at once if held.
//
// The range is named by the overlapped structure, which for a synchronous lock
// is consulted only for its offset, and the length is the pair of arguments
// before it. Getting either wrong does not fail loudly: a lock on the wrong
// region succeeds, and the second process is then allowed in.
func lockExclusive(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.LockFileEx(
		windows.Handle(file.Fd()),
		lockfileExclusiveLock|lockfileFailImmediately,
		0,
		wholeFile, wholeFile,
		&overlapped,
	)
}

// unlockExclusive releases a lock taken by lockExclusive. It runs from the
// deferred close path, so a failure here is not actionable.
func unlockExclusive(file *os.File) {
	var overlapped windows.Overlapped
	windows.UnlockFileEx(windows.Handle(file.Fd()), 0, wholeFile, wholeFile, &overlapped)
}

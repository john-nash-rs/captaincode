//go:build windows

package storage

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	modkernel32      = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = modkernel32.NewProc("LockFileEx")
	procUnlockFileEx = modkernel32.NewProc("UnlockFileEx")
)

// lockFileEx wraps LockFileEx, which is the Windows equivalent of flock.
type lockFileEx struct {
	Offset     uint32
	LengthLow  uint32
	LengthHigh uint32
	Reserved   uint32
	Flags      uint32
	Reserved2  uint32
}

// LOCKFILE_EXCLUSIVE_LOCK is 0x2. The FAIL_IMMEDIATELY flag is what turns a
// contended lock into an error instead of a wait: the harness refuses to share
// a database, and blocking would hang the second process with no way to tell
// what it is waiting for.
const (
	lockfileExclusiveLock   = 0x00000002
	lockfileFailImmediately = 0x00000001
	wholeFileLow            = ^uint32(0)
	wholeFileHigh           = ^uint32(0)
)

// lockExclusive takes a whole-file exclusive lock, failing at once if held.
func lockExclusive(file *os.File) error {
	request := lockFileEx{
		LengthLow:  wholeFileLow,
		LengthHigh: wholeFileHigh,
		Flags:      lockfileExclusiveLock | lockfileFailImmediately,
	}
	result, _, callErr := procLockFileEx.Call(
		file.Fd(),
		uintptr(request.Flags),
		0,
		uintptr(unsafe.Pointer(&request)),
	)
	if result == 0 {
		return callErr
	}
	return nil
}

// unlockExclusive releases a lock taken by lockExclusive. It runs from the
// deferred close path, so a failure here is not actionable.
func unlockExclusive(file *os.File) {
	request := lockFileEx{LengthLow: wholeFileLow, LengthHigh: wholeFileHigh}
	procUnlockFileEx.Call(file.Fd(), 0, uintptr(unsafe.Pointer(&request)))
}

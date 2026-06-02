//go:build windows

package cron

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx = kernel32.NewProc("UnlockFileEx")
)

const (
	_lockfileExclusiveLock   = 0x00000002
	_lockfileFailImmediately = 0x00000001
)

// lockFile uses LockFileEx for non-blocking exclusive lock.
func lockFile(f *os.File) error {
	var overlapped syscall.Overlapped

	ret, _, err := procLockFileEx.Call(
		f.Fd(),
		_lockfileExclusiveLock|_lockfileFailImmediately,
		0,
		0xFFFFFFFF,
		0xFFFFFFFF,
		uintptr(unsafe.Pointer(&overlapped)),
	)
	if ret == 0 {
		return fmt.Errorf("LockFileEx failed: %w", err)
	}
	return nil
}

// unlockFile releases the lock via UnlockFileEx.
func unlockFile(f *os.File) error {
	var overlapped syscall.Overlapped

	ret, _, err := procUnlockFileEx.Call(
		f.Fd(),
		0,
		0xFFFFFFFF,
		0xFFFFFFFF,
		uintptr(unsafe.Pointer(&overlapped)),
	)
	if ret == 0 {
		return fmt.Errorf("UnlockFileEx failed: %w", err)
	}
	return nil
}

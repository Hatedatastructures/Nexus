//go:build windows

package memory

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// isWindows 返回当前是否为 Windows 平台。
func isWindows() bool {
	return true
}

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx = kernel32.NewProc("UnlockFileEx")
)

const (
	_lockfileExclusiveLock = 0x00000002
)

// lockFile 在 Windows 上使用 LockFileEx 获取排他锁。
func lockFile(f *os.File) error {
	var overlapped syscall.Overlapped

	ret, _, err := procLockFileEx.Call(
		f.Fd(),
		_lockfileExclusiveLock,
		0,
		0xFFFFFFFF,
		0xFFFFFFFF,
		uintptr(unsafe.Pointer(&overlapped)),
	)
	if ret == 0 {
		return fmt.Errorf("LockFileEx 失败: %w", err)
	}
	return nil
}

// unlockFile 在 Windows 上使用 UnlockFileEx 释放锁。
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
		return fmt.Errorf("UnlockFileEx 失败: %w", err)
	}
	return nil
}

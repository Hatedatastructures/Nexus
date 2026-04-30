//go:build !windows

package memory

import (
	"os"
	"syscall"
)

// isWindows 返回当前是否为 Windows 平台。
func isWindows() bool {
	return false
}

// lockFile 在 Unix 上使用 fcntl 获取排他锁 (阻塞模式)。
func lockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
}

// unlockFile 在 Unix 上使用 fcntl 释放锁。
func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

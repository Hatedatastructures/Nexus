//go:build !windows

package memory

import (
	"fmt"
	"os"
	"syscall"
	"time"
)

const lockTimeout = 30 * time.Second

// isWindows 返回当前是否为 Windows 平台。
func isWindows() bool {
	return false
}

// lockFile 在 Unix 上使用 fcntl 获取排他锁 (带超时的非阻塞模式)。
func lockFile(f *os.File) error {
	deadline := time.Now().Add(lockTimeout)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if err != syscall.EWOULDBLOCK {
			return fmt.Errorf("获取文件锁失败: %w", err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("获取文件锁超时 (%v)", lockTimeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// unlockFile 在 Unix 上使用 fcntl 释放锁。
func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

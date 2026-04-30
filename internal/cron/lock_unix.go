//go:build !windows

package cron

import (
	"os"
	"syscall"
)

// lockFile 在 Unix 上使用 fcntl 获取非阻塞排他锁。
// 返回 nil 错误表示成功获取锁。
func lockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

// unlockFile 在 Unix 上使用 fcntl 释放锁。
func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

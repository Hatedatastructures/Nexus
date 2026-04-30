//go:build windows

package cron

import "os"

// lockFile 在 Windows 上通过尝试对已打开的文件使用互斥方式加锁。
// Windows 不支持 fcntl，简化处理：文件已通过 O_EXCL 模式打开。
func lockFile(f *os.File) error {
	return nil
}

// unlockFile 在 Windows 上关闭文件句柄即释放锁。
func unlockFile(f *os.File) error {
	return nil
}

//go:build windows

package memory

import "os"

// isWindows 返回当前是否为 Windows 平台。
func isWindows() bool {
	return true
}

// lockFile 在 Windows 上已打开的文件句柄不需要额外锁操作。
func lockFile(f *os.File) error {
	return nil
}

// unlockFile 在 Windows 上关闭文件句柄即释放锁。
func unlockFile(f *os.File) error {
	return nil
}

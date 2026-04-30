// Package sandbox 提供 Windows 平台的进程属性设置。
package sandbox

import "os/exec"

// setPlatformProcAttr 在 Windows 上为默认空操作。
// Windows 的 SysProcAttr 使用不同的字段集合。
func setPlatformProcAttr(cmd *exec.Cmd) {
	// Windows 不需要特殊的进程组设置
}

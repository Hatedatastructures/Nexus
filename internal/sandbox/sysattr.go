// Package sandbox 提供平台相关的进程属性设置。
package sandbox

import "os/exec"

// setSysProcAttr 设置平台特定的进程属性。
// Unix: 设置 Setpgid 以便优雅终止整个进程树。
// Windows: 不需要特殊设置。
func setSysProcAttr(cmd *exec.Cmd) {
	setPlatformProcAttr(cmd)
}

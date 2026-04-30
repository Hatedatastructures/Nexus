//go:build unix || darwin

package sandbox

import (
	"os/exec"
	"syscall"
)

// setPlatformProcAttr 设置 Unix 平台的进程属性。
// Setpgid 让子进程创建独立的进程组，以便可以优雅终止整个进程树。
func setPlatformProcAttr(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

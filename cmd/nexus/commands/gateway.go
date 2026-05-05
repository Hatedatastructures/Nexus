package commands

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"nexus-agent/internal/config"
)

// GatewayCommand 实现 nexus gateway 命令。
type GatewayCommand struct{}

func (c *GatewayCommand) Name() string    { return "gateway" }
func (c *GatewayCommand) Synopsis() string { return "网关服务管理 (run/start/stop/restart/status)" }

func (c *GatewayCommand) Run(args []string) {
	if len(args) == 0 {
		c.printUsage()
		return
	}

	switch args[0] {
	case "run":
		c.runGateway()
	case "start":
		c.startGateway()
	case "stop":
		c.stopGateway()
	case "restart":
		c.restartGateway()
	case "status":
		c.statusGateway()
	default:
		PrintError("未知子命令: %s", args[0])
	}
}

func (c *GatewayCommand) printUsage() {
	fmt.Println("用法: nexus gateway <subcommand>")
	fmt.Println("可用子命令:")
	fmt.Println("  run       - 前台运行网关")
	fmt.Println("  start     - 后台启动网关")
	fmt.Println("  stop      - 停止网关")
	fmt.Println("  restart   - 重启网关")
	fmt.Println("  status    - 显示网关状态")
}

func (c *GatewayCommand) runGateway() {
	cfg, err := config.Load("")
	if err != nil {
		PrintError("加载配置失败: %v", err)
	}

	if !cfg.Gateway.Enabled {
		PrintError("网关未启用，请在 config.yaml 中设置 gateway.enabled: true")
	}

	fmt.Println(GreenBold.Render("  启动网关..."))
	fmt.Println()

	// 使用 nexus-gateway 可执行文件
	execPath, err := exec.LookPath("nexus-gateway")
	if err != nil {
		// 尝试在当前目录查找
		execPath = "./nexus-gateway"
		if _, err := os.Stat(execPath); err != nil {
			PrintError("未找到 nexus-gateway 可执行文件")
		}
	}

	cmd := exec.Command(execPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Run(); err != nil {
		PrintError("网关运行失败: %v", err)
	}
}

func (c *GatewayCommand) startGateway() {
	cfg, err := config.Load("")
	if err != nil {
		PrintError("加载配置失败: %v", err)
	}

	if !cfg.Gateway.Enabled {
		PrintError("网关未启用，请在 config.yaml 中设置 gateway.enabled: true")
	}

	// 检查是否已运行
	pidFile := c.getPIDFile()
	if pid, err := c.readPID(pidFile); err == nil {
		if c.isProcessRunning(pid) {
			PrintError("网关已在运行 (PID: %d)", pid)
		}
	}

	// 查找 nexus-gateway
	execPath, err := exec.LookPath("nexus-gateway")
	if err != nil {
		execPath = "./nexus-gateway"
		if _, err := os.Stat(execPath); err != nil {
			PrintError("未找到 nexus-gateway 可执行文件")
		}
	}

	// 后台启动
	cmd := exec.Command(execPath)
	cmd.Stdout = nil
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		PrintError("启动网关失败: %v", err)
	}

	// 写入 PID 文件
	if err := os.MkdirAll(GetNexusHome(), 0755); err != nil {
		PrintError("创建目录失败: %v", err)
	}

	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0644); err != nil {
		PrintError("写入 PID 文件失败: %v", err)
	}

	fmt.Printf("  %s\n", GreenBold.Render("网关已启动"))
	fmt.Printf("  PID: %d\n", cmd.Process.Pid)
	fmt.Println()
}

func (c *GatewayCommand) stopGateway() {
	pidFile := c.getPIDFile()
	pid, err := c.readPID(pidFile)
	if err != nil {
		PrintError("未找到 PID 文件，网关可能未运行")
	}

	if !c.isProcessRunning(pid) {
		fmt.Println(DimStyle.Render("  网关未运行"))
		os.Remove(pidFile)
		return
	}

	// 发送 SIGTERM
	process, err := os.FindProcess(pid)
	if err != nil {
		PrintError("找不到进程: %v", err)
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		PrintError("发送停止信号失败: %v", err)
	}

	os.Remove(pidFile)
	fmt.Println(GreenBold.Render("  网关已停止"))
}

func (c *GatewayCommand) restartGateway() {
	c.stopGateway()
	c.startGateway()
}

func (c *GatewayCommand) statusGateway() {
	pidFile := c.getPIDFile()
	pid, err := c.readPID(pidFile)

	fmt.Println(TitleStyle.Render("网关状态"))
	fmt.Println(strings.Repeat("━", 60))

	if err != nil {
		fmt.Printf("  %s\n", DimStyle.Render("○ 未运行"))
		fmt.Println()
		return
	}

	if c.isProcessRunning(pid) {
		fmt.Printf("  %s (PID: %d)\n", GreenBold.Render("● 运行中"), pid)
	} else {
		fmt.Printf("  %s (PID: %d)\n", DimStyle.Render("○ 已停止"), pid)
		os.Remove(pidFile)
	}

	// 显示配置的平台
	cfg, configErr := config.Load("")
	if configErr == nil && cfg.Gateway.Enabled {
		fmt.Println()
		fmt.Println(GreenBold.Render("  已启用的平台:"))
		for _, p := range cfg.Gateway.Platforms {
			fmt.Printf("    • %s\n", p.Platform)
		}
	}
	fmt.Println()
}

func (c *GatewayCommand) getPIDFile() string {
	return GetNexusHome() + "/gateway.pid"
}

func (c *GatewayCommand) readPID(pidFile string) (int, error) {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, err
	}
	return pid, nil
}

func (c *GatewayCommand) isProcessRunning(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// 在 Unix 上，FindProcess 总是成功，需要发送信号 0 检查
	err = process.Signal(syscall.Signal(0))
	return err == nil
}

func init() {
	Register(&GatewayCommand{})
}

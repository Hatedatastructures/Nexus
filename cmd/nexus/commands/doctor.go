package commands

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"nexus-agent/internal/config"
)

// DoctorCommand 实现 nexus doctor 命令。
type DoctorCommand struct{}

func (c *DoctorCommand) Name() string    { return "doctor" }
func (c *DoctorCommand) Synopsis() string { return "系统诊断检查" }

func (c *DoctorCommand) Run(args []string) {
	PrintTitle("Nexus 系统诊断")
	fmt.Println()

	allOK := true

	// Go 版本检查
	allOK = c.checkGoVersion() && allOK

	// Chrome/Chromium 检查
	allOK = c.checkChrome() && allOK

	// 配置文件检查
	allOK = c.checkConfig() && allOK

	// API Key 检查
	allOK = c.checkAPIKeys() && allOK

	// 网络连通性检查
	allOK = c.checkNetwork() && allOK

	// 数据库检查
	allOK = c.checkDatabase() && allOK

	// 磁盘空间检查
	allOK = c.checkDisk() && allOK

	fmt.Println()
	if allOK {
		PrintSuccess("所有检查通过")
	} else {
		fmt.Println(ErrorStyle.Render("  ✖ 存在问题，请查看上方详情"))
	}
	fmt.Println()
}

func (c *DoctorCommand) checkGoVersion() bool {
	fmt.Print("  Go 版本: ")
	if _, err := exec.LookPath("go"); err != nil {
		fmt.Println(DimStyle.Render("未安装 (非必需)"))
		return true
	}
	fmt.Println(GreenBold.Render(runtime.Version()))
	return true
}

func (c *DoctorCommand) checkChrome() bool {
	fmt.Print("  Chrome/Chromium: ")

	// 检查常见路径
	paths := []string{
		"chrome", "chromium", "google-chrome", "google-chrome-stable",
		"/usr/bin/google-chrome", "/usr/bin/chromium",
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	}

	for _, p := range paths {
		if _, err := exec.LookPath(p); err == nil {
			fmt.Println(GreenBold.Render("已找到"))
			return true
		}
	}

	// Windows 特殊检查
	if runtime.GOOS == "windows" {
		paths := []string{
			os.Getenv("PROGRAMFILES") + "\\Google\\Chrome\\Application\\chrome.exe",
			os.Getenv("PROGRAMFILES(X86)") + "\\Google\\Chrome\\Application\\chrome.exe",
			os.Getenv("LOCALAPPDATA") + "\\Google\\Chrome\\Application\\chrome.exe",
		}
		for _, p := range paths {
			if FileExists(p) {
				fmt.Println(GreenBold.Render("已找到"))
				return true
			}
		}
	}

	fmt.Println(DimStyle.Render("未找到 (浏览器工具将不可用)"))
	return true
}

func (c *DoctorCommand) checkConfig() bool {
	fmt.Print("  配置文件: ")
	cfgPath := GetConfigPath()
	if FileExists(cfgPath) {
		fmt.Println(GreenBold.Render("存在"))
		return true
	}
	fmt.Println(ErrorStyle.Render("不存在"))
	fmt.Println("    提示: 运行 nexus setup 创建配置文件")
	return false
}

func (c *DoctorCommand) checkAPIKeys() bool {
	fmt.Print("  API Key: ")
	cfg, err := config.Load("")
	if err != nil {
		fmt.Println(ErrorStyle.Render("无法加载配置"))
		return false
	}

	if len(cfg.Providers) == 0 {
		fmt.Println(ErrorStyle.Render("未配置任何提供者"))
		return false
	}

	hasValid := false
	for _, p := range cfg.Providers {
		if p.APIKey != "" {
			hasValid = true
			break
		}
	}

	if hasValid {
		fmt.Println(GreenBold.Render("已配置"))
	} else {
		fmt.Println(ErrorStyle.Render("未配置"))
	}
	return hasValid
}

func (c *DoctorCommand) checkNetwork() bool {
	fmt.Print("  网络连通性: ")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.anthropic.com", nil)
	if err != nil {
		fmt.Println(ErrorStyle.Render("创建请求失败"))
		return false
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Println(DimStyle.Render("无法连接 (可能是网络问题或代理)"))
		return true // 非致命
	}
	defer resp.Body.Close()

	fmt.Println(GreenBold.Render("正常"))
	return true
}

func (c *DoctorCommand) checkDatabase() bool {
	fmt.Print("  数据库: ")
	dbPath := GetDBPath()
	if FileExists(dbPath) {
		info, err := os.Stat(dbPath)
		if err == nil {
			sizeMB := float64(info.Size()) / 1024 / 1024
			fmt.Printf("%s (%.1f MB)\n", GreenBold.Render("正常"), sizeMB)
		} else {
			fmt.Println(GreenBold.Render("存在"))
		}
		return true
	}
	fmt.Println(DimStyle.Render("不存在 (首次运行时自动创建)"))
	return true
}

func (c *DoctorCommand) checkDisk() bool {
	fmt.Print("  磁盘空间: ")
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Println(DimStyle.Render("无法检测"))
		return true
	}

	// 简单检查: 尝试写入临时文件
	testFile := home + "/.nexus/.disk_test"
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		fmt.Println(ErrorStyle.Render("无法写入 ~/.nexus/"))
		return false
	}
	os.Remove(testFile)

	fmt.Println(GreenBold.Render("正常"))
	return true
}

func init() {
	Register(&DoctorCommand{})
}

package commands

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LogsCommand 实现 nexus logs 命令。
type LogsCommand struct{}

func (c *LogsCommand) Name() string    { return "logs" }
func (c *LogsCommand) Synopsis() string { return "查看日志" }

func (c *LogsCommand) Run(args []string) {
	logsDir := GetLogsDir()

	// 解析参数
	tail := 50
	follow := false
	level := ""

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--tail", "-n":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &tail)
				i++
			}
		case "--follow", "-f":
			follow = true
		case "--level":
			if i+1 < len(args) {
				level = args[i+1]
				i++
			}
		}
	}

	// 查找日志文件
	logFiles, err := filepath.Glob(logsDir + "/*.log")
	if err != nil || len(logFiles) == 0 {
		fmt.Println(DimStyle.Render("  未找到日志文件"))
		fmt.Printf("  日志目录: %s\n", logsDir)
		fmt.Println()
		fmt.Println(DimStyle.Render("  提示: 日志会在首次运行时自动创建"))
		return
	}

	// 使用最新的日志文件
	latestFile := logFiles[len(logFiles)-1]
	for _, f := range logFiles {
		info1, _ := os.Stat(f)
		info2, _ := os.Stat(latestFile)
		if info1 != nil && info2 != nil && info1.ModTime().After(info2.ModTime()) {
			latestFile = f
		}
	}

	fmt.Println(TitleStyle.Render("日志查看"))
	fmt.Println(strings.Repeat("━", 60))
	fmt.Printf("  文件: %s\n", filepath.Base(latestFile))
	fmt.Println()

	if follow {
		c.followFile(latestFile, level)
	} else {
		c.showTail(latestFile, tail, level)
	}
}

func (c *LogsCommand) showTail(filePath string, lines int, level string) {
	file, err := os.Open(filePath)
	if err != nil {
		PrintError("无法打开日志文件: %v", err)
		return
	}
	defer file.Close()

	// 读取所有行
	var allLines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if level != "" && !strings.Contains(strings.ToLower(line), strings.ToLower(level)) {
			continue
		}
		allLines = append(allLines, line)
	}

	// 显示最后 N 行
	start := len(allLines) - lines
	if start < 0 {
		start = 0
	}

	for _, line := range allLines[start:] {
		c.printLogLine(line)
	}

	if len(allLines) == 0 {
		fmt.Println(DimStyle.Render("  日志为空"))
	}
}

func (c *LogsCommand) followFile(filePath string, level string) {
	fmt.Println(DimStyle.Render("  实时跟踪日志 (Ctrl+C 退出)"))
	fmt.Println()

	file, err := os.Open(filePath)
	if err != nil {
		PrintError("无法打开日志文件: %v", err)
		return
	}
	defer file.Close()

	// 先显示最后 10 行
	c.showTail(filePath, 10, level)

	// 跳到文件末尾
	file.Seek(0, 2)

	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		line = strings.TrimRight(line, "\n")
		if level != "" && !strings.Contains(strings.ToLower(line), strings.ToLower(level)) {
			continue
		}

		c.printLogLine(line)
	}
}

func (c *LogsCommand) printLogLine(line string) {
	// 根据日志级别着色
	lower := strings.ToLower(line)
	switch {
	case strings.Contains(lower, "error") || strings.Contains(lower, "fatal"):
		fmt.Println(ErrorStyle.Render("  " + line))
	case strings.Contains(lower, "warn"):
		fmt.Println(UserStyle.Render("  " + line))
	case strings.Contains(lower, "info"):
		fmt.Println("  " + line)
	case strings.Contains(lower, "debug"):
		fmt.Println(DimStyle.Render("  " + line))
	default:
		fmt.Println("  " + line)
	}
}

func init() {
	Register(&LogsCommand{})
}

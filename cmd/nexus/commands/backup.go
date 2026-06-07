package commands

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// BackupCommand 实现 nexus backup 命令。
type BackupCommand struct{}

func (c *BackupCommand) Name() string     { return "backup" }
func (c *BackupCommand) Synopsis() string { return "备份配置和数据" }

func (c *BackupCommand) Run(args []string) {
	nexusHome := GetNexusHome()

	// 检查目录是否存在
	if _, err := os.Stat(nexusHome); os.IsNotExist(err) {
		PrintError("~/.nexus 目录不存在")
	}

	// 生成备份文件名
	timestamp := time.Now().Format("20060102_150405")
	backupFile := fmt.Sprintf("nexus_backup_%s.tar.gz", timestamp)

	fmt.Println(TitleStyle.Render("备份 Nexus 数据"))
	fmt.Println()
	fmt.Printf("  源目录: %s\n", nexusHome)
	fmt.Printf("  备份文件: %s\n", backupFile)
	fmt.Println()

	// 创建备份文件
	if err := createTarGz(nexusHome, backupFile); err != nil {
		PrintError("创建备份失败: %v", err)
	}

	// 获取备份文件大小
	info, err := os.Stat(backupFile)
	if err != nil {
		PrintError("获取备份文件信息失败: %v", err)
	}

	sizeMB := float64(info.Size()) / 1024 / 1024
	PrintSuccess(fmt.Sprintf("备份完成 (%.1f MB)", sizeMB))
	fmt.Printf("  文件: %s\n", backupFile)
	fmt.Println()
}

func createTarGz(sourceDir, targetFile string) error {
	// 创建目标文件
	file, err := os.Create(targetFile)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	// 创建 gzip writer
	gw := gzip.NewWriter(file)
	defer func() { _ = gw.Close() }()

	// 创建 tar writer
	tw := tar.NewWriter(gw)
	defer func() { _ = tw.Close() }()

	// 遍历源目录
	return filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// 创建 tar header
		header, err := tar.FileInfoHeader(info, path)
		if err != nil {
			return err
		}

		// 设置相对路径
		relPath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		header.Name = relPath

		// 写入 header
		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		// 如果是文件，写入内容
		if !info.IsDir() {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer func() { _ = file.Close() }()

			if _, err := io.Copy(tw, file); err != nil {
				return err
			}
		}

		return nil
	})
}

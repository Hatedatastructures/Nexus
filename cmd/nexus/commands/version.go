package commands

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// VersionCommand 实现 nexus version 命令。
type VersionCommand struct{}

func (c *VersionCommand) Name() string     { return "version" }
func (c *VersionCommand) Synopsis() string { return "显示版本信息" }

func (c *VersionCommand) Run(args []string) {
	version := getVersion()

	fmt.Println(TitleStyle.Render("Nexus Agent"))
	fmt.Println()
	fmt.Printf("  版本:    %s\n", version)
	fmt.Printf("  Go:      %s\n", runtime.Version())
	fmt.Printf("  OS/Arch: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Println()
}

// getVersion 从构建信息中获取版本号。
func getVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		// 尝试从 vcs 信息获取
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" {
				rev := s.Value
				if len(rev) > 8 {
					rev = rev[:8]
				}
				// 检查是否有 tag
				for _, s2 := range info.Settings {
					if s2.Key == "vcs.tag" && s2.Value != "" {
						return s2.Value
					}
				}
				return rev
			}
		}
		return info.Main.Version
	}
	return "dev"
}

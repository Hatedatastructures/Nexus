package commands

import (
	"fmt"
	"os"
)

// MemoryCommand 实现 nexus memory 命令。
type MemoryCommand struct{}

func (c *MemoryCommand) Name() string    { return "memory" }
func (c *MemoryCommand) Synopsis() string { return "管理记忆" }

func (c *MemoryCommand) Run(args []string) {
	nexusHome := GetNexusHome()

	PrintTitle("持久记忆")

	// 读取 MEMORY.md
	memPath := nexusHome + "/MEMORY.md"
	if data, err := os.ReadFile(memPath); err == nil {
		fmt.Println(string(data))
	} else {
		fmt.Println(DimStyle.Render(fmt.Sprintf("  未找到 MEMORY.md (预期路径: %s)", memPath)))
	}

	// 读取 USER.md
	userPath := nexusHome + "/USER.md"
	if data, err := os.ReadFile(userPath); err == nil {
		fmt.Println()
		fmt.Println(TitleStyle.Render("用户记忆 (USER.md)"))
		fmt.Println()
		fmt.Println(string(data))
	}
}

func init() {
	Register(&MemoryCommand{})
}

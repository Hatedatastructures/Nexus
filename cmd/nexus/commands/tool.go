package commands

import (
	"encoding/json"
	"fmt"

	"nexus-agent/internal/tool"
)

// ToolCommand 实现 nexus tool 命令。
type ToolCommand struct{}

func (c *ToolCommand) Name() string     { return "tool" }
func (c *ToolCommand) Synopsis() string { return "工具管理 (list/info)" }

func (c *ToolCommand) Run(args []string) {
	if len(args) == 0 {
		c.listTools()
		return
	}

	switch args[0] {
	case "list", "ls":
		c.listTools()
	case "info":
		if len(args) < 2 {
			PrintError("用法: nexus tool info <name>")
		}
		c.toolInfo(args[1])
	default:
		PrintError("未知子命令: %s", args[0])
	}
}

func (c *ToolCommand) listTools() {
	registry := tool.NewRegistry()
	tool.RegisterAllTools(registry)

	PrintTitle("已注册的可用工具")

	names := registry.ListTools()

	if len(names) == 0 {
		fmt.Println(DimStyle.Render("  无已注册的工具"))
		return
	}

	// 按工具集分组显示
	toolsetMap := make(map[string][]string)
	for _, name := range names {
		entry := registry.GetEntry(name)
		if entry != nil {
			ts := entry.Tool.Toolset()
			toolsetMap[ts] = append(toolsetMap[ts], name)
		}
	}

	for ts, tools := range toolsetMap {
		tsDef, ok := tool.DefaultToolsets[ts]
		desc := ""
		if ok {
			desc = " - " + tsDef.Description
		}
		fmt.Printf("\n  %s%s\n", GreenBold.Render("["+ts+"]"), desc)
		for _, name := range tools {
			entry := registry.GetEntry(name)
			if entry != nil {
				status := ""
				if entry.Tool.IsAvailable() {
					status = GreenBold.Render("✓")
				} else {
					status = DimStyle.Render("○")
				}
				fmt.Printf("    %s %-25s %s\n", status, name, entry.Tool.Description())
			}
		}
	}

	fmt.Printf("\n  共 %d 个工具\n", len(names))
	fmt.Println()
	fmt.Printf("  %s = 可用  %s = 不可用\n", GreenBold.Render("✓"), DimStyle.Render("○"))
}

func (c *ToolCommand) toolInfo(name string) {
	registry := tool.NewRegistry()
	tool.RegisterAllTools(registry)

	entry := registry.GetEntry(name)
	if entry == nil {
		PrintError("未找到工具: %s", name)
	}

	t := entry.Tool

	PrintTitle(fmt.Sprintf("工具信息: %s", name))

	if t.IsAvailable() {
		fmt.Printf("  %s %s\n", GreenBold.Render("状态:"), GreenBold.Render("可用"))
	} else {
		fmt.Printf("  %s %s\n", ErrorStyle.Render("状态:"), ErrorStyle.Render("不可用"))
	}
	fmt.Printf("  %s %s\n", DimStyle.Render("描述:"), t.Description())
	fmt.Printf("  %s %s\n", DimStyle.Render("工具集:"), t.Toolset())
	fmt.Printf("  %s %s\n", DimStyle.Render("图标:"), t.Emoji())
	fmt.Printf("  %s %d\n", DimStyle.Render("最大结果字符:"), entry.MaxResultChars)

	// 显示 Schema
	schema := t.Schema()
	if schema != nil && schema.Parameters != nil {
		fmt.Println()
		fmt.Println(GreenBold.Render("  参数 Schema:"))
		paramsJSON, err := json.MarshalIndent(schema.Parameters, "    ", "  ")
		if err != nil {
			fmt.Printf("    (无法序列化: %v)\n", err)
		} else {
			fmt.Printf("    %s\n", string(paramsJSON))
		}
	}
}

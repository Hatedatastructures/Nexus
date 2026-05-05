// Package agent 提供技能策展功能。
// 自动管理技能的生命周期：检测过期、归档、重新激活，
// 并通过 LLM 审查生成策展报告。
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ───────────────────────────── 常量 ─────────────────────────────

const (
	curatorInterval    = 7 * 24 * time.Hour // 7 天检查间隔
	curatorMaxSkillAge = 30 * 24 * time.Hour // 30 天未使用视为过期
	curatorArchiveAge  = 90 * 24 * time.Hour // 90 天未使用归档
)

// ───────────────────────────── 数据结构 ─────────────────────────────

// SkillState 技能状态
type SkillState string

const (
	SkillStateActive    SkillState = "active"    // 活跃
	SkillStateStale     SkillState = "stale"     // 过期
	SkillStateArchived  SkillState = "archived"  // 已归档
	SkillStateReactivated SkillState = "reactivated" // 重新激活
)

// CuratorSkill 策展技能信息。
type CuratorSkill struct {
	Name        string     `json:"name"`
	Path        string     `json:"path"`
	State       SkillState `json:"state"`
	LastUsed    time.Time  `json:"last_used"`
	UsageCount  int        `json:"usage_count"`
	Description string     `json:"description"`
}

// CuratorState 策展状态。
type CuratorState struct {
	LastRun   time.Time      `json:"last_run"`
	Skills    []CuratorSkill `json:"skills"`
	RunCount  int            `json:"run_count"`
}

// ReviewResult LLM 审查结果。
type ReviewResult struct {
	Summary      string            `json:"summary"`
	KeepSkills   []string          `json:"keep_skills"`
	RemoveSkills []string          `json:"remove_skills"`
	NewSkills    []string          `json:"new_skills"`
	Actions      []string          `json:"actions"`
}

// Curator 技能策展器。
type Curator struct {
	stateFile string
	skillsDir string
	state     CuratorState
}

// NewCurator 创建策展器。
func NewCurator(skillsDir string) *Curator {
	home, _ := os.UserHomeDir()
	return &Curator{
		stateFile: filepath.Join(home, ".nexus", "curator_state.json"),
		skillsDir: skillsDir,
	}
}

// ───────────────────────────── 状态管理 ─────────────────────────────

// LoadState 加载策展状态。
func (c *Curator) LoadState() error {
	data, err := os.ReadFile(c.stateFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return json.Unmarshal(data, &c.state)
}

// SaveState 保存策展状态。
func (c *Curator) SaveState() error {
	dir := filepath.Dir(c.stateFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c.state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.stateFile, data, 0644)
}

// ───────────────────────────── 策展逻辑 ─────────────────────────────

// ShouldRunNow 检查是否应该执行策展。
func (c *Curator) ShouldRunNow() bool {
	if c.state.LastRun.IsZero() {
		return true
	}
	return time.Since(c.state.LastRun) > curatorInterval
}

// ApplyAutomaticTransitions 应用自动状态转换。
// 纯函数，不依赖外部服务。
func (c *Curator) ApplyAutomaticTransitions(skills []CuratorSkill) []CuratorSkill {
	now := time.Now()
	var result []CuratorSkill

	for _, skill := range skills {
		age := now.Sub(skill.LastUsed)

		switch skill.State {
		case SkillStateActive:
			if age > curatorArchiveAge {
				skill.State = SkillStateArchived
				slog.Info("技能已归档", "name", skill.Name, "last_used", skill.LastUsed)
			} else if age > curatorMaxSkillAge {
				skill.State = SkillStateStale
				slog.Info("技能已过期", "name", skill.Name, "last_used", skill.LastUsed)
			}

		case SkillStateStale:
			if age > curatorArchiveAge {
				skill.State = SkillStateArchived
			}

		case SkillStateArchived:
			// 归档技能不会自动恢复

		case SkillStateReactivated:
			skill.State = SkillStateActive
		}

		result = append(result, skill)
	}

	return result
}

// Run 执行策展流程。
func (c *Curator) Run(ctx context.Context) (*ReviewResult, error) {
	if err := c.LoadState(); err != nil {
		return nil, fmt.Errorf("加载策展状态失败: %w", err)
	}

	if !c.ShouldRunNow() {
		return nil, nil
	}

	slog.Info("开始策展", "last_run", c.state.LastRun)

	// 应用自动状态转换
	c.state.Skills = c.ApplyAutomaticTransitions(c.state.Skills)

	// 扫描技能目录
	if err := c.scanSkills(); err != nil {
		return nil, fmt.Errorf("扫描技能目录失败: %w", err)
	}

	// 更新状态
	c.state.LastRun = time.Now()
	c.state.RunCount++

	if err := c.SaveState(); err != nil {
		slog.Warn("保存策展状态失败", "err", err)
	}

	result := &ReviewResult{
		Summary: fmt.Sprintf("策展完成，共 %d 个技能", len(c.state.Skills)),
	}

	slog.Info("策展完成", "skills", len(c.state.Skills), "run_count", c.state.RunCount)
	return result, nil
}

// scanSkills 扫描技能目录。
func (c *Curator) scanSkills() error {
	if c.skillsDir == "" {
		return nil
	}

	existing := make(map[string]bool)
	for _, s := range c.state.Skills {
		existing[s.Name] = true
	}

	return filepath.Walk(c.skillsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.Name() != "SKILL.md" {
			return nil
		}

		dir := filepath.Dir(path)
		name := filepath.Base(dir)

		if !existing[name] {
			c.state.Skills = append(c.state.Skills, CuratorSkill{
				Name:     name,
				Path:     dir,
				State:    SkillStateActive,
				LastUsed: info.ModTime(),
			})
		}

		return nil
	})
}

// WriteRunReport 生成策展报告。
func (c *Curator) WriteRunReport(outputDir string, result *ReviewResult) error {
	reportPath := filepath.Join(outputDir, "curator_report.md")

	var b strings.Builder
	b.WriteString("# 策展报告\n\n")
	b.WriteString(fmt.Sprintf("执行时间: %s\n\n", time.Now().Format(time.RFC3339)))
	b.WriteString(fmt.Sprintf("总技能数: %d\n\n", len(c.state.Skills)))

	// 按状态分组
	byState := make(map[SkillState][]CuratorSkill)
	for _, s := range c.state.Skills {
		byState[s.State] = append(byState[s.State], s)
	}

	for state, skills := range byState {
		b.WriteString(fmt.Sprintf("## %s (%d)\n\n", state, len(skills)))
		for _, s := range skills {
			b.WriteString(fmt.Sprintf("- **%s** — 最后使用: %s\n", s.Name, s.LastUsed.Format("2006-01-02")))
		}
		b.WriteString("\n")
	}

	if result != nil && result.Summary != "" {
		b.WriteString(fmt.Sprintf("## 摘要\n\n%s\n", result.Summary))
	}

	return os.WriteFile(reportPath, []byte(b.String()), 0644)
}

// GetSkillsByState 按状态获取技能列表。
func (c *Curator) GetSkillsByState(state SkillState) []CuratorSkill {
	var result []CuratorSkill
	for _, s := range c.state.Skills {
		if s.State == state {
			result = append(result, s)
		}
	}
	return result
}

// UpdateSkillUsage 更新技能使用记录。
func (c *Curator) UpdateSkillUsage(name string) {
	for i := range c.state.Skills {
		if c.state.Skills[i].Name == name {
			c.state.Skills[i].LastUsed = time.Now()
			c.state.Skills[i].UsageCount++
			if c.state.Skills[i].State == SkillStateStale || c.state.Skills[i].State == SkillStateArchived {
				c.state.Skills[i].State = SkillStateReactivated
			}
			return
		}
	}
}

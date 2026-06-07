// Package environments 提供 Web Research 环境的辅助类型和评估函数。
package environments

import (
	"fmt"
	"time"
)

// ───────────────────────────── 类型定义 ─────────────────────────────

// researchPhase 表示网页研究的阶段。
type researchPhase int

const (
	PhaseInitial       researchPhase = iota // 初始探索阶段
	PhaseDeepDive                           // 深入挖掘阶段
	PhaseCrossValidate                      // 交叉验证阶段
	PhaseSynthesize                         // 综合总结阶段
	PhaseComplete                           // 研究完成
)

// phaseName 返回阶段的名称。
func (p researchPhase) phaseName() string {
	names := map[researchPhase]string{
		PhaseInitial:       "初始探索",
		PhaseDeepDive:      "深入挖掘",
		PhaseCrossValidate: "交叉验证",
		PhaseSynthesize:    "综合总结",
		PhaseComplete:      "研究完成",
	}
	if name, ok := names[p]; ok {
		return name
	}
	return "未知阶段"
}

// ResearchSource 表示一个信息来源。
type ResearchSource struct {
	// URL 来源地址
	URL string
	// Title 来源标题
	Title string
	// Type 来源类型 (article, paper, official, forum, etc.)
	Type string
	// Credibility 可信度评分 (1-10)
	Credibility int
	// Timestamp 收集时间
	Timestamp time.Time
	// Content 内容摘要
	Content string
}

// Finding 表示一项研究发现。
type Finding struct {
	// Topic 发现的主题
	Topic string
	// Summary 摘要
	Summary string
	// Evidence 支撑证据的来源索引
	Evidence []int
	// Confidence 置信度 (0.0-1.0)
	Confidence float64
}

// ───────────────────────────── 质量评估 ─────────────────────────────

// evaluateQuality 评估当前研究质量。
func (w *WebResearchEnvironment) evaluateQuality() {
	score := 0

	// 来源数量评分 (最多 30 分)
	sourceScore := len(w.sources) * 3
	if sourceScore > 30 {
		sourceScore = 30
	}
	score += sourceScore

	// 来源可信度评分 (最多 40 分)
	totalCred := 0
	for _, src := range w.sources {
		totalCred += src.Credibility
	}
	if len(w.sources) > 0 {
		avgCred := totalCred / len(w.sources)
		score += avgCred * 4
		if score > 70 {
			score = 70
		}
	}

	// 交叉验证评分 (最多 20 分)
	if w.phase >= PhaseCrossValidate {
		score += 20
	}

	// 综合评分 (最多 10 分)
	if w.phase >= PhaseSynthesize {
		score += 10
	}

	if score > 100 {
		score = 100
	}
	w.qualityScore = score
}

// ───────────────────────────── 访问器 ─────────────────────────────

// QualityScore 获取当前研究质量评分。
func (w *WebResearchEnvironment) QualityScore() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.qualityScore
}

// Sources 返回已收集的来源副本。
func (w *WebResearchEnvironment) Sources() []ResearchSource {
	w.mu.Lock()
	defer w.mu.Unlock()
	cp := make([]ResearchSource, len(w.sources))
	copy(cp, w.sources)
	return cp
}

// Findings 返回已形成的发现副本。
func (w *WebResearchEnvironment) Findings() []Finding {
	w.mu.Lock()
	defer w.mu.Unlock()
	cp := make([]Finding, len(w.findings))
	copy(cp, w.findings)
	return cp
}

// Render 返回网页研究环境的可渲染状态描述。
func (w *WebResearchEnvironment) Render() string {
	w.mu.Lock()
	defer w.mu.Unlock()

	return fmt.Sprintf("Web Research Environment\n"+
		"  Query: %s\n"+
		"  Phase: %s\n"+
		"  Sources: %d\n"+
		"  Findings: %d\n"+
		"  Quality Score: %d/100\n"+
		"  Duration: %s\n"+
		"  Done: %v",
		w.query,
		w.phase.phaseName(),
		len(w.sources),
		len(w.findings),
		w.qualityScore,
		time.Since(w.startTime).Truncate(time.Second),
		w.done,
	)
}

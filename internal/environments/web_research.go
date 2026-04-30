// Package environments 提供 Agent 运行环境的抽象和具体实现。

package environments

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// ───────────────────────────── 网页研究环境 ─────────────────────────────

// WebResearchEnvironment 提供网页研究任务的标准运行环境。
// 它模拟信息搜集、交叉验证和知识综合的完整流程，
// 并为每个阶段提供质量评估。
type WebResearchEnvironment struct {
	*BaseEnvironment

	mu sync.Mutex

	// query 当前研究查询
	query string
	// phase 当前研究阶段
	phase researchPhase
	// sources 已收集的信息来源
	sources []ResearchSource
	// findings 已形成的发现
	findings []Finding
	// startTime 研究开始时间
	startTime time.Time
	// maxSources 最大信息来源数量
	maxSources int
	// qualityScore 当前研究质量评分 (0-100)
	qualityScore int
}

// researchPhase 表示网页研究的阶段。
type researchPhase int

const (
	PhaseInitial    researchPhase = iota // 初始探索阶段
	PhaseDeepDive                        // 深入挖掘阶段
	PhaseCrossValidate                   // 交叉验证阶段
	PhaseSynthesize                      // 综合总结阶段
	PhaseComplete                        // 研究完成
)

// phaseName 返回阶段的名称。
func (p researchPhase) phaseName() string {
	names := map[researchPhase]string{
		PhaseInitial:      "初始探索",
		PhaseDeepDive:     "深入挖掘",
		PhaseCrossValidate: "交叉验证",
		PhaseSynthesize:   "综合总结",
		PhaseComplete:     "研究完成",
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

// NewWebResearchEnvironment 创建网页研究环境实例。
func NewWebResearchEnvironment() *WebResearchEnvironment {
	base := NewBaseEnvironment("web_research", "网页研究标准流程环境")
	return &WebResearchEnvironment{
		BaseEnvironment: base,
		phase:           PhaseInitial,
		sources:         make([]ResearchSource, 0),
		findings:        make([]Finding, 0),
		maxSources:      20,
		qualityScore:    0,
	}
}

// SetQuery 设置研究查询。
func (w *WebResearchEnvironment) SetQuery(query string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.query = query
	w.startTime = time.Now()
	slog.Info("网页研究: 设置查询", "query", query)
}

// Execute 执行网页研究环境中的动作。
// 支持的动作类型: "search"、"read"、"validate"、"synthesize"、"submit"。
func (w *WebResearchEnvironment) Execute(ctx context.Context, action Action) (*Observation, error) {
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("网页研究执行被取消: %w", ctx.Err())
	default:
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	switch action.Type {
	case "search":
		return w.handleSearch(action)
	case "read":
		return w.handleRead(action)
	case "validate":
		return w.handleValidate(action)
	case "synthesize":
		return w.handleSynthesize(action)
	case "submit":
		return w.handleSubmit()
	default:
		return nil, fmt.Errorf("网页研究环境不支持的动作类型: %s", action.Type)
	}
}

// handleSearch 处理搜索动作。
func (w *WebResearchEnvironment) handleSearch(action Action) (*Observation, error) {
	if len(w.sources) >= w.maxSources {
		return &Observation{
			State:  fmt.Sprintf("已达到最大来源数量限制 (%d)", w.maxSources),
			Reward: -0.1,
			Done:   false,
			Info:   map[string]any{"reason": "max_sources_reached"},
		}, nil
	}

	// 提取搜索关键词
	keywords, _ := action.Parameters["keywords"].(string)
	if keywords == "" {
		return &Observation{
			State:  "搜索关键词为空",
			Reward: -0.2,
			Done:   false,
			Info:   map[string]any{"reason": "empty_keywords"},
		}, nil
	}

	// 模拟搜索结果并记录来源
	source := ResearchSource{
		URL:         fmt.Sprintf("https://example.com/search?q=%s", keywords),
		Title:       keywords,
		Type:        "search_result",
		Credibility: 5,
		Timestamp:   time.Now(),
		Content:     fmt.Sprintf("关于 '%s' 的搜索结果", keywords),
	}
	w.sources = append(w.sources, source)

	// 评估搜索质量并更新分数
	w.evaluateQuality()

	slog.Info("网页研究: 搜索", "keywords", keywords, "sources_count", len(w.sources))

	return &Observation{
		State:  fmt.Sprintf("已找到来源 (%d/%d), 阶段: %s", len(w.sources), w.maxSources, w.phase.phaseName()),
		Reward: 0.1,
		Done:   false,
		Info: map[string]any{
			"source":         source.URL,
			"sources_count":  len(w.sources),
			"quality_score":  w.qualityScore,
		},
	}, nil
}

// handleRead 处理阅读动作。
func (w *WebResearchEnvironment) handleRead(action Action) (*Observation, error) {
	if len(w.sources) == 0 {
		return &Observation{
			State:  "无来源可供阅读",
			Reward: -0.2,
			Done:   false,
			Info:   map[string]any{"reason": "no_sources"},
		}, nil
	}

	// 推进到深入挖掘阶段
	if w.phase == PhaseInitial {
		w.phase = PhaseDeepDive
	}

	slog.Info("网页研究: 深入阅读", "phase", w.phase.phaseName())

	return &Observation{
		State:  fmt.Sprintf("正在深入阅读, 阶段: %s", w.phase.phaseName()),
		Reward: 0.15,
		Done:   false,
		Info: map[string]any{
			"phase":          w.phase.phaseName(),
			"sources_count":  len(w.sources),
		},
	}, nil
}

// handleValidate 处理验证动作。
func (w *WebResearchEnvironment) handleValidate(action Action) (*Observation, error) {
	if w.phase != PhaseDeepDive {
		w.phase = PhaseCrossValidate
	}

	// 检查是否有多个来源支持
	verifiedCount := 0
	for _, src := range w.sources {
		if src.Credibility >= 7 {
			verifiedCount++
		}
	}

	slog.Info("网页研究: 交叉验证",
		"verified", verifiedCount,
		"total", len(w.sources),
	)

	return &Observation{
		State:  fmt.Sprintf("验证完成 (%d 个高可信度来源), 阶段: %s", verifiedCount, w.phase.phaseName()),
		Reward: 0.2,
		Done:   false,
		Info: map[string]any{
			"verified_sources": verifiedCount,
			"total_sources":    len(w.sources),
			"phase":            w.phase.phaseName(),
		},
	}, nil
}

// handleSynthesize 处理综合总结动作。
func (w *WebResearchEnvironment) handleSynthesize(action Action) (*Observation, error) {
	if len(w.sources) < 3 {
		return &Observation{
			State:  "来源不足，无法综合（至少需要 3 个来源）",
			Reward: -0.3,
			Done:   false,
			Info:   map[string]any{"reason": "insufficient_sources"},
		}, nil
	}

	w.phase = PhaseSynthesize

	// 生成一个综合发现
	finding := Finding{
		Topic:      w.query,
		Summary:    fmt.Sprintf("基于 %d 个来源的综合分析结果", len(w.sources)),
		Evidence:   make([]int, len(w.sources)),
		Confidence: float64(w.qualityScore) / 100.0,
	}
	for i := range w.sources {
		finding.Evidence[i] = i
	}
	w.findings = append(w.findings, finding)

	slog.Info("网页研究: 综合总结",
		"findings_count", len(w.findings),
		"confidence", finding.Confidence,
	)

	return &Observation{
		State:  fmt.Sprintf("综合完成, 发现数量: %d", len(w.findings)),
		Reward: 0.3,
		Done:   false,
		Info: map[string]any{
			"findings_count": len(w.findings),
			"confidence":     finding.Confidence,
		},
	}, nil
}

// handleSubmit 处理提交动作。
func (w *WebResearchEnvironment) handleSubmit() (*Observation, error) {
	if len(w.findings) == 0 {
		return &Observation{
			State:  "无发现可提交，请先完成综合",
			Reward: -0.5,
			Done:   false,
			Info:   map[string]any{"reason": "no_findings"},
		}, nil
	}

	w.phase = PhaseComplete
	w.done = true

	slog.Info("网页研究: 提交完成",
		"sources", len(w.sources),
		"findings", len(w.findings),
		"quality_score", w.qualityScore,
	)

	return &Observation{
		State:  "研究已完成并提交",
		Reward: 1.0,
		Done:   true,
		Info: map[string]any{
			"sources":       len(w.sources),
			"findings":      len(w.findings),
			"quality_score": w.qualityScore,
			"duration":      time.Since(w.startTime).String(),
		},
	}, nil
}

// Step 推进网页研究环境的内部阶段。
func (w *WebResearchEnvironment) Step(ctx context.Context) (*Observation, error) {
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("网页研究步进被取消: %w", ctx.Err())
	default:
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// 自动推进到下一个阶段
	switch w.phase {
	case PhaseInitial:
		w.phase = PhaseDeepDive
	case PhaseDeepDive:
		w.phase = PhaseCrossValidate
	case PhaseCrossValidate:
		w.phase = PhaseSynthesize
	case PhaseSynthesize:
		w.phase = PhaseComplete
		w.done = true
	}

	w.evaluateQuality()

	slog.Debug("网页研究: 阶段推进", "phase", w.phase.phaseName())

	return &Observation{
		State:  fmt.Sprintf("阶段已推进至: %s", w.phase.phaseName()),
		Reward: 0.05,
		Done:   w.done,
		Info: map[string]any{
			"phase":           w.phase.phaseName(),
			"quality_score":   w.qualityScore,
		},
	}, nil
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

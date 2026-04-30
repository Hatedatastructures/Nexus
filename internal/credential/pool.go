// Package credential 提供多凭证池管理。
// 支持从多种来源 (环境变量/文件/OAuth) 加载凭证，
// 并在运行时进行轮换和故障转移。
package credential

import (
	"context"
	"encoding/json"
	"log/slog"
	"math/rand"
	"sync"
)

// ───────────────────────────── 凭证类型 ─────────────────────────────

// Credential 表示一个 LLM API 凭证
type Credential struct {
	Provider    string // 提供者名称
	ID          string // 凭证唯一标识
	Label       string // 人类可读标签
	AuthType    string // 认证类型: "oauth" / "api_key"
	Priority    int    // 优先级 (越小越优先)
	Source      string // 来源: "env:TOKEN" / "claude_code" / "config" / ...
	AccessToken string // 访问令牌
	BaseURL     string // API 基础 URL (可选)
}

// ───────────────────────────── 凭证池 ─────────────────────────────

// Pool 管理一组 LLM API 凭证。
// 支持多凭证、轮换策略、故障转移。
// 线程安全: 所有公开方法使用 sync.RWMutex 保护。
type Pool struct {
	mu          sync.RWMutex
	credentials []Credential
	strategy    string // 选择策略: fill_first / round_robin / random / least_used
	cursor      int    // round_robin 游标
	useCounts   []int  // 每个凭证的使用计数 (least_used 策略)
}

// NewPool 创建凭证池
func NewPool() *Pool {
	return &Pool{
		credentials: nil,
		strategy:    "fill_first",
	}
}

// SetStrategy 设置凭证选择策略。
// 支持: fill_first / round_robin / random / least_used
func (p *Pool) SetStrategy(strategy string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.strategy = strategy
}

// ApplyConfig 从配置对象应用凭证池策略。
func (p *Pool) ApplyConfig(cfg CredentialConfig) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if cfg.Selection != "" {
		p.strategy = cfg.Selection
	}
}

// CredentialConfig 凭证池配置 (避免循环依赖 internal/config)。
type CredentialConfig struct {
	Selection string // 选择策略: fill_first / round_robin / random / least_used
}

// Add 添加一个凭证到池中
func (p *Pool) Add(cred Credential) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.credentials = append(p.credentials, cred)
	p.useCounts = append(p.useCounts, 0)
}

// Select 根据当前策略选择最佳可用凭证。
//
// 支持以下策略:
//   - fill_first:  始终返回第一个凭证 (按优先级顺序使用)
//   - round_robin:  轮询策略，使用 cursor 循环选择
//   - random:       随机选择一个凭证
//   - least_used:   选择使用次数最少的凭证
func (p *Pool) Select() *Credential {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.credentials) == 0 {
		return nil
	}

	switch p.strategy {
	case "round_robin":
		return p.selectRoundRobin()
	case "random":
		return p.selectRandom()
	case "least_used":
		return p.selectLeastUsed()
	default: // fill_first 及未知策略
		return &p.credentials[0]
	}
}

// selectRoundRobin 轮询选择: 使用 cursor 循环遍历凭证。
// 必须在持有 p.mu 锁的情况下调用。
func (p *Pool) selectRoundRobin() *Credential {
	idx := p.cursor % len(p.credentials)
	p.cursor = (p.cursor + 1) % len(p.credentials)
	p.useCounts[idx]++
	return &p.credentials[idx]
}

// selectRandom 随机选择一个凭证。
// 必须在持有 p.mu 锁的情况下调用。
func (p *Pool) selectRandom() *Credential {
	idx := rand.Intn(len(p.credentials))
	p.useCounts[idx]++
	return &p.credentials[idx]
}

// selectLeastUsed 选择使用次数最少的凭证。
// 如果有多个凭证使用次数相同，返回索引最小的那个。
// 必须在持有 p.mu 锁的情况下调用。
func (p *Pool) selectLeastUsed() *Credential {
	minIdx := 0
	minCount := p.useCounts[0]
	for i := 1; i < len(p.useCounts); i++ {
		if p.useCounts[i] < minCount {
			minCount = p.useCounts[i]
			minIdx = i
		}
	}
	p.useCounts[minIdx]++
	return &p.credentials[minIdx]
}

// MarkExhausted 标记当前凭证已耗尽并轮换到下一个可用凭证。
// 记录日志用于故障排查。
//
// 不同策略的轮换行为:
//   - fill_first:  移除耗尽的凭证，返回新的第一个
//   - round_robin:  移动 cursor 到下一个
//   - random:       移除耗尽的凭证，后续随机选择
//   - least_used:   移除耗尽的凭证，后续重新计算最少使用
func (p *Pool) MarkExhausted(ctx context.Context, statusCode int, errorMsg string) *Credential {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.credentials) == 0 {
		slog.WarnContext(ctx, "credential pool exhausted, no credentials available")
		return nil
	}

	slog.WarnContext(ctx, "credential marked exhausted, rotating",
		"status_code", statusCode,
		"error", errorMsg,
		"remaining", len(p.credentials)-1,
		"strategy", p.strategy,
	)

	// 移除第一个凭证 (当前耗尽的)
	p.credentials = p.credentials[1:]
	p.useCounts = p.useCounts[1:]

	// round_robin 策略下重置 cursor
	if p.strategy == "round_robin" {
		p.cursor = 0
	}

	if len(p.credentials) == 0 {
		slog.WarnContext(ctx, "all credentials exhausted after rotation")
		return nil
	}

	// 根据策略返回下一个凭证
	switch p.strategy {
	case "round_robin":
		idx := p.cursor % len(p.credentials)
		p.cursor = (p.cursor + 1) % len(p.credentials)
		p.useCounts[idx]++
		return &p.credentials[idx]
	case "random":
		idx := rand.Intn(len(p.credentials))
		p.useCounts[idx]++
		return &p.credentials[idx]
	case "least_used":
		minIdx := 0
		minCount := p.useCounts[0]
		for i := 1; i < len(p.useCounts); i++ {
			if p.useCounts[i] < minCount {
				minCount = p.useCounts[i]
				minIdx = i
			}
		}
		p.useCounts[minIdx]++
		return &p.credentials[minIdx]
	default: // fill_first
		return &p.credentials[0]
	}
}

// Count 返回池中凭证数量
func (p *Pool) Count() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.credentials)
}

// Credentials 返回当前凭证列表的副本 (只读快照)
func (p *Pool) Credentials() []Credential {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]Credential, len(p.credentials))
	copy(out, p.credentials)
	return out
}

// UseCounts 返回当前使用计数的副本 (只读快照，用于诊断)
func (p *Pool) UseCounts() []int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]int, len(p.useCounts))
	copy(out, p.useCounts)
	return out
}

// MarshalJSON 实现 json.Marshaler，用于诊断端点输出。
func (p *Pool) MarshalJSON() ([]byte, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	type credInfo struct {
		ID        string `json:"id"`
		Label     string `json:"label"`
		Provider  string `json:"provider"`
		UseCount  int    `json:"use_count"`
	}

	out := struct {
		Strategy    string     `json:"strategy"`
		Credentials []credInfo `json:"credentials"`
	}{
		Strategy:    p.strategy,
		Credentials: make([]credInfo, 0, len(p.credentials)),
	}
	for i, c := range p.credentials {
		uc := 0
		if i < len(p.useCounts) {
			uc = p.useCounts[i]
		}
		out.Credentials = append(out.Credentials, credInfo{
			ID:       c.ID,
			Label:    c.Label,
			Provider: c.Provider,
			UseCount: uc,
		})
	}

	return json.Marshal(out)
}

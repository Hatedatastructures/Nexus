// Package credential 提供多凭证池管理。
// 支持从多种来源 (环境变量/文件/OAuth) 加载凭证，
// 并在运行时进行轮换和故障转移。
package credential

import (
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// ErrAllExhausted 表示所有凭证均处于冷却期。
var ErrAllExhausted = errors.New("all credentials are exhausted, waiting for cooldown")

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
	mu              sync.RWMutex
	credentials     []Credential
	strategy        string               // 选择策略: fill_first / round_robin / random / least_used
	cursor          int                  // round_robin 游标
	useCounts       []int                // 每个凭证的使用计数 (least_used 策略)
	lastSelectedIdx int                  // 上次选择的凭证索引 (用于 MarkExhausted)
	exhaustedAt     map[string]time.Time // 凭证耗尽时间戳 (用于冷却恢复)
	exhaustCooldown time.Duration        // 耗尽冷却时间 (默认 30 分钟)
}

// NewPool 创建凭证池
func NewPool() *Pool {
	return &Pool{
		credentials:     nil,
		strategy:        "fill_first",
		exhaustedAt:     make(map[string]time.Time),
		exhaustCooldown: 30 * time.Minute,
	}
}

// cryptoRandIntn 使用 crypto/rand 生成 [0, n) 范围内的随机整数。
func cryptoRandIntn(n int) int {
	if n <= 0 {
		return 0
	}
	b := make([]byte, 4)
	if _, err := crand.Read(b); err != nil {
		return 0
	}
	return int(binary.BigEndian.Uint32(b[:])) % n
}

// SetStrategy 设置凭证选择策略。
// 支持: fill_first / round_robin / random / least_used
func (p *Pool) SetStrategy(strategy string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.strategy = strategy
}

// SetExhaustCooldown 设置耗尽冷却时间。
func (p *Pool) SetExhaustCooldown(d time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.exhaustCooldown = d
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

// recoverExhausted 清除冷却时间已过的耗尽标记。
// 必须在持有 p.mu 写锁的情况下调用。
func (p *Pool) recoverExhausted() {
	now := time.Now()
	for id, t := range p.exhaustedAt {
		if now.Sub(t) >= p.exhaustCooldown {
			delete(p.exhaustedAt, id)
		}
	}
}

// availableIndices 返回未耗尽的凭证索引列表。
// 必须在持有 p.mu 锁的情况下调用。
func (p *Pool) availableIndices() []int {
	avail := make([]int, 0, len(p.credentials))
	for i, c := range p.credentials {
		if _, exhausted := p.exhaustedAt[c.ID]; !exhausted {
			avail = append(avail, i)
		}
	}
	return avail
}

// Select 根据当前策略选择最佳可用凭证。
// 返回值类型为 Credential (非指针)，避免 append 重分配导致悬挂指针。
// 自动恢复冷却已过的耗尽凭证，跳过仍在冷却中的凭证。
// 若所有凭证均处于冷却期则返回 (零值, false)。
//
// 支持以下策略:
//   - fill_first:  始终返回第一个可用凭证 (按优先级顺序使用)
//   - round_robin:  轮询策略，使用 cursor 循环选择
//   - random:       随机选择一个可用凭证
//   - least_used:   选择使用次数最少的可用凭证
func (p *Pool) Select() (Credential, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.credentials) == 0 {
		return Credential{}, false
	}

	p.recoverExhausted()

	avail := p.availableIndices()
	if len(avail) == 0 {
		return Credential{}, false
	}

	switch p.strategy {
	case "round_robin":
		return p.selectRoundRobinAvail(avail)
	case "random":
		return p.selectRandomAvail(avail)
	case "least_used":
		return p.selectLeastUsedAvail(avail)
	default: // fill_first 及未知策略
		idx := avail[0]
		p.useCounts[idx]++
		p.lastSelectedIdx = idx
		return p.credentials[idx], true
	}
}

func (p *Pool) selectRoundRobinAvail(avail []int) (Credential, bool) {
	for i := range avail {
		if avail[i] >= p.cursor {
			idx := avail[i]
			p.cursor = idx + 1
			if p.cursor >= len(p.credentials) {
				p.cursor = 0
			}
			p.useCounts[idx]++
			p.lastSelectedIdx = idx
			return p.credentials[idx], true
		}
	}
	idx := avail[0]
	p.cursor = idx + 1
	if p.cursor >= len(p.credentials) {
		p.cursor = 0
	}
	p.useCounts[idx]++
	p.lastSelectedIdx = idx
	return p.credentials[idx], true
}

func (p *Pool) selectRandomAvail(avail []int) (Credential, bool) {
	pick := cryptoRandIntn(len(avail))
	idx := avail[pick]
	p.useCounts[idx]++
	p.lastSelectedIdx = idx
	return p.credentials[idx], true
}

func (p *Pool) selectLeastUsedAvail(avail []int) (Credential, bool) {
	bestIdx := avail[0]
	bestCount := p.useCounts[bestIdx]
	for _, i := range avail[1:] {
		if p.useCounts[i] < bestCount {
			bestCount = p.useCounts[i]
			bestIdx = i
		}
	}
	p.useCounts[bestIdx]++
	p.lastSelectedIdx = bestIdx
	return p.credentials[bestIdx], true
}

// MarkExhausted 标记当前凭证已耗尽并轮换到下一个可用凭证。
// 凭证不会从池中移除，而是进入冷却期 (默认 30 分钟)，
// 冷却结束后自动恢复可用。
//
// 不同策略的轮换行为:
//   - fill_first:  标记耗尽，返回下一个优先级凭证
//   - round_robin:  标记耗尽，移动 cursor 到下一个
//   - random:       标记耗尽，后续随机选择
//   - least_used:   标记耗尽，后续重新计算最少使用
func (p *Pool) MarkExhausted(ctx context.Context, statusCode int, errorMsg string) (Credential, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.credentials) == 0 {
		slog.WarnContext(ctx, "credential pool exhausted, no credentials available")
		return Credential{}, false
	}

	// 查找实际耗尽的凭证索引
	targetIdx := p.lastSelectedIdx
	switch p.strategy {
	case "round_robin":
		targetIdx = (p.cursor - 1 + len(p.credentials)) % len(p.credentials)
	}

	exhaustedID := p.credentials[targetIdx].ID
	now := time.Now()

	p.recoverExhausted()
	p.exhaustedAt[exhaustedID] = now

	avail := p.availableIndices()

	slog.WarnContext(ctx, "credential marked exhausted, cooling down",
		"status_code", statusCode,
		"error", errorMsg,
		"exhausted_id", exhaustedID,
		"available", len(avail),
		"cooldown", p.exhaustCooldown,
		"strategy", p.strategy,
	)

	if len(avail) == 0 {
		slog.WarnContext(ctx, "all credentials exhausted, waiting for cooldown")
		return Credential{}, false
	}

	// 根据策略返回下一个可用凭证
	switch p.strategy {
	case "round_robin":
		return p.selectRoundRobinAvail(avail)
	case "random":
		return p.selectRandomAvail(avail)
	case "least_used":
		return p.selectLeastUsedAvail(avail)
	default: // fill_first
		idx := avail[0]
		p.useCounts[idx]++
		p.lastSelectedIdx = idx
		return p.credentials[idx], true
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
		ID       string `json:"id"`
		Label    string `json:"label"`
		Provider string `json:"provider"`
		UseCount int    `json:"use_count"`
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

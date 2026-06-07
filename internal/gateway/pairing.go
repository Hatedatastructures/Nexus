// Package gateway 提供平台配对系统。
// PairingStore 管理用户与平台之间的配对码生成、验证和生命周期。
// 配对码使用无歧义字母表 (排除 0/O/1/I) 防止手动输入错误。
package gateway

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ───────────────────────────── 平台配对存储 ─────────────────────────────

// PairingStore 管理平台配对码的持久化存储。
// 每个平台一个 JSON 文件，存储在 ~/.nexus/pairing/ 目录下。
// 线程安全，使用读写锁保护并发访问。
type PairingStore struct {
	mu      sync.RWMutex
	baseDir string                      // ~/.nexus/pairing/
	records map[string][]*PairingRecord // platform → records
}

// NewPairingStore 创建平台配对存储。
// baseDir 为空时使用默认路径 ~/.nexus/pairing/。
func NewPairingStore(baseDir string) (*PairingStore, error) {
	if baseDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("获取用户主目录失败: %w", err)
		}
		baseDir = filepath.Join(home, ".nexus", "pairing")
	}

	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("创建配对目录失败: %w", err)
	}

	store := &PairingStore{
		baseDir: baseDir,
		records: make(map[string][]*PairingRecord),
	}

	// 加载已有记录
	if err := store.loadAll(); err != nil {
		slog.Warn("pairing: failed to load history", "error", err)
	}

	return store, nil
}

// ───────────────────────────── 配对码生成 ─────────────────────────────

// GenerateCode 为指定用户和平台生成配对码。
// 返回 8 位无歧义字母配对码。
//
// 限制条件:
//   - 每用户最多 maxPendingPerUser 个未验证配对码
//   - 两次生成间隔不低于 rateLimitInterval
//   - 配对码有效期为 pairingTTL
func (s *PairingStore) GenerateCode(platform, userID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()

	// 检查速率限制: 10 分钟内只能生成一次
	records := s.getRecords(platform, userID)
	for _, r := range records {
		if !r.Verified && now.Sub(r.LastGeneratedAt) < rateLimitInterval {
			remaining := rateLimitInterval - now.Sub(r.LastGeneratedAt)
			return "", fmt.Errorf("生成过于频繁，请等待 %s 后重试", remaining.Round(time.Second))
		}
	}

	// 检查未验证配对码数量上限
	pendingCount := 0
	for _, r := range records {
		if !r.Verified && now.Before(r.ExpiresAt) {
			pendingCount++
		}
	}
	if pendingCount >= maxPendingPerUser {
		return "", fmt.Errorf("已达到最大未验证配对码数量 (%d)，请先验证或等待过期", maxPendingPerUser)
	}

	// 生成配对码
	code, err := generateRandomCode(pairingCodeLength)
	if err != nil {
		return "", fmt.Errorf("生成配对码失败: %w", err)
	}

	// 生成随机盐并计算哈希
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("生成盐失败: %w", err)
	}
	hash := pairingHashCode(code, salt)

	// 创建记录（Code 留空，仅存储哈希）
	record := &PairingRecord{
		Code:            "",
		Hash:            hash,
		Salt:            hex.EncodeToString(salt),
		Platform:        platform,
		UserID:          userID,
		CreatedAt:       now,
		ExpiresAt:       now.Add(pairingTTL),
		LastGeneratedAt: now,
	}

	// 追加到记录列表
	s.records[platform] = append(s.records[platform], record)

	// 持久化
	if err := s.savePlatform(platform); err != nil {
		slog.Warn("pairing: failed to save record", "platform", platform, "error", err)
	}

	slog.Info("pairing: code generated",
		"platform", platform,
		"user_id", userID,
		"code", code[:4]+"****",
	)

	return code, nil
}

// ───────────────────────────── 配对码验证 ─────────────────────────────

// VerifyCode 验证指定用户和平台的配对码。
// 返回 true 表示验证成功。
//
// 安全限制:
//   - 配对码必须在有效期内
//   - 连续验证失败达到 lockoutThreshold 次后锁定 10 分钟
//   - 已验证的配对码不能重复验证
func (s *PairingStore) VerifyCode(platform, userID, code string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()

	records := s.getRecords(platform, userID)

	// Phase 1: 查找匹配的配对码
	for _, r := range records {
		matched := false
		if r.Salt != "" {
			// 新记录: 哈希比较
			salt, err := hex.DecodeString(r.Salt)
			if err == nil {
				computed := pairingHashCode(code, salt)
				matched = subtle.ConstantTimeCompare([]byte(computed), []byte(r.Hash)) == 1
			}
		} else {
			// Legacy 记录: 明文比较
			matched = r.Code == code
		}

		if !matched {
			continue
		}

		// 检查是否已验证
		if r.Verified {
			return false, fmt.Errorf("配对码已被使用")
		}

		// 检查是否过期
		if now.After(r.ExpiresAt) {
			return false, fmt.Errorf("配对码已过期")
		}

		// 检查是否被锁定
		if !r.LockedUntil.IsZero() && now.Before(r.LockedUntil) {
			remaining := r.LockedUntil.Sub(now).Round(time.Second)
			return false, fmt.Errorf("验证已锁定，请等待 %s 后重试", remaining)
		}

		// 所有检查通过，标记为已验证
		r.Verified = true
		if err := s.savePlatform(platform); err != nil {
			slog.Error("pairing: critical - failed to persist verification", "platform", platform, "error", err)
			return false, fmt.Errorf("持久化验证状态失败: %w", err)
		}

		slog.Info("pairing: code verification succeeded",
			"platform", platform,
			"user_id", userID,
		)
		return true, nil
	}

	// Phase 2: 未找到匹配 — 对所有未验证记录递增 Attempts
	for _, r := range records {
		if r.Verified {
			continue
		}
		r.Attempts++
		r.LastAttemptAt = now
		if r.Attempts > lockoutThreshold {
			r.LockedUntil = now.Add(10 * time.Minute)
		}
	}
	if err := s.savePlatform(platform); err != nil {
		slog.Warn("pairing: failed to persist attempt count", "platform", platform, "error", err)
	}

	return false, fmt.Errorf("无效的配对码")
}

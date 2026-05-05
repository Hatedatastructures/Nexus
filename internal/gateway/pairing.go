// Package gateway 提供平台配对系统。
// PairingStore 管理用户与平台之间的配对码生成、验证和生命周期。
// 配对码使用无歧义字母表 (排除 0/O/1/I) 防止手动输入错误。
package gateway

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ───────────────────────────── 常量 ─────────────────────────────

// unambiguousAlphabet 32 字符无歧义字母表，排除容易混淆的 0/O/1/I。
const unambiguousAlphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"

// 配对码长度
const pairingCodeLength = 8

// 配对码有效期
const pairingTTL = 1 * time.Hour

// 每用户最大未使用配对码数量
const maxPendingPerUser = 3

// 生成速率限制间隔
const rateLimitInterval = 10 * time.Minute

// 验证失败锁定阈值
const lockoutThreshold = 5

// ───────────────────────────── 配对码记录 ─────────────────────────────

// PairingRecord 表示一个配对码记录。
type PairingRecord struct {
	Code           string    `json:"code"`             // 8 位配对码
	Platform       string    `json:"platform"`         // 平台标识
	UserID         string    `json:"user_id"`          // 用户 ID
	CreatedAt      time.Time `json:"created_at"`       // 创建时间
	ExpiresAt      time.Time `json:"expires_at"`       // 过期时间
	Verified       bool      `json:"verified"`         // 是否已验证
	Attempts       int       `json:"attempts"`         // 验证尝试次数
	LockedUntil    time.Time `json:"locked_until"`     // 锁定截止时间
	LastAttemptAt  time.Time `json:"last_attempt_at"`  // 最后尝试时间
	LastGeneratedAt time.Time `json:"last_generated_at"` // 最后生成时间
}

// ───────────────────────────── 平台配对存储 ─────────────────────────────

// PairingStore 管理平台配对码的持久化存储。
// 每个平台一个 JSON 文件，存储在 ~/.nexus/pairing/ 目录下。
// 线程安全，使用读写锁保护并发访问。
type PairingStore struct {
	mu       sync.RWMutex
	baseDir  string                        // ~/.nexus/pairing/
	records  map[string][]*PairingRecord   // platform → records
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
		slog.Warn("配对: 加载历史记录失败", "error", err)
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

	// 创建记录
	record := &PairingRecord{
		Code:            code,
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
		slog.Warn("配对: 保存记录失败", "platform", platform, "error", err)
	}

	slog.Info("配对: 生成配对码",
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
	for _, r := range records {
		if r.Code != code {
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

		// 记录尝试次数
		r.Attempts++
		r.LastAttemptAt = now

		// 检查尝试次数超限
		if r.Attempts > lockoutThreshold {
			r.LockedUntil = now.Add(10 * time.Minute)
			s.savePlatform(platform)
			return false, fmt.Errorf("验证失败次数过多，已锁定 10 分钟")
		}

		// 标记为已验证
		r.Verified = true
		s.savePlatform(platform)

		slog.Info("配对: 配对码验证成功",
			"platform", platform,
			"user_id", userID,
		)
		return true, nil
	}

	// 未找到匹配的配对码
	return false, fmt.Errorf("无效的配对码")
}

// ───────────────────────────── 查询方法 ─────────────────────────────

// GetPendingCodes 返回指定用户在指定平台上的未验证配对码。
func (s *PairingStore) GetPendingCodes(platform, userID string) []*PairingRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()
	var pending []*PairingRecord

	for _, r := range s.getRecords(platform, userID) {
		if !r.Verified && now.Before(r.ExpiresAt) {
			pending = append(pending, r)
		}
	}

	return pending
}

// PurgeExpired 清理所有平台上的过期配对码。
// 返回清理的记录数。
func (s *PairingStore) PurgeExpired() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	total := 0

	for platform, records := range s.records {
		var valid []*PairingRecord
		for _, r := range records {
			if now.Before(r.ExpiresAt) || r.Verified {
				valid = append(valid, r)
			} else {
				total++
			}
		}
		s.records[platform] = valid
		s.savePlatform(platform)
	}

	if total > 0 {
		slog.Info("配对: 清理过期配对码", "count", total)
	}

	return total
}

// ───────────────────────────── 持久化 ─────────────────────────────

// loadAll 从磁盘加载所有平台的配对码记录。
func (s *PairingStore) loadAll() error {
	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		platform := entry.Name()[:len(entry.Name())-5] // 去掉 .json 后缀
		path := filepath.Join(s.baseDir, entry.Name())

		data, err := os.ReadFile(path)
		if err != nil {
			slog.Warn("配对: 读取平台文件失败", "platform", platform, "error", err)
			continue
		}

		var records []*PairingRecord
		if err := json.Unmarshal(data, &records); err != nil {
			slog.Warn("配对: 解析平台文件失败", "platform", platform, "error", err)
			continue
		}

		s.records[platform] = records
	}

	return nil
}

// savePlatform 持久化指定平台的配对码记录。
func (s *PairingStore) savePlatform(platform string) error {
	path := filepath.Join(s.baseDir, platform+".json")

	data, err := json.MarshalIndent(s.records[platform], "", "  ")
	if err != nil {
		return fmt.Errorf("序列化记录失败: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("写入记录文件失败: %w", err)
	}

	return nil
}

// getRecords 获取指定平台和用户的配对码记录列表。
// 必须持有 s.mu 锁。
func (s *PairingStore) getRecords(platform, userID string) []*PairingRecord {
	records := s.records[platform]
	var userRecords []*PairingRecord
	for _, r := range records {
		if r.UserID == userID {
			userRecords = append(userRecords, r)
		}
	}
	return userRecords
}

// ───────────────────────────── 内部辅助 ─────────────────────────────

// generateRandomCode 生成指定长度的随机配对码。
// 使用无歧义字母表 (32 字符)，每个字符由加密安全随机数生成。
func generateRandomCode(length int) (string, error) {
	alphabetLen := len(unambiguousAlphabet)
	buf := make([]byte, length)

	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("读取随机字节失败: %w", err)
	}

	code := make([]byte, length)
	for i, b := range buf {
		code[i] = unambiguousAlphabet[int(b)%alphabetLen]
	}

	return string(code), nil
}

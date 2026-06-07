// Package gateway 提供平台配对系统的辅助函数。
package gateway

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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
	Code            string    `json:"code"`              // legacy 明文配对码（旧记录保留，新记录为空）
	Hash            string    `json:"hash,omitempty"`    // sha256(salt+code) hex（新记录使用）
	Salt            string    `json:"salt,omitempty"`    // hex 编码的随机盐（新记录使用）
	Platform        string    `json:"platform"`          // 平台标识
	UserID          string    `json:"user_id"`           // 用户 ID
	CreatedAt       time.Time `json:"created_at"`        // 创建时间
	ExpiresAt       time.Time `json:"expires_at"`        // 过期时间
	Verified        bool      `json:"verified"`          // 是否已验证
	Attempts        int       `json:"attempts"`          // 验证尝试次数
	LockedUntil     time.Time `json:"locked_until"`      // 锁定截止时间
	LastAttemptAt   time.Time `json:"last_attempt_at"`   // 最后尝试时间
	LastGeneratedAt time.Time `json:"last_generated_at"` // 最后生成时间
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

// pairingHashCode 使用 HMAC-SHA256 计算配对码哈希。
func pairingHashCode(code string, salt []byte) string {
	h := hmac.New(sha256.New, salt)
	h.Write([]byte(code))
	return hex.EncodeToString(h.Sum(nil))
}

// atomicWriteFile 原子写入文件: 先写入临时文件，再重命名替换。
// 防止崩溃时留下损坏文件。
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
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
			slog.Warn("pairing: failed to read platform file", "platform", platform, "error", err)
			continue
		}

		var records []*PairingRecord
		if err := json.Unmarshal(data, &records); err != nil {
			slog.Warn("pairing: failed to parse platform file", "platform", platform, "error", err)
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

	if err := atomicWriteFile(path, data, 0600); err != nil {
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

// PendingCodeDisplay 返回用于显示的配对码标识。
// 新记录显示哈希前缀，legacy 记录显示 "legacy"。
func PendingCodeDisplay(r *PairingRecord) string {
	if r.Salt != "" && r.Hash != "" {
		if len(r.Hash) >= 8 {
			return r.Hash[:8]
		}
		return r.Hash
	}
	return "legacy"
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
		if err := s.savePlatform(platform); err != nil {
			slog.Warn("pairing: failed to persist purge", "platform", platform, "error", err)
		}
	}

	if total > 0 {
		slog.Info("pairing: purged expired codes", "count", total)
	}

	return total
}

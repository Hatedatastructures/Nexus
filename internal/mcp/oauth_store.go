package mcp

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
)

// ───────────────────────────── 令牌持久化存储 ─────────────────────────────

// TokenStore 负责将 OAuth 令牌持久化到本地文件。
// 文件以 0600 权限写入，仅当前用户可读写。
// 通过原子写入（临时文件 + rename）防止写入中断导致数据损坏。
type TokenStore struct {
	tokensPath string // 令牌文件路径
}

// NewTokenStore 创建令牌存储实例。
// storePath 为令牌文件的完整路径；如果为空则使用默认路径。
func NewTokenStore(storePath string) (*TokenStore, error) {
	if storePath == "" {
		var err error
		storePath, err = defaultTokenPath()
		if err != nil {
			return nil, fmt.Errorf("获取默认令牌路径失败: %w", err)
		}
	}

	return &TokenStore{
		tokensPath: storePath,
	}, nil
}

// SaveToken 将 OAuth 令牌保存到文件中。
// 使用原子写入策略：先写入临时文件，再 rename 到目标路径。
// 文件权限设为 0600（仅所有者可读写）。
func (s *TokenStore) SaveToken(token *OAuthToken) error {
	if token == nil {
		return fmt.Errorf("令牌不能为空")
	}

	data, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化令牌失败: %w", err)
	}

	// 确保父目录存在
	dir := filepath.Dir(s.tokensPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("创建令牌目录失败: %w", err)
	}

	// 原子写入：先写入临时文件，然后 rename
	tmpPath := s.tokensPath + ".tmp"

	// 写入临时文件（权限 0600）
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		// 清理临时文件
		os.Remove(tmpPath)
		return fmt.Errorf("写入令牌文件失败: %w", err)
	}

	// 原子 rename
	if err := os.Rename(tmpPath, s.tokensPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("重命名令牌文件失败: %w", err)
	}

	slog.Debug("OAuth 令牌已持久化", "path", s.tokensPath)
	return nil
}

// LoadToken 从文件中加载 OAuth 令牌。
// 如果文件不存在或格式错误，返回错误。
func (s *TokenStore) LoadToken() (*OAuthToken, error) {
	data, err := os.ReadFile(s.tokensPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("令牌文件不存在: %s", s.tokensPath)
		}
		return nil, fmt.Errorf("读取令牌文件失败: %w", err)
	}

	var token OAuthToken
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, fmt.Errorf("解析令牌文件 JSON 失败: %w", err)
	}

	if token.AccessToken == "" {
		return nil, fmt.Errorf("令牌文件中没有 access_token")
	}

	slog.Debug("已从磁盘加载 OAuth 令牌", "path", s.tokensPath)
	return &token, nil
}

// DeleteToken 删除磁盘上的令牌文件。
// 文件不存在时不返回错误（幂等操作）。
func (s *TokenStore) DeleteToken() error {
	if err := os.Remove(s.tokensPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除令牌文件失败: %w", err)
	}

	slog.Debug("已删除 OAuth 令牌文件", "path", s.tokensPath)
	return nil
}

// Exists 检查令牌文件是否存在。
func (s *TokenStore) Exists() bool {
	_, err := os.Stat(s.tokensPath)
	return err == nil
}

// ───────────────────────────── 默认路径 ─────────────────────────────

// defaultTokenPath 返回默认的令牌存储路径。
// 路径格式: <NEXUS_HOME>/mcp-tokens/default.json
// NEXUS_HOME 优先使用环境变量，否则使用 ~/.nexus。
func defaultTokenPath() (string, error) {
	base := os.Getenv("NEXUS_HOME")
	if base == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("获取用户主目录失败: %w", err)
		}
		base = filepath.Join(homeDir, ".nexus")
	}

	tokenDir := filepath.Join(base, "mcp-tokens")
	return filepath.Join(tokenDir, "default.json"), nil
}

// DefaultTokenDir 返回默认的令牌存储目录路径。
func DefaultTokenDir() (string, error) {
	base := os.Getenv("NEXUS_HOME")
	if base == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("获取用户主目录失败: %w", err)
		}
		base = filepath.Join(homeDir, ".nexus")
	}
	return filepath.Join(base, "mcp-tokens"), nil
}

// ServerTokenPath 返回指定服务器名称的令牌文件路径。
// 服务器名称会被安全化（去除路径分隔符和特殊字符）。
func ServerTokenPath(serverName string) (string, error) {
	dir, err := DefaultTokenDir()
	if err != nil {
		return "", err
	}
	safeName := safeFilename(serverName)
	return filepath.Join(dir, safeName+".json"), nil
}

// safeFilename 将服务器名称安全化为合法的文件名。
// 去除所有非字母数字、非下划线、非连字符的字符。
var (
	safeFilenameRE  = regexp.MustCompile(`[^\w\-]`)
	safeTrimRE      = regexp.MustCompile(`^_+|_+$`)
)

func safeFilename(name string) string {
	safe := safeFilenameRE.ReplaceAllString(name, "_")
	safe = safeTrimRE.ReplaceAllString(safe, "")
	if safe == "" {
		safe = "default"
	}
	if len(safe) > 128 {
		safe = safe[:128]
	}
	return safe
}

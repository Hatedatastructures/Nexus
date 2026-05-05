// Package testutil 提供测试用的模拟对象和辅助工具。
// 本文件提供测试环境的设置辅助函数，包括环境变量清理和临时目录创建。
package testutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ───────────────────────────── 环境变量清理 ─────────────────────────────

// sensitiveEnvSuffixes 定义需要清理的敏感环境变量后缀。
var sensitiveEnvSuffixes = []string{
	"_API_KEY",
	"_TOKEN",
	"_SECRET",
	"_API_SECRET",
	"_ACCESS_KEY",
	"_PRIVATE_KEY",
}

// sensitiveEnvNames 定义需要清理的完整环境变量名。
var sensitiveEnvNames = []string{
	"OPENAI_API_KEY",
	"ANTHROPIC_API_KEY",
	"GEMINI_API_KEY",
	"GOOGLE_API_KEY",
	"AWS_ACCESS_KEY_ID",
	"AWS_SECRET_ACCESS_KEY",
	"GITHUB_TOKEN",
	"SLACK_TOKEN",
	"DISCORD_TOKEN",
	"TELEGRAM_TOKEN",
	"DATABASE_URL",
}

// ───────────────────────────── 测试环境设置 ─────────────────────────────

// TestEnv 包含测试环境的配置信息。
type TestEnv struct {
	// NexusHome 临时 NEXUS_HOME 目录路径。
	NexusHome string

	// origEnv 保存被修改的环境变量的原始值，用于恢复。
	origEnv map[string]*string
}

// SetupTestEnv 设置隔离的测试环境。
// - 清理所有敏感环境变量 (*_API_KEY, *_TOKEN, *_SECRET 等)
// - 创建临时 NEXUS_HOME 目录
// - 设置 TZ=UTC
// - 注册 t.Cleanup 自动恢复环境
func SetupTestEnv(t *testing.T) *TestEnv {
	t.Helper()

	env := &TestEnv{
		origEnv: make(map[string]*string),
	}

	// 创建临时 NEXUS_HOME
	nexusHome := filepath.Join(t.TempDir(), ".nexus")
	if err := os.MkdirAll(nexusHome, 0o755); err != nil {
		t.Fatalf("创建临时 NEXUS_HOME 失败: %v", err)
	}
	env.NexusHome = nexusHome

	// 保存并设置 NEXUS_HOME
	env.setEnv("NEXUS_HOME", nexusHome)

	// 设置 TZ=UTC
	env.setEnv("TZ", "UTC")

	// 清理敏感环境变量
	env.cleanSensitiveEnv(t)

	// 注册清理函数
	t.Cleanup(func() {
		env.restore()
	})

	return env
}

// setEnv 设置环境变量并保存原始值。
func (e *TestEnv) setEnv(key, value string) {
	if old, exists := os.LookupEnv(key); exists {
		e.origEnv[key] = &old
	} else {
		e.origEnv[key] = nil
	}
	os.Setenv(key, value)
}

// cleanSensitiveEnv 清理敏感环境变量。
func (e *TestEnv) cleanSensitiveEnv(t *testing.T) {
	t.Helper()

	// 清理精确匹配的变量名
	for _, name := range sensitiveEnvNames {
		if _, exists := os.LookupEnv(name); exists {
			e.unsetEnv(name)
		}
	}

	// 清理匹配后缀的变量名
	for _, env := range os.Environ() {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := parts[0]
		upper := strings.ToUpper(key)
		for _, suffix := range sensitiveEnvSuffixes {
			if strings.HasSuffix(upper, suffix) {
				e.unsetEnv(key)
				break
			}
		}
	}
}

// unsetEnv 取消设置环境变量并保存原始值。
func (e *TestEnv) unsetEnv(key string) {
	if old, exists := os.LookupEnv(key); exists {
		e.origEnv[key] = &old
	} else {
		e.origEnv[key] = nil
	}
	os.Unsetenv(key)
}

// restore 恢复所有被修改的环境变量。
func (e *TestEnv) restore() {
	for key, val := range e.origEnv {
		if val == nil {
			os.Unsetenv(key)
		} else {
			os.Setenv(key, *val)
		}
	}
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

// Setenv 设置环境变量并在测试结束时自动恢复。
func Setenv(t *testing.T, key, value string) {
	t.Helper()
	old, exists := os.LookupEnv(key)
	os.Setenv(key, value)
	t.Cleanup(func() {
		if exists {
			os.Setenv(key, old)
		} else {
			os.Unsetenv(key)
		}
	})
}

// Unsetenv 取消设置环境变量并在测试结束时自动恢复。
func Unsetenv(t *testing.T, key string) {
	t.Helper()
	old, exists := os.LookupEnv(key)
	os.Unsetenv(key)
	t.Cleanup(func() {
		if exists {
			os.Setenv(key, old)
		}
	})
}

// Package sandbox 提供基于 SHA-256 的差分文件同步功能。
// 用于在本地和远程沙箱之间高效同步文件变更。
package sandbox

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ───────────────────────────── 文件同步管理器 ─────────────────────────────

// FileSyncManager 管理本地与远程沙箱之间的文件同步。
type FileSyncManager struct {
	localRoot  string
	remoteRoot string
	state      map[string]string // path -> SHA-256 hash
	mu         sync.Mutex
	lastSync   time.Time
	rateLimit  time.Duration // 最小同步间隔
}

// NewFileSyncManager 创建文件同步管理器。
func NewFileSyncManager(localRoot, remoteRoot string) *FileSyncManager {
	return &FileSyncManager{
		localRoot:  localRoot,
		remoteRoot: remoteRoot,
		state:      make(map[string]string),
		rateLimit:  5 * time.Second,
	}
}

// Sync 检测本地文件变更并同步到远程。
// 返回变更的文件列表。
func (m *FileSyncManager) Sync() ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 速率限制
	if time.Since(m.lastSync) < m.rateLimit {
		return nil, nil
	}

	var changed []string

	err := filepath.Walk(m.localRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		// 跳过隐藏目录和大文件
		if info.IsDir() {
			name := info.Name()
			if strings.HasPrefix(name, ".") && name != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Size() > 10*1024*1024 { // 10MB
			return nil
		}

		relPath, _ := filepath.Rel(m.localRoot, path)
		hash, err := fileHash(path)
		if err != nil {
			slog.Warn("file_sync: skip file with hash error", "path", path, "error", err)
			return nil
		}

		if oldHash, exists := m.state[relPath]; !exists || oldHash != hash {
			changed = append(changed, relPath)
			m.state[relPath] = hash
		}

		return nil
	})

	m.lastSync = time.Now()
	return changed, err
}

// SyncBack 从远程下载变更的文件到本地。
// 使用 tar 归档和 SHA-256 差分。
func (m *FileSyncManager) SyncBack(downloadFn func(remotePath string) (io.ReadCloser, error)) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	remoteArchive, err := downloadFn(m.remoteRoot)
	if err != nil {
		return fmt.Errorf("下载远程文件失败: %w", err)
	}
	defer remoteArchive.Close()

	// 解析 tar 归档
	tr := tar.NewReader(remoteArchive)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("读取 tar 失败: %w", err)
		}

		if header.Typeflag != tar.TypeReg {
			continue
		}

		// 路径穿越防护
		if strings.Contains(header.Name, "..") {
			slog.Warn("skipping file with path traversal", "name", header.Name)
			continue
		}

		localPath := filepath.Join(m.localRoot, header.Name)

		// 确保路径在 localRoot 内
		absLocal, err := filepath.Abs(localPath)
		if err != nil {
			slog.Warn("unable to resolve absolute path", "name", header.Name, "err", err)
			continue
		}
		absRoot, err := filepath.Abs(m.localRoot)
		if err != nil {
			slog.Warn("unable to resolve root path", "err", err)
			continue
		}
		rel, err := filepath.Rel(absRoot, absLocal)
		if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			slog.Warn("skipping file outside root directory", "name", header.Name)
			continue
		}

		// 先读取全部内容到缓冲区，同时计算哈希
		remoteHash := sha256.New()
		const maxTarFileSize = 10 * 1024 * 1024 // 10MB
		var buf bytes.Buffer
		limited := io.LimitReader(io.TeeReader(tr, remoteHash), maxTarFileSize)
		if _, err := io.Copy(&buf, limited); err != nil {
			slog.Warn("file_sync: failed to read tar entry", "name", header.Name, "err", err)
			continue
		}
		remoteHashHex := fmt.Sprintf("%x", remoteHash.Sum(nil))

		// 检查本地文件是否已存在且相同
		if localHash, err := fileHash(localPath); err == nil {
			if localHash == remoteHashHex {
				continue // 文件未变更
			}
		}

		// 写入本地文件
		if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
			slog.Warn("file_sync: failed to create directory", "path", filepath.Dir(localPath), "err", err)
			continue
		}

		if err := os.WriteFile(localPath, buf.Bytes(), 0644); err != nil {
			slog.Warn("file_sync: failed to write file", "path", localPath, "err", err)
			continue
		}

		slog.Debug("file synced", "path", header.Name)
	}

	return nil
}

// CreateTarArchive 将变更文件打包为 tar 归档。
func (m *FileSyncManager) CreateTarArchive(files []string, w io.Writer) error {
	tw := tar.NewWriter(w)
	defer tw.Close()

	for _, relPath := range files {
		localPath := filepath.Join(m.localRoot, relPath)
		info, err := os.Stat(localPath)
		if err != nil {
			continue
		}

		header := &tar.Header{
			Name:    relPath,
			Size:    info.Size(),
			Mode:    int64(info.Mode()),
			ModTime: info.ModTime(),
		}

		if err := tw.WriteHeader(header); err != nil {
			continue
		}

		f, err := os.Open(localPath)
		if err != nil {
			continue
		}
		if _, err := io.Copy(tw, f); err != nil {
			slog.Warn("file_sync: failed to write file to tar", "path", relPath, "err", err)
		}
		f.Close()
	}

	return nil
}

// StateHash 返回当前同步状态的哈希（用于变更检测）。
func (m *FileSyncManager) StateHash() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	h := sha256.New()
	keys := make([]string, 0, len(m.state))
	for path := range m.state {
		keys = append(keys, path)
	}
	sort.Strings(keys)
	for _, path := range keys {
		fmt.Fprintf(h, "%s:%s\n", path, m.state[path])
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

// fileHash 计算文件的 SHA-256 哈希。
func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

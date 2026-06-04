package state

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// jsonlRecord is a single line in a JSONL session log.
type jsonlRecord struct {
	Type      string `json:"type"`       // "session_meta", "message", "compaction", "prompt_history"
	Timestamp int64  `json:"timestamp"`  // Unix timestamp
	Data      any    `json:"data"`
}

// SessionPersister writes session events to JSONL files with rotation.
type SessionPersister struct {
	mu           sync.Mutex
	dir          string // output directory
	maxSize      int64  // rotation threshold in bytes
	maxRotations int    // max rotated files to keep
	file         *os.File
	writer       *bufio.Writer
	currentSize  int64
	sessionID    string
}

const (
	defaultMaxSize      = 256 * 1024 // 256KB
	defaultMaxRotations = 3
)

// NewSessionPersister creates a new SessionPersister for the given directory and session ID.
// The directory is created if it does not exist.
func NewSessionPersister(dir, sessionID string) *SessionPersister {
	return &SessionPersister{
		dir:          dir,
		sessionID:    sanitizeFilename(sessionID),
		maxSize:      defaultMaxSize,
		maxRotations: defaultMaxRotations,
	}
}

// sanitizeFilename strips characters that could cause path traversal or confusion.
func sanitizeFilename(name string) string {
	name = strings.Map(func(r rune) rune {
		if r >= 0x20 && r != '/' && r != '\\' && r != ':' && r != '*' && r != '?' &&
			r != '"' && r != '<' && r != '>' && r != '|' && r != 0 {
			return r
		}
		return '_'
	}, name)
	if len(name) > 128 {
		name = name[:128]
	}
	if name == "" || name == "." || name == ".." {
		return "_invalid_"
	}
	return name
}

// Open creates or opens the JSONL file for the session. If the file already exists it is
// opened in append mode and currentSize is set to the existing file size.
func (sp *SessionPersister) Open() error {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	if err := os.MkdirAll(sp.dir, 0o700); err != nil {
		return fmt.Errorf("create session dir: %w", err)
	}

	path := sp.filePath()

	info, err := os.Stat(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("stat session file: %w", err)
	}

	flag := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	f, err := os.OpenFile(path, flag, 0o600)
	if err != nil {
		return fmt.Errorf("open session file: %w", err)
	}

	sp.file = f
	sp.writer = bufio.NewWriter(f)
	if info != nil {
		sp.currentSize = info.Size()
	}

	return nil
}

// RecordSessionMeta writes a session_meta record.
func (sp *SessionPersister) RecordSessionMeta(session *Session) error {
	return sp.writeRecord("session_meta", session)
}

// RecordMessage writes a message record.
func (sp *SessionPersister) RecordMessage(msg *MessageRecord) error {
	return sp.writeRecord("message", msg)
}

// RecordCompaction writes a compaction record.
func (sp *SessionPersister) RecordCompaction(messageCount int, tokensSaved int) error {
	return sp.writeRecord("compaction", map[string]int{
		"message_count": messageCount,
		"tokens_saved":  tokensSaved,
	})
}

// RecordPromptHistory writes a prompt_history record.
func (sp *SessionPersister) RecordPromptHistory(prompt string) error {
	return sp.writeRecord("prompt_history", map[string]string{
		"prompt": prompt,
	})
}

// Close flushes and closes the underlying file.
func (sp *SessionPersister) Close() error {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	if sp.writer != nil {
		if err := sp.writer.Flush(); err != nil {
			return fmt.Errorf("flush writer: %w", err)
		}
	}
	if sp.file != nil {
		if err := sp.file.Close(); err != nil {
			return fmt.Errorf("close file: %w", err)
		}
		sp.file = nil
		sp.writer = nil
	}
	return nil
}

// writeRecord is the common path for all Record methods. It handles locking, rotation, and writing.
func (sp *SessionPersister) writeRecord(recordType string, data any) error {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	if sp.writer == nil {
		return fmt.Errorf("session persister is not open")
	}

	if err := sp.rotateIfNeeded(); err != nil {
		slog.Warn("session persister rotation failed", "error", err)
		// Continue writing to the current file; rotation is best-effort.
	}

	rec := jsonlRecord{
		Type:      recordType,
		Timestamp: time.Now().Unix(),
		Data:      data,
	}

	before := sp.currentSize
	if err := json.NewEncoder(sp.writer).Encode(rec); err != nil {
		return fmt.Errorf("encode jsonl record: %w", err)
	}
	if err := sp.writer.Flush(); err != nil {
		return fmt.Errorf("flush writer: %w", err)
	}

	// Estimate bytes written from the encoder — we need a stat-free approximation.
	// We track by comparing file position when possible, or fall back to a seek.
	if fi, err := sp.file.Stat(); err == nil {
		sp.currentSize = fi.Size()
	} else {
		// Rough fallback: nothing we can do, keep the old size.
		sp.currentSize = before
	}

	return nil
}

// rotateIfNeeded performs log rotation when the current file exceeds maxSize.
func (sp *SessionPersister) rotateIfNeeded() error {
	if sp.currentSize <= sp.maxSize {
		return nil
	}

	// Flush and close the current file.
	if sp.writer != nil {
		if err := sp.writer.Flush(); err != nil {
			return fmt.Errorf("flush during rotation: %w", err)
		}
	}
	if sp.file != nil {
		if err := sp.file.Close(); err != nil {
			return fmt.Errorf("close during rotation: %w", err)
		}
	}

	base := sp.filePath()

	// Delete the oldest rotation if it exists.
	oldest := sp.rotatedPath(sp.maxRotations)
	if _, err := os.Stat(oldest); err == nil {
		if err := os.Remove(oldest); err != nil {
			return fmt.Errorf("remove oldest rotation: %w", err)
		}
	}

	// Shift existing rotated files: .N-1 -> .N, ..., .1 -> .2
	for i := sp.maxRotations - 1; i >= 1; i-- {
		src := sp.rotatedPath(i)
		dst := sp.rotatedPath(i + 1)
		if _, err := os.Stat(src); err == nil {
			if err := os.Rename(src, dst); err != nil {
				return fmt.Errorf("rotate %d to %d: %w", i, i+1, err)
			}
		}
	}

	// Move current file to .1 rotation.
	if err := os.Rename(base, sp.rotatedPath(1)); err != nil {
		return fmt.Errorf("rotate current to .1: %w", err)
	}

	// Open a fresh file.
	f, err := os.OpenFile(base, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open new session file after rotation: %w", err)
	}
	sp.file = f
	sp.writer = bufio.NewWriter(f)
	sp.currentSize = 0

	return nil
}

// filePath returns the path to the active JSONL file.
func (sp *SessionPersister) filePath() string {
	return filepath.Join(sp.dir, sp.sessionID+".jsonl")
}

// rotatedPath returns the path for the Nth rotated file.
func (sp *SessionPersister) rotatedPath(n int) string {
	return filepath.Join(sp.dir, fmt.Sprintf("%s.%d.jsonl", sp.sessionID, n))
}

package jsonl

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
)

// Sink appends the event records of one node to that node's JSONL file. It is
// safe for concurrent use: the mutex keeps one record's bytes and its newline
// contiguous, so a line is never interleaved with another writer's.
type Sink struct {
	mu     sync.Mutex
	path   string
	file   *os.File
	writer *bufio.Writer
	closed bool
}

// Open creates dir if needed and opens node's events file there for append.
// Opening eagerly is how a configured events directory is validated: a path the
// platform cannot write fails at startup, not on the first event.
func Open(dir string, node events.Node) (*Sink, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create events directory %s: %w", dir, err)
	}
	path := filepath.Join(dir, FileName(node))
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open events file %s: %w", path, err)
	}
	return &Sink{path: path, file: file, writer: bufio.NewWriter(file)}, nil
}

// FileName returns the file node's events are appended to. The node identity is
// constant for a process run, so it is stated here once instead of on every
// record. It is exported so a reader can find a known node's events without
// scanning a directory.
func FileName(node events.Node) string {
	return fmt.Sprintf("events-%s-%s-%s-%s-%s.jsonl",
		portable(node.Project),
		portable(node.Environment),
		portable(node.Site),
		portable(node.Machine),
		portable(node.Role),
	)
}

// portable replaces every character that is not portable in a file name with
// "-". Deployment identifiers are only required to be non-blank and unique, so
// a project or role may legitimately contain characters a file system does not
// accept, and failing to record is a worse answer than an approximate name.
func portable(value string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '-', r == '_', r == '.':
			return r
		default:
			return '-'
		}
	}, value)
}

// Append writes record as one compact JSON line and flushes it before
// returning. Appending to a closed sink is an error, so events are never
// discarded silently.
func (s *Sink) Append(_ context.Context, record events.Record) error {
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode event for %s: %w", s.path, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("append to closed events file %s", s.path)
	}
	if _, err := s.writer.Write(data); err != nil {
		return fmt.Errorf("write events file %s: %w", s.path, err)
	}
	if err := s.writer.WriteByte('\n'); err != nil {
		return fmt.Errorf("write events file %s: %w", s.path, err)
	}
	if err := s.writer.Flush(); err != nil {
		return fmt.Errorf("flush events file %s: %w", s.path, err)
	}
	return nil
}

// Close flushes and closes the events file. It is idempotent, so a deferred
// Close is safe next to an explicit one during shutdown. A flush failure still
// closes the file and is reported with the file's context.
func (s *Sink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	flushErr := s.writer.Flush()
	closeErr := s.file.Close()
	if flushErr != nil {
		return fmt.Errorf("flush events file %s: %w", s.path, flushErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close events file %s: %w", s.path, closeErr)
	}
	return nil
}

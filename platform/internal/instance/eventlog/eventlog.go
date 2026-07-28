package eventlog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events/storage"
)

var (
	// ErrClosed indicates that operations were attempted on a closed Backend.
	ErrClosed = errors.New("jsonl: backend is closed")

	// ErrInvalidPath indicates an empty or invalid events file path.
	ErrInvalidPath = errors.New("jsonl: invalid events file")
)

var _ storage.Backend = (*Backend)(nil)

// Backend appends stamped envelopes to one instance's JSONL event log.
type Backend struct {
	mu     sync.Mutex
	file   *os.File
	closed bool
	path   string
}

// New opens path for append, creating its parent directory and file when needed.
func New(path string) (*Backend, error) {
	filePath := strings.TrimSpace(path)
	if filePath == "" {
		return nil, fmt.Errorf("%w: events file cannot be empty", ErrInvalidPath)
	}

	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("jsonl: create events directory %s: %w", dir, err)
	}

	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("jsonl: open events file %s: %w", filePath, err)
	}

	return &Backend{
		file: file,
		path: filePath,
	}, nil
}

// Path returns the instance event log path.
func (b *Backend) Path() string { return b.path }

// Store encodes envelope into a canonical JSON object, appends it as a single
// line, and syncs the file to disk.
func (b *Backend) Store(ctx context.Context, envelope events.Envelope) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return ErrClosed
	}

	data, err := events.Encode(envelope)
	if err != nil {
		return fmt.Errorf("jsonl: %w", err)
	}

	data = append(data, '\n')

	if _, err := b.file.Write(data); err != nil {
		return fmt.Errorf("jsonl: write %s: %w", b.path, err)
	}

	if err := b.file.Sync(); err != nil {
		return fmt.Errorf("jsonl: sync %s: %w", b.path, err)
	}

	return nil
}

// Close syncs and closes the underlying events file. Subsequent calls to Close
// are idempotent and return nil.
func (b *Backend) Close(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return nil
	}
	b.closed = true

	syncErr := b.file.Sync()
	closeErr := b.file.Close()

	if syncErr != nil || closeErr != nil {
		var errs []error
		if syncErr != nil {
			errs = append(errs, fmt.Errorf("jsonl: sync on close %s: %w", b.path, syncErr))
		}
		if closeErr != nil {
			errs = append(errs, fmt.Errorf("jsonl: close %s: %w", b.path, closeErr))
		}
		return errors.Join(errs...)
	}

	return nil
}

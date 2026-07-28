package eventstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
)

// ErrInvalidPath reports an empty or unusable machine store path.
var ErrInvalidPath = errors.New("eventstore: invalid store file")

var _ Appender = (*File)(nil)

// File stores one machine-scoped envelope per JSONL line.
type File struct {
	mu     sync.Mutex
	file   *os.File
	closed bool
	path   string
}

// Open opens the machine store for append, creating its parent directory and
// file when needed.
func Open(path string) (*File, error) {
	storePath := strings.TrimSpace(path)
	if storePath == "" {
		return nil, fmt.Errorf("%w: store file cannot be empty", ErrInvalidPath)
	}

	dir := filepath.Dir(storePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("eventstore: create store directory %s: %w", dir, err)
	}

	file, err := os.OpenFile(storePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("eventstore: open store file %s: %w", storePath, err)
	}

	return &File{file: file, path: storePath}, nil
}

// Path returns the shared machine event file.
func (f *File) Path() string { return f.path }

// Append encodes envelope as one line and appends it, syncing to disk before
// returning. A non machine-scoped envelope is refused before anything is
// written.
func (f *File) Append(ctx context.Context, envelope events.Envelope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if envelope.Scope != events.ScopeMachine {
		return fmt.Errorf("%w: %s is scoped %q", ErrScope, envelope.Type, envelope.Scope)
	}

	data, err := events.Encode(envelope)
	if err != nil {
		return fmt.Errorf("eventstore: %w", err)
	}
	data = append(data, '\n')

	f.mu.Lock()
	defer f.mu.Unlock()

	if f.closed {
		return ErrClosed
	}
	if _, err := f.file.Write(data); err != nil {
		return fmt.Errorf("eventstore: write %s: %w", f.path, err)
	}
	if err := f.file.Sync(); err != nil {
		return fmt.Errorf("eventstore: sync %s: %w", f.path, err)
	}
	return nil
}

// Close syncs and closes the file. It is idempotent.
func (f *File) Close(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.closed {
		return nil
	}
	f.closed = true

	syncErr := f.file.Sync()
	closeErr := f.file.Close()

	var errs []error
	if syncErr != nil {
		errs = append(errs, fmt.Errorf("eventstore: sync on close %s: %w", f.path, syncErr))
	}
	if closeErr != nil {
		errs = append(errs, fmt.Errorf("eventstore: close %s: %w", f.path, closeErr))
	}
	return errors.Join(errs...)
}

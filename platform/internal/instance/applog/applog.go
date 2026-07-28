package applog

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// ErrInvalidPath indicates an empty or invalid log file path.
var ErrInvalidPath = errors.New("applog: invalid log file")

// Logger is one instance's open application log: an slog.Logger writing to the
// instance's log file, and the file itself so the process can close it.
//
// It embeds *slog.Logger, so a holder logs through it directly and derives
// contextual loggers from it with With. Only the process that opened it closes
// it; a derived logger is a view on the same file.
type Logger struct {
	*slog.Logger

	mu     sync.Mutex
	file   *os.File
	closed bool
	path   string
}

// Open opens path for append, creating its parent directory and the file when
// needed, and returns a logger writing JSON records to it.
//
// base is stamped on every record this logger and everything derived from it
// writes. It is where the process states who it is, so no call site has to
// repeat it and none can claim to be a different instance.
func Open(path string, base ...slog.Attr) (*Logger, error) {
	filePath := strings.TrimSpace(path)
	if filePath == "" {
		return nil, fmt.Errorf("%w: log file cannot be empty", ErrInvalidPath)
	}

	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("applog: create log directory %s: %w", dir, err)
	}

	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("applog: open log file %s: %w", filePath, err)
	}

	// The error stream is the second destination rather than the only fallback:
	// the file is what a deployed instance leaves behind, and the stream is what a
	// developer and the scenario harness watch while it runs.
	var handler slog.Handler = slog.NewJSONHandler(io.MultiWriter(file, os.Stderr), &slog.HandlerOptions{Level: slog.LevelInfo})
	if len(base) > 0 {
		handler = handler.WithAttrs(base)
	}
	return &Logger{Logger: slog.New(handler), file: file, path: filePath}, nil
}

// Path returns the instance application log path.
func (l *Logger) Path() string { return l.path }

// Close syncs and closes the underlying log file. Subsequent calls are
// idempotent and return nil.
//
// Records written after it are dropped rather than failing anything: slog
// discards a handler's error, and a process that is already shutting down has
// nowhere to report a log write it could not make. Close is therefore the last
// thing the process does.
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return nil
	}
	l.closed = true

	var errs []error
	if err := l.file.Sync(); err != nil {
		errs = append(errs, fmt.Errorf("applog: sync on close %s: %w", l.path, err))
	}
	if err := l.file.Close(); err != nil {
		errs = append(errs, fmt.Errorf("applog: close %s: %w", l.path, err))
	}
	return errors.Join(errs...)
}

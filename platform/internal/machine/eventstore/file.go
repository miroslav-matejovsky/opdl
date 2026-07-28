package eventstore

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
)

// ErrInvalidPath reports an empty or unusable machine store path.
var ErrInvalidPath = errors.New("eventstore: invalid store file")

// followInterval is how long a reader that has caught up waits before looking
// for more. It is an operational constant of this implementation, not part of
// the Reader contract: a SQLite store would wait on something else, and no
// caller may depend on how quickly an appended entry shows up.
const followInterval = 100 * time.Millisecond

var (
	_ Appender = (*File)(nil)
	_ Reader   = (*File)(nil)
)

// File is a machine store backed by a JSON Lines file: one envelope per line,
// in the order the store accepted them, so a line number is a Position.
//
// The file is the machine's, not an instance's. Both instances of a machine
// name the same path, and only the one holding Primary Ownership appends to it.
type File struct {
	mu     sync.Mutex
	file   *os.File
	closed bool
	path   string
}

// Open opens the machine store at path, creating the parent directory and the
// file if they are missing and appending to what is already there, so a restart
// continues the machine's store rather than starting a new one.
//
// The path is taken whole. It is authored in the machine's blueprint and
// carried in the deployment descriptor, so no path is composed here and an
// operator reading the blueprint sees exactly which file the machine's
// instances share.
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

// Path returns the file the machine's instances share. The runtime reports it
// at startup, because it is what an operator opens to read what the machine
// did.
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

// Read opens its own handle on the store file and streams the entries after
// from, then follows the file for new ones until ctx ends.
//
// The handle is the reader's own, so a stream is unaffected by Close and ends
// only with its context. That is what lets the machine's Passive instance
// follow a file the Active instance may reopen.
func (f *File) Read(ctx context.Context, from Position) (<-chan Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	f.mu.Lock()
	closed := f.closed
	f.mu.Unlock()
	if closed {
		return nil, ErrClosed
	}

	file, err := os.Open(f.path)
	if err != nil {
		return nil, fmt.Errorf("eventstore: open %s for reading: %w", f.path, err)
	}

	results := make(chan Result)
	go f.follow(ctx, file, from, results)
	return results, nil
}

// follow reads whole lines from file, emitting the ones after from, and waits
// for more once it reaches the end. It owns file and closes it on the way out.
func (f *File) follow(ctx context.Context, file *os.File, from Position, results chan<- Result) {
	defer close(results)
	defer func() { _ = file.Close() }()

	// A line is only complete once its newline has been read. An appender that
	// is mid-write leaves a partial line at the end of the file, so what has
	// been read of it is held here until the rest arrives rather than decoded
	// as a truncated envelope.
	var pending []byte
	reader := bufio.NewReader(file)
	var position Position

	for {
		chunk, err := reader.ReadBytes('\n')
		pending = append(pending, chunk...)

		switch {
		case err == nil:
			line := bytes.TrimSpace(pending)
			pending = nil
			if len(line) == 0 {
				continue
			}
			position++
			if position <= from {
				continue
			}
			envelope, decodeErr := events.Decode(line)
			if decodeErr != nil {
				emit(ctx, results, Result{Err: fmt.Errorf("eventstore: read %s at position %d: %w", f.path, position, decodeErr)})
				return
			}
			if !emit(ctx, results, Result{Entry: Entry{Envelope: envelope, Position: position}}) {
				return
			}
		case errors.Is(err, io.EOF):
			select {
			case <-ctx.Done():
				return
			case <-time.After(followInterval):
			}
		default:
			emit(ctx, results, Result{Err: fmt.Errorf("eventstore: read %s: %w", f.path, err)})
			return
		}
	}
}

// emit delivers one result, reporting whether it was received. A reader that
// has gone away cancels its context, which is the only thing that unblocks a
// send nobody is waiting for.
func emit(ctx context.Context, results chan<- Result, result Result) bool {
	select {
	case results <- result:
		return true
	case <-ctx.Done():
		return false
	}
}

// Close syncs and closes the appending handle. It is idempotent. Streams opened
// by Read hold their own handles and are not affected.
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

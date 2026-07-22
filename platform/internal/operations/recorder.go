package operations

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
)

// Recorder writes platform events to the process error stream and, optionally,
// to one append-only JSONL file. Record is safe for concurrent callbacks.
//
// It is a writer, not an event model: Record stamps a typed payload with the
// process's one envelope factory and writes the same canonical envelope the site
// journal carries. What differs is only where the event goes and what a failure
// means. A local record is best effort, because a process that cannot describe
// itself must still run.
type Recorder struct {
	mu      sync.Mutex
	factory events.Factory
	stderr  io.Writer
	file    *os.File
	path    string
}

type contextKey struct{}

var safeFilename = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

// discard is what a component used on its own gets: a recorder that writes
// nowhere. It carries no factory, so it never stamps anything either.
var discard = &Recorder{stderr: io.Discard}

// Open creates a recorder that stamps with factory, the one envelope factory
// composed for this process. eventDir may be empty to disable JSONL retention;
// events are still written to stderr for the service manager.
//
// The file is named after the writing process's origin, so two instances of one
// machine never write to the same file and a reader can tell them apart before
// opening either.
func Open(eventDir string, factory events.Factory) (*Recorder, error) {
	origin := factory.Origin()
	r := &Recorder{factory: factory, stderr: os.Stderr}
	eventDir = strings.TrimSpace(eventDir)
	if eventDir == "" {
		return r, nil
	}
	if err := os.MkdirAll(eventDir, 0o750); err != nil {
		return nil, fmt.Errorf("create operations event directory %s: %w", eventDir, err)
	}
	name := safeName(strings.Join([]string{
		origin.Project,
		origin.Environment,
		origin.Site,
		origin.Machine,
		origin.ProcessRole,
		fmt.Sprint(origin.PID),
	}, "-")) + ".jsonl"
	path := filepath.Join(eventDir, name)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open operations event file %s: %w", path, err)
	}
	r.file, r.path = file, path
	return r, nil
}

// WithRecorder attaches recorder to ctx for platform components and callbacks.
func WithRecorder(ctx context.Context, recorder *Recorder) context.Context {
	return context.WithValue(ctx, contextKey{}, recorder)
}

// FromContext returns the recorder attached to ctx. It returns a discard
// recorder when a component is used independently in a unit test.
func FromContext(ctx context.Context) *Recorder {
	if recorder, ok := ctx.Value(contextKey{}).(*Recorder); ok && recorder != nil {
		return recorder
	}
	return discard
}

// Record states one typed platform event locally: it stamps event into a
// canonical envelope and writes that envelope as one JSON line to stderr and to
// the JSONL file. Callers pass a payload and nothing else.
//
// It returns nothing on purpose. A local record is best effort: a process that
// cannot describe what it is doing must still do it, so a failure to stamp or
// write is reported to the process error stream and never propagated into the
// operation that caused it. An event a caller must not lose belongs in the site
// journal, through the Event Fabric publisher.
func (r *Recorder) Record(ctx context.Context, event events.Event) {
	if r == nil {
		return
	}
	envelope, err := r.factory.Wrap(ctx, event)
	if err != nil {
		fallbackWrite(r.stderr, "platform event stamping failed: type=%s error=%v\n", event.EventType(), err)
		return
	}
	r.write(envelope)
}

// write puts one envelope on both sinks as a single JSON line. It is the only
// thing that writes an event, and it accepts nothing but an envelope, so no
// other shape can reach a sink.
//
// A sink failure is reported to the other sink as plain text, never as an event
// line: a broken sink is not a fact about the platform, and a reader must be
// able to decode every event line it finds. It is never recursively recorded.
func (r *Recorder) write(envelope events.Envelope) {
	data, err := events.Encode(envelope)
	if err != nil {
		fallbackWrite(r.stderr, "platform event encoding failed: type=%s error=%v\n", envelope.Type, err)
		return
	}
	data = append(data, '\n')

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.stderr.Write(data); err != nil && r.file != nil {
		fallbackWrite(r.file, "platform event stderr write failed: error=%v\n", err)
	}
	if r.file != nil {
		if _, err := r.file.Write(data); err != nil {
			fallbackWrite(r.stderr, "platform event JSONL write failed: path=%s error=%v\n", r.path, err)
		}
	}
}

// fallbackWrite is the terminal reporting path for a broken operational sink.
// There is nowhere else to return or record this error, so the final write is
// deliberately best effort.
func fallbackWrite(writer io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(writer, format, args...)
}

// Path returns the configured JSONL path, or an empty string when file storage
// is disabled.
func (r *Recorder) Path() string {
	if r == nil {
		return ""
	}
	return r.path
}

// Close flushes and closes the optional JSONL file.
func (r *Recorder) Close() error {
	if r == nil || r.file == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	syncErr := r.file.Sync()
	closeErr := r.file.Close()
	r.file = nil
	if syncErr != nil {
		syncErr = fmt.Errorf("sync operations event file %s: %w", r.path, syncErr)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("close operations event file %s: %w", r.path, closeErr)
	}
	return errors.Join(syncErr, closeErr)
}

func safeName(value string) string {
	value = safeFilename.ReplaceAllString(strings.TrimSpace(value), "_")
	value = strings.Trim(value, "._-")
	if value == "" {
		return "platform"
	}
	return value
}

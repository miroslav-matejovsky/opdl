package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/miroslav-matejovsky/opdl/platform/deployment"
)

// Level is the operational severity of an event.
type Level string

const (
	// LevelInfo describes a normal lifecycle transition.
	LevelInfo Level = "info"
	// LevelWarn describes a recoverable degradation that needs attention.
	LevelWarn Level = "warn"
	// LevelError describes an operation that failed or made the process unavailable.
	LevelError Level = "error"

	// AttributeError contains an operation's error string.
	AttributeError = "error"
	// AttributeDurationMS contains elapsed wall-clock milliseconds.
	AttributeDurationMS = "duration_ms"
	// AttributePath contains the local path involved in an operation.
	AttributePath = "path"
	// AttributeActivationKind identifies initial activation, failover, or failback.
	AttributeActivationKind = "activation_kind"
	// AttributeAppliedSequence contains the last journal sequence projected locally.
	AttributeAppliedSequence = "applied_sequence"
	// AttributeObject contains the kernel object name involved in an operation,
	// such as the Primary Ownership mutex. A named kernel object has no
	// path, so this is what identifies it to an operator.
	AttributeObject = "object"
	// AttributeAbandoned reports that ownership was taken over from a process that
	// died without releasing it, rather than from one that handed it over. It is
	// what distinguishes a crash failover from a planned handover.
	AttributeAbandoned = "abandoned"
	// AttributeExisted reports that a kernel object already existed when this
	// process opened it, meaning a peer process on this machine is running.
	AttributeExisted = "existed"
)

// Event is one self-contained operational JSONL record.
type Event struct {
	Timestamp   time.Time      `json:"timestamp"`
	Type        string         `json:"type"`
	Level       Level          `json:"level"`
	Component   string         `json:"component"`
	Project     string         `json:"project"`
	Environment string         `json:"environment"`
	Site        string         `json:"site"`
	Machine     string         `json:"machine"`
	Role        string         `json:"role"`
	PID         int            `json:"pid"`
	Message     string         `json:"message"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

// Recorder serializes operational events to the process error stream and,
// optionally, one append-only JSONL file. Emit is safe for concurrent callbacks.
type Recorder struct {
	mu       sync.Mutex
	identity Event
	stderr   io.Writer
	file     *os.File
	path     string
	now      func() time.Time
}

type contextKey struct{}

var safeFilename = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

var discard = &Recorder{stderr: io.Discard, now: time.Now}

// Open creates a recorder. eventDir may be empty to disable JSONL retention;
// structured events are still written to stderr for the service manager.
func Open(eventDir string, descriptor deployment.Descriptor, role string) (*Recorder, error) {
	r := &Recorder{
		identity: Event{
			Project:     descriptor.Project,
			Environment: descriptor.Environment,
			Site:        descriptor.Site,
			Machine:     descriptor.Machine,
			Role:        role,
			PID:         os.Getpid(),
		},
		stderr: os.Stderr,
		now:    time.Now,
	}
	eventDir = strings.TrimSpace(eventDir)
	if eventDir == "" {
		return r, nil
	}
	if err := os.MkdirAll(eventDir, 0o750); err != nil {
		return nil, fmt.Errorf("create operations event directory %s: %w", eventDir, err)
	}
	name := safeName(strings.Join([]string{
		descriptor.Project,
		descriptor.Environment,
		descriptor.Site,
		descriptor.Machine,
		role,
		fmt.Sprint(os.Getpid()),
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

// Emit records one operational transition. Attributes must contain values that
// encoding/json can marshal. A sink failure is reported to stderr and never
// recursively emitted.
func (r *Recorder) Emit(eventType string, level Level, component, message string, attributes map[string]any) {
	if r == nil {
		return
	}
	event := r.identity
	event.Timestamp = r.now().UTC()
	event.Type = eventType
	event.Level = level
	event.Component = component
	event.Message = message
	event.Attributes = attributes
	data, err := json.Marshal(event)
	if err != nil {
		fallbackWrite(r.stderr, "platform operations event encoding failed: type=%s error=%v\n", eventType, err)
		return
	}
	data = append(data, '\n')

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.stderr.Write(data); err != nil && r.file != nil {
		fallbackWrite(r.file, "{\"type\":\"operations.stderr_write_failed\",\"error\":%q}\n", err.Error())
	}
	if r.file != nil {
		if _, err := r.file.Write(data); err != nil {
			fallbackWrite(r.stderr, "platform operations JSONL write failed: path=%s error=%v\n", r.path, err)
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

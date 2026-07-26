package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/miroslav-matejovsky/opdl/utils/atomicfile"
)

// ErrInvalidPath reports an empty state file path. Every deployed instance
// authors one, so an empty path is a descriptor that never reached the runtime
// intact rather than something to fall back from.
var ErrInvalidPath = errors.New("state: invalid state file")

// ErrUnknownReason reports an advance that named no known reason. Every advance
// is one of the two moments an instance becomes a new incarnation, and which one
// it was is recorded, so there is no unattributed advance to fall back to.
var ErrUnknownReason = errors.New("state: unknown advance reason")

// Reason is why an instance began a new incarnation. It selects which counter an
// advance moves, and it is what the runtime reports the advance with, so the
// vocabulary is stated here rather than separately by whoever calls Advance.
type Reason string

// The two reasons are the two moments an instance becomes a new incarnation. A
// step-down is deliberately not one of them; see the package doc.
const (
	// ReasonProcessStarted is a new process, whether after a clean stop or a crash.
	ReasonProcessStarted Reason = "process_started"
	// ReasonActivated is the instance taking Primary Ownership and beginning to
	// serve.
	ReasonActivated Reason = "activated"
)

// Counter is how often one kind of advance has happened and when it last did.
//
// The count is kept per kind because the total says only that this is a
// different incarnation, not what made it one: an instance on epoch 6 that
// started once and activated five times is a machine whose ownership keeps
// moving, and one that started five times and activated once is a machine whose
// process keeps dying. Both reach the same epoch.
type Counter struct {
	// Count is how many advances of this kind this instance has had, ever.
	Count uint64 `json:"count"`
	// UpdatedAt is when the last one happened, in UTC. It is the zero time on a
	// kind that has never advanced, which for activations is every instance that
	// has only ever been Passive.
	UpdatedAt time.Time `json:"updated_at"`
}

// State is one instance's durable record as it is stored on disk.
//
// It is a struct rather than a bare number because the file is the instance's
// state, not its epoch: anything later found to need carrying across a restart
// is a field here, and an older file that does not carry it decodes with that
// field's zero value rather than failing to decode at all.
type State struct {
	// Epoch is how many incarnations this instance has had, counting both kinds.
	// It starts at zero on an instance that has never run and advances by exactly
	// one per incarnation, so the first epoch any running instance sees is one.
	// This is the number used as a fencing token; the counters below explain it
	// but never replace it, because neither of them alone is monotonic in the
	// order things happened.
	Epoch uint64 `json:"epoch"`
	// UpdatedAt is when the epoch last advanced, whichever kind moved it, in UTC.
	// It is stated next to the epoch so a reader of the file can tell a counter
	// that stopped days ago from one still moving without reasoning about which
	// of the two kinds was last.
	UpdatedAt time.Time `json:"updated_at"`
	// ProcessEpoch counts the incarnations that began because the process
	// started, so it is how many times this instance has been launched or has
	// come back from a crash.
	ProcessEpoch Counter `json:"process_epoch"`
	// ActivationEpoch counts the incarnations that began because the instance
	// took Primary Ownership, so it is how many times this instance has served.
	ActivationEpoch Counter `json:"activation_epoch"`
}

// Store is one instance's state file and the current contents of it.
//
// A Store is safe for concurrent use: the epoch is advanced from the process's
// startup path and again from the ownership machine's activation path, and read
// by whatever reports it.
type Store struct {
	path string

	mu    sync.Mutex
	state State
}

// Open reads the instance's state file, or starts a fresh record when the file
// does not exist yet, which is this instance's first ever start on this machine.
//
// It does not advance the epoch. Opening the file and beginning a new
// incarnation are separate acts, because the runtime opens the store before it
// knows whether it will get far enough to be an incarnation at all; Advance is
// what claims one.
//
// A file that exists but does not decode is an error. The runtime cannot tell a
// truncated record from an absent one, and treating it as absent would restart
// the counter at an epoch that has already been handed out, which is exactly the
// confusion the epoch exists to prevent.
func Open(path string) (*Store, error) {
	filePath := strings.TrimSpace(path)
	if filePath == "" {
		return nil, fmt.Errorf("%w: state file cannot be empty", ErrInvalidPath)
	}
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return nil, fmt.Errorf("state: create state directory %s: %w", filepath.Dir(filePath), err)
	}

	store := &Store{path: filePath}
	data, err := os.ReadFile(filePath)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("state: read state %s: %w", filePath, err)
	}
	if err := json.Unmarshal(data, &store.state); err != nil {
		return nil, fmt.Errorf("state: decode state %s: %w", filePath, err)
	}
	return store, nil
}

// utcNow is the clock the record is stamped from. Times are stored in UTC so the
// file reads the same whatever the host's zone is, and so two instances' records
// can be compared with each other and with the local event record.
func utcNow() time.Time { return time.Now().UTC() }

// Advance begins a new incarnation of this instance for the given reason: it
// raises the epoch and that reason's counter by exactly one each, stamps both
// with the current time, persists the record, and returns the record it wrote.
//
// It returns the whole record rather than the new epoch alone so a caller that
// reports the advance reports what was written by it, rather than reading the
// store again and describing whatever the next advance had already made of it.
//
// The write happens before the record is returned, so an epoch a caller has
// acted on is one a restart will not hand out again. It is atomic, so a crash
// part way through leaves the previous epoch intact rather than a record nothing
// can decode; a caller that is told the write failed has not been given a new
// epoch and must not use one.
func (s *Store) Advance(reason Reason) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	next := s.state
	counter, err := next.counterFor(reason)
	if err != nil {
		return State{}, err
	}
	at := utcNow()
	next.Epoch++
	next.UpdatedAt = at
	counter.Count++
	counter.UpdatedAt = at

	data, err := json.Marshal(next)
	if err != nil {
		return State{}, fmt.Errorf("state: encode state: %w", err)
	}
	if err := atomicfile.WriteFile(s.path, data, 0o644); err != nil {
		return State{}, fmt.Errorf("state: write state %s: %w", s.path, err)
	}
	s.state = next
	return next, nil
}

// counterFor returns the counter the given reason advances, for the caller to
// modify in place. An unknown reason is an error rather than a third counter:
// the two kinds are the whole vocabulary, and silently accepting a new one would
// leave an epoch nothing accounts for.
func (s *State) counterFor(reason Reason) (*Counter, error) {
	switch reason {
	case ReasonProcessStarted:
		return &s.ProcessEpoch, nil
	case ReasonActivated:
		return &s.ActivationEpoch, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownReason, reason)
	}
}

// Epoch returns this instance's current epoch: zero before the first Advance,
// and the incarnation number after it.
func (s *Store) Epoch() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Epoch
}

// State returns a copy of the whole durable record: the epoch, both counters,
// and when each last moved. It is a copy, so a caller reading several fields
// reads one consistent record rather than racing an advance between them.
func (s *Store) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// Path returns the state file this store persists to. The runtime reports it at
// startup, so an operator knows which file carries this instance's epoch.
func (s *Store) Path() string { return s.path }

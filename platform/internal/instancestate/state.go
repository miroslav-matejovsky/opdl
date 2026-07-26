package instancestate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/miroslav-matejovsky/opdl/utils/atomicfile"
)

// ErrInvalidPath reports an empty state file path. Every deployed instance
// authors one, so an empty path is a descriptor that never reached the runtime
// intact rather than something to fall back from.
var ErrInvalidPath = errors.New("instancestate: invalid state file")

// State is one instance's durable record as it is stored on disk.
//
// It is a struct with one field rather than a bare number because the file is
// the instance's state, not its epoch: anything later found to need carrying
// across a restart is a field here, and an older file that does not carry it
// decodes with that field's zero value rather than failing to decode at all.
type State struct {
	// Epoch is how many incarnations this instance has had. It starts at zero on
	// an instance that has never run and advances by exactly one per incarnation,
	// so the first epoch any running instance sees is one.
	Epoch uint64 `json:"epoch"`
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
		return nil, fmt.Errorf("instancestate: create state directory %s: %w", filepath.Dir(filePath), err)
	}

	data, err := os.ReadFile(filePath)
	if errors.Is(err, os.ErrNotExist) {
		return &Store{path: filePath}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("instancestate: read state %s: %w", filePath, err)
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("instancestate: decode state %s: %w", filePath, err)
	}
	return &Store{path: filePath, state: state}, nil
}

// Advance begins a new incarnation of this instance: it raises the epoch by
// exactly one, persists the record, and returns the new epoch.
//
// The write happens before the value is returned, so an epoch a caller has acted
// on is one a restart will not hand out again. It is atomic, so a crash part way
// through leaves the previous epoch intact rather than a record nothing can
// decode; a caller that is told the write failed has not been given a new epoch
// and must not use one.
func (s *Store) Advance() (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	next := s.state
	next.Epoch++
	data, err := json.Marshal(next)
	if err != nil {
		return 0, fmt.Errorf("instancestate: encode state: %w", err)
	}
	if err := atomicfile.WriteFile(s.path, data, 0o644); err != nil {
		return 0, fmt.Errorf("instancestate: write state %s: %w", s.path, err)
	}
	s.state = next
	return next.Epoch, nil
}

// Epoch returns this instance's current epoch: zero before the first Advance,
// and the incarnation number after it.
func (s *Store) Epoch() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Epoch
}

// Path returns the state file this store persists to. The runtime reports it at
// startup, so an operator knows which file carries this instance's epoch.
func (s *Store) Path() string { return s.path }

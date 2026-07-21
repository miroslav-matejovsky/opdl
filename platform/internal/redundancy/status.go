package redundancy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/miroslav-matejovsky/opdl/utils/atomicfile"
)

// Status is a process's live operational snapshot, written to its status file
// for deployment diagnostics: its role, lifecycle state, process id, how far
// its projection has applied of the journal, how long it has
// been lagging, whether it is promotable, and its last error.
//
// It is not an active fence. The OS lock remains authoritative, and the runtime
// never grants active ownership from this file. Deployment tooling may use a
// fresh status whose PID is still live to verify handover readiness. A stale
// status after a crash is only historical diagnostics.
type Status struct {
	// Role identifies the primary or standby process.
	Role InstanceRole `json:"role"`
	// State is the process lifecycle state.
	State State `json:"state"`
	// PID is the operating-system process id, so a stale file can be told from a
	// live one.
	PID int `json:"pid"`
	// Applied is the highest journal sequence this process has projected.
	Applied uint64 `json:"applied"`
	// HighWater is the last sequence the journal has accepted, as this process last
	// observed it.
	HighWater uint64 `json:"high_water"`
	// Lag is how long the projection has continuously been behind the journal, as
	// a duration string; "0s" when caught up.
	Lag string `json:"lag"`
	// Promotable reports whether this process is current enough to take over.
	Promotable bool `json:"promotable"`
	// UpdatedAt is when this snapshot was written, in UTC.
	UpdatedAt time.Time `json:"updated_at"`
	// LastError is the last error this process recorded, empty when none.
	LastError string `json:"last_error,omitempty"`
}

// PrepareStatusDir creates the directory a machine's status files live in.
//
// It exists because ownership no longer touches the filesystem. The file-lock
// fence this replaced created this directory as a side effect of taking the lock,
// so status writes silently depended on the fence having run first. Creating it
// explicitly keeps that dependency from being reintroduced by accident.
func PrepareStatusDir(statusPath string) error {
	if err := os.MkdirAll(filepath.Dir(statusPath), 0o755); err != nil {
		return fmt.Errorf("redundancy: create status directory: %w", err)
	}
	return nil
}

// Write writes s to path atomically: it encodes to a sibling temporary file and
// renames it into place using atomicfile, so a reader never sees a half-written status even if the
// process is killed mid-write.
func (s Status) Write(path string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("redundancy: encode status: %w", err)
	}
	data = append(data, '\n')
	if err := atomicfile.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("redundancy: write status: %w", err)
	}
	return nil
}

// ReadStatus reads a process status file. It is for diagnostics and tests; the
// runtime never reads a status file to make a decision.
func ReadStatus(path string) (Status, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Status{}, fmt.Errorf("redundancy: read status %s: %w", path, err)
	}
	var s Status
	if err := json.Unmarshal(data, &s); err != nil {
		return Status{}, fmt.Errorf("redundancy: decode status %s: %w", path, err)
	}
	return s, nil
}

package redundancy

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Status is a slot's live operational snapshot, written to the slot's status file
// for deployment diagnostics: which slot this is, its lifecycle state, its
// process id, how far its projection has applied of the journal, how long it has
// been lagging, whether it is promotable, and its last error.
//
// It is not coordination state. The OS fence remains authoritative, and a status
// file can be stale after a crash, so nothing reads it to decide who is active.
// It exists so a deployment tool or an operator can see what each slot is doing
// without a control API.
type Status struct {
	// Slot is the local process identity this status is for.
	Slot Slot `json:"slot"`
	// State is the slot's lifecycle state.
	State State `json:"state"`
	// PID is the operating-system process id, so a stale file can be told from a
	// live one.
	PID int `json:"pid"`
	// Applied is the highest journal sequence this slot's projection has applied.
	Applied uint64 `json:"applied"`
	// HighWater is the last sequence the journal has accepted, as this slot last
	// observed it.
	HighWater uint64 `json:"high_water"`
	// Lag is how long the projection has continuously been behind the journal, as
	// a duration string; "0s" when caught up.
	Lag string `json:"lag"`
	// Promotable reports whether this slot's projection is current enough to take
	// over. A slot lagging beyond the configured bound is not promotable.
	Promotable bool `json:"promotable"`
	// UpdatedAt is when this snapshot was written, in UTC.
	UpdatedAt time.Time `json:"updated_at"`
	// LastError is the last error this slot recorded, empty when none.
	LastError string `json:"last_error,omitempty"`
}

// Write writes s to path atomically: it encodes to a sibling temporary file and
// renames it into place, so a reader never sees a half-written status even if the
// process is killed mid-write.
func (s Status) Write(path string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("redundancy: encode status: %w", err)
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("redundancy: write status %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("redundancy: replace status %s: %w", path, err)
	}
	return nil
}

// ReadStatus reads a slot's status file. It is for diagnostics and tests; the
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

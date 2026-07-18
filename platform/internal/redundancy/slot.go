package redundancy

import "fmt"

// Slot is a stable local process identity on one machine: A or B. A slot is
// process identity only. It is never a machine, a service, a registration voter,
// or a permanent primary: which slot holds the active fence can change across
// restarts, and the machine keeps exactly one registration vote no matter how
// many slots run.
type Slot string

const (
	// SlotA is the first local process slot.
	SlotA Slot = "a"
	// SlotB is the second local process slot.
	SlotB Slot = "b"
)

// ParseSlot parses s into a Slot, rejecting anything but "a" or "b". A two-slot
// machine requires an explicit slot per process, so a launch argument that is
// blank or misspelled fails here rather than defaulting two processes to one
// identity.
func ParseSlot(s string) (Slot, error) {
	switch Slot(s) {
	case SlotA, SlotB:
		return Slot(s), nil
	default:
		return "", fmt.Errorf("redundancy: invalid slot %q: want %q or %q", s, SlotA, SlotB)
	}
}

// String returns the slot's token, "a" or "b".
func (s Slot) String() string { return string(s) }

// Valid reports whether s is one of the two defined slots.
func (s Slot) Valid() bool { return s == SlotA || s == SlotB }

// OperationalName composes the operational identity of one slot on one machine,
// "<machine>/<slot>". It labels logs and operational diagnostics so the two slots
// of a machine are told apart.
//
// It is never a domain identity. Registration proposal, decision, and
// durable-handler identities stay machine-scoped, so two slots on one machine
// never become two registration voters.
func OperationalName(machine string, slot Slot) string {
	return machine + "/" + string(slot)
}

package events

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// newEventID returns a time-ordered unique event ID. The occurrence time makes
// IDs sort lexically, and random bytes distinguish events created in one clock
// tick.
//
// It is the factory's generator, not an API. An occurrence identity belongs to
// the envelope that occurrence produced, so nothing outside this package mints
// one and then decides what to attach it to.
func newEventID() (string, error) {
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", fmt.Errorf("events: generate occurrence ID: %w", err)
	}
	return fmt.Sprintf("%020d-%s", time.Now().UnixNano(), hex.EncodeToString(suffix[:])), nil
}

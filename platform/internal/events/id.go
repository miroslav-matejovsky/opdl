package events

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// NewID returns a time-ordered unique event ID. The occurrence time makes IDs
// sort lexically, and random bytes distinguish events created in one clock tick.
func NewID() string {
	var suffix [8]byte
	_, _ = rand.Read(suffix[:])
	return fmt.Sprintf("%020d-%s", time.Now().UnixNano(), hex.EncodeToString(suffix[:]))
}

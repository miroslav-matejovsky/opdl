package eventstore

import (
	"context"
	"errors"

	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
)

var (
	// ErrScope reports an envelope offered to the machine store that is not
	// machine-scoped. It is a composition error, not an input error: the
	// envelope belongs somewhere else and appending it here would replay one
	// process's story, or a whole site's, as the machine's own.
	ErrScope = errors.New("eventstore: envelope is not machine-scoped")

	// ErrClosed reports an operation on a store that has been closed.
	ErrClosed = errors.New("eventstore: store is closed")
)

// Position is a store's own ordinal for one stored envelope, counted from one
// in the order the store accepted it. It is a property of the store, not of the
// event: the same fact appended to two stores can hold two different positions,
// and a store rebuilt from scratch renumbers everything it holds.
type Position uint64

// FromStart asks a Reader for everything the store still holds. It is the zero
// Position because no entry ever has it, so "resume from where I got to" and
// "start from the beginning" are the same call with the same argument.
const FromStart Position = 0

// Entry is one stored envelope and the position the store gave it.
type Entry struct {
	// Envelope is the stored fact, exactly as it was appended.
	Envelope events.Envelope
	// Position is where this store put it. Positions are strictly increasing
	// within one Read, with no gaps between consecutive entries.
	Position Position
}

// Result is one read from the store: an Entry, or the failure that ended the
// stream.
//
// A failed read is on the channel rather than lost because a reader that stops
// receiving cannot otherwise tell a cancelled follow from a store it can no
// longer read, and those call for opposite reactions. Err is set on the last
// value a Read channel carries, and its Entry is zero; a channel that closes
// with no such value ended because its context did.
type Result struct {
	Entry Entry
	Err   error
}

// Appender is the write half of a machine store, held by the instance that owns
// the machine. Implementations must be safe for concurrent use by multiple
// goroutines.
type Appender interface {
	// Append stores one machine-scoped envelope. It returns an error wrapping
	// ErrScope for any other scope, and ErrClosed after Close.
	Append(ctx context.Context, envelope events.Envelope) error

	// Close releases the store's write resources. It is idempotent.
	Close(ctx context.Context) error
}

// Reader is the read half of a machine store, held by the instance that does
// not own the machine.
type Reader interface {
	// Read replays the entries after from in append order and then follows the
	// store for new ones, until ctx ends. Pass FromStart for everything the
	// store still holds.
	//
	// The returned channel is closed when the stream ends, whether because ctx
	// ended or because a Result carrying an error was delivered. A reader that
	// stops receiving blocks the stream rather than losing entries, so a reader
	// that is leaving must cancel ctx.
	Read(ctx context.Context, from Position) (<-chan Result, error)
}

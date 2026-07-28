// Package eventstore is the machine level's event store: the one place a
// machine's two instances share what happened to the machine.
//
// # What it is for
//
// An instance's own record (internal/instance/eventlog) is write-only and
// private to one process. The site's distribution (internal/site/eventfabric)
// carries what a whole site has to agree on. Between them sits a fact that is
// neither: a machine-scoped fact, true of the machine rather than of one of its
// processes, which the machine's other instance has to be able to read. Primary
// Ownership moving is the first of them. This package is where those live.
//
// # Who writes and who reads
//
//	instance | write-only              | one process appends, nobody reads back
//	machine  | one writer, one reader  | the Active instance appends, the Passive instance reads
//	site     | one writer, many readers| the Active instance publishes, every machine reads
//
// The single writer is not enforced here and does not need to be. Only the
// instance holding Primary Ownership runs the active composition, and ownership
// is released only after the active composition has closed, so the machine's
// two instances never hold the appender at the same time. Within one process,
// Append is safe for concurrent use.
//
// # Two contracts, not one
//
// Append and Read are separate interfaces because their holders are separate.
// The Active instance is handed an Appender and cannot read; the Passive
// instance is handed a Reader and cannot write. A single Store interface would
// hand each of them the half it must not use, and the compiler would stop
// saying anything useful about which is which.
//
// # Positions are delivery, not fact
//
// A Position is this store's own ordinal, counted in the order the store
// accepted envelopes. It is not on the envelope and never becomes part of a
// fact, the same stance internal/events already takes on transport ordering: an
// event carries what happened, and a store says where it put it. A reader
// resuming from a position is asking this store to continue, not asserting
// anything about the events themselves.
//
// # Scope is enforced on the way in
//
// Append refuses an envelope that is not machine-scoped. Scope adds
// destinations and never removes the instance record (see events.Scope), so an
// instance-scoped fact reaching this store is not a routing choice but a
// composition mistake: a fact about one process would be replayed by the other
// instance as though the machine had done it. The check is here rather than at
// the composition root because this store is the thing that would be wrong.
//
// A site-scoped envelope is refused for the same reason from the other side.
// The site distribution already delivers it to every instance of every machine,
// including both of this machine's, so appending it here would deliver it to
// the Passive instance twice.
//
// # The file implementation
//
// Open backs the store with a JSON Lines file, one envelope per line, appended
// and fsynced in the order stated. Reads replay the retained lines and then
// follow the file for new ones by polling. Polling rather than change
// notification is a deliberate first implementation: the interval is an
// operational constant of this implementation and not part of the contract, and
// a reader that missed a notification on a shared Windows file would stall
// silently, which is the failure that is hard to see.
//
// The store the machine's instances share is expected to become SQLite. Nothing
// above this package should have to change when it does, which is what the two
// interfaces and the contract test suite are for.
package eventstore

// Package state is one instance's durable state: the record it carries
// across restarts and crashes, kept in the state file its blueprint authored.
//
// # The epoch counter
//
// The only thing the record holds today is an epoch: a counter that advances by
// exactly one every time this instance starts a new incarnation. It is what lets
// a reader tell one incarnation of an instance from the next, which nothing else
// about an instance can say. A machine, a role, and an address are all the same
// after a crash as they were before it; the epoch is not.
//
// Two moments advance it, and they are the two moments an instance becomes a
// different incarnation:
//
//   - the process starts, which covers a clean restart and a crash alike, because
//     a crashed process is one that will next be observed starting;
//   - the instance becomes Active, which is when it starts producing decisions
//     and writing on the machine's behalf.
//
// Stepping down does not advance it. An instance that stops serving writes
// nothing further, so there is nothing for a later reader to have to order
// against, and the next activation advances the epoch anyway.
//
// # Each kind is counted on its own
//
// The record also keeps the two kinds apart: how many times this instance has
// started, how many times it has activated, and when each of those last
// happened. The total epoch is what fences writes, but on its own it says only
// that an incarnation is a different one, never what made it different. An
// instance on epoch six that started once and activated five times is a machine
// whose ownership keeps moving; one that started five times and activated once
// is a machine whose process keeps dying. The epoch is the same number in both.
//
// The counters are derived from nothing and derive nothing: the epoch is not
// recomputed from them on read, because a record written by an older build may
// carry an epoch the counters do not add up to, and the epoch is the value
// already handed out. Neither counter is usable as a fencing token on its own,
// since neither is monotonic in the order the two kinds actually interleaved.
//
// Timestamps are stored in UTC, so a record reads the same whatever the host's
// zone is and can be lined up against the instance's event record and against
// the other instance's state file.
//
// # Why it is durable
//
// A counter kept in memory would restart at one on every crash, which is the one
// case it exists for. Persisting it is what makes the sequence monotonic across
// the whole life of a deployed instance, so a later incarnation always carries a
// higher epoch than every earlier one. That is the property a fencing token
// needs: a reader holding an instance's epoch can reject anything stamped with an
// older one. See docs/drafts/data-priority.md.
//
// # It is an instance's, not a machine's
//
// A machine's two instances are independent runtimes with independent
// incarnations, so each has its own state file and its own epoch, and the two
// counters are unrelated. Neither reads the other's. The one local file the two
// instances do share is the ownership lease, which is the machine's and belongs
// to package redundancy; it is not this.
//
// # Durability
//
// Every advance is written, atomically, before the new epoch is returned, so a
// caller never acts on an epoch that was not recorded first. A crash part way
// through a write leaves the previous epoch rather than a record nothing can
// decode, and a file that exists but does not decode is a startup failure rather
// than a counter silently restarting: the runtime cannot tell a truncated record
// from an absent one, and guessing would hand out an epoch already used.
//
// The write is a rename over the file, the same durability the ownership lease
// has. It is atomic against a concurrent reader but not fsynced, so a host that
// loses power in the same instant can lose the last advance. That is accepted
// here for the same reason it is on the lease: both files are local to one host,
// and a host that lost power is one whose instances are all restarting anyway.
package state

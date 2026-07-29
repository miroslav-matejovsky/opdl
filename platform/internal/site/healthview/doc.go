// Package healthview is one instance's picture of every service at its site.
//
// It is built from the static inventory the descriptor carries, so it knows
// what exists before anything has reported. A unit nobody has spoken about
// reads as Unknown rather than being absent, which is the difference between a
// service the platform cannot see and a service the platform has never heard
// of.
//
// # Why observers are kept apart
//
// Every machine's services are probed by both of that machine's platform
// instances, and their reports are kept in separate slots rather than being
// collapsed into whichever arrived last. Last-writer-wins would make the view
// depend on delivery order, so two instances that received the same reports
// could disagree. Keeping a slot per expected observer makes the reduction a
// function of what is currently fresh, which is the same on every receiver that
// has the same reports.
//
// It also keeps disagreement visible. Two observers of one service that do not
// agree is a real condition an operator should see, not something to average
// away by preferring the Active instance or the newest message.
//
// # Freshness is the receiver's
//
// A report expires by how long ago it arrived here, measured on this process's
// own clock, and never by the timestamp its sender wrote. Machines at a site do
// not share a clock, and a sender with a wrong one would otherwise be able to
// make its reports immortal or stillborn. The sender's time is kept as
// diagnostic detail and is used for nothing.
//
// An expired report is not deleted. It becomes a stale observer, which is a
// different fact from an observer that has never reported at all, and both are
// reported separately from what the service itself is doing.
//
// # Fencing
//
// Reports carry their sender's process-start epoch and a sequence within it. A
// report is applied only when it is newer than what that observer's slot
// already holds, so a message that arrives late or twice cannot undo a newer
// one, and a restarted observer's first report supersedes everything its
// previous incarnation said.
//
// The epoch is the process-start count, not the activation count. Ownership
// moving does not make an observer's reports newer or older, because probing
// has nothing to do with which instance is Active.
//
// # What this package does not do
//
// It holds no transport and speaks no wire format. Reports reach it as values,
// from whatever delivered them, and the whole of it is in memory: nothing here
// is written down, because a health result is current state that expires rather
// than a fact worth replaying. A restarted instance rebuilds its picture from
// the reports that arrive next.
package healthview

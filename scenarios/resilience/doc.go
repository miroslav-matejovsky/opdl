// Package resilience holds the scenarios about a platform that is interrupted
// or cannot start.
//
// They are the reason the site journal is on disk and the reason startup is
// ordered, so they are worth driving from outside the process, where a promise
// about restart and refusal is the only thing a customer can actually rely on.
//
// RestartRebuildsStateFromTheJournal kills a machine rather than stopping it,
// because what has to survive is a process that stopped without warning.
// PlatformRefusesToStartWithoutItsJournalStorage takes the storage away and
// requires the machine to fail loudly instead of serving an empty projection.
//
// The shared machinery is internal/harness; see its documentation for the
// scratch layout, the machine budget, and the failure diagnostics.
package resilience

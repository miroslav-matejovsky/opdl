# Stage 6: one polling primitive and pure log parsing

## Goal

Replace the hand-rolled poll loops with one primitive, and split log scraping
into pure parsing plus scenario assertions. Behavior does not change.

## Why

### The poll loops

Two functions implement the same loop with different names.

`waitStatus` (`scenarios/harness_test.go:598`) and `waitForMarker`
(`scenarios/harness_test.go:807`) both:

1. compute a deadline,
2. check a condition,
3. check whether a watched process has already exited, and fail immediately
   with its output if so,
4. fail on deadline with a rendered diagnostic,
5. sleep and repeat.

Step 3 is the part worth keeping and the part that is easy to get wrong. It is
why neither uses `require.Eventually`: `Eventually` has no notion of a process
that can no longer possibly satisfy the condition, so it burns its whole
timeout and then reports "Condition never satisfied" instead of the exit error
that explains everything. Both functions document that hazard separately, in
their own words.

Meanwhile the places that do use `require.Eventually` inherit exactly that
weakness. `waitForAPI` (`scenarios/registrationapi_test.go:218`) polls a
machine that may have died at startup and will wait 60 seconds to say so.

### The duplicate readiness polls

`waitForAPI` (`scenarios/registrationapi_test.go:218`) and
`waitForManagedAPI` (`scenarios/warm_standby_test.go:229`) are the same
function. Both GET `/registrations`, both accept 200, both use
`apiWaitTimeout` and `apiPollInterval`. They differ only in whose logs go into
the failure message.

### The log scrapers

Five of them, across three files:

- `parseFabricConfigs` (`scenarios/warm_standby_test.go:201`) parses
  `key=value` fields after a known prefix.
- `lastFabricConfig` (`scenarios/four_machine_storage_test.go:192`) takes the
  last of those.
- `journalOf` (`scenarios/two_machine_eventfabric_test.go:77`) finds a marker
  and reads the token after it.
- `requireReported` (`scenarios/two_machine_eventfabric_test.go:92`) is a
  substring check.
- `waitForConnectionEvent` (`scenarios/four_machine_storage_test.go:140`)
  searches JSONL lines for a type and a server field.

Each takes a `*testing.T` and asserts inside, so none of the parsing can be
tested on its own. The parsing is the part with edge cases: an unparsable
field, a marker at end of line, an empty value, a field appearing twice. Those
cases are currently only reachable through a full scenario run.

## Design

### `utils/waitfor`

```go
// Condition is polled until it reports true.
type Condition func() bool

// Abort reports that the condition can no longer become true, with a reason.
type Abort func() (aborted bool, reason string)

// Poll polls cond every interval until it returns true, until abort reports
// the wait is pointless, or until timeout. It returns nil, an AbortError, or a
// TimeoutError.
func Poll(ctx context.Context, timeout, interval time.Duration, cond Condition, abort Abort) error
```

It returns an error rather than taking a `*testing.T`. That keeps it testable
with a fake clock and no test harness, and it keeps the assertion where it
belongs: the scenario decides what a failure means and what diagnostics to
attach. It also sidesteps the `require` inside a poll goroutine hazard
entirely, since `Poll` runs the condition on the caller's goroutine.

The harness wraps it once:

```go
// scenarios/harness_test.go
func waitFor(t *testing.T, what string, cond func() bool, abort waitfor.Abort, diag fmt.Stringer)
```

Then `waitStatus`, `waitForMarker`, `waitForAPI`, `waitForManagedAPI`,
`waitForRegistration`, and `waitForConnectionEvent` all become a condition, an
abort, and a diagnostic. Every one of them gains the fail-fast-on-dead-process
behavior that only two of them have today, which is a real improvement rather
than only a tidy-up: a scenario whose machine died at startup reports the exit
in a second instead of after a minute.

### `utils/logscan`

Pure functions over a log string. No `testing` import.

```go
// Fields parses "k=v k=v" after each occurrence of prefix, in order.
func Fields(logs, prefix string) []map[string]string

// After returns the whitespace-delimited token following each occurrence of
// marker.
func After(logs, marker string) []string
```

`parseFabricConfigs` becomes a thin mapping from `logscan.Fields` output into
`fabricConfig` plus the existing assertions. `journalOf` becomes
`logscan.After` plus an assertion. `requireReported` stays as it is, since a
substring check is already one line. `waitForConnectionEvent` keeps its JSONL
matching in the scenario, since the shape it looks for is a platform contract
detail and belongs next to the scenario that asserts it.

## Changes

`utils/waitfor/` (new)
- `poll.go`, `doc.go`, tests: satisfied immediately, satisfied after N polls,
  timeout, abort before timeout, context cancellation, and that abort is
  checked before the deadline so a dead process is reported as dead rather
  than as slow.

`utils/logscan/` (new)
- `logscan.go`, `doc.go`, table-driven tests: absent prefix, multiple
  occurrences, unparsable field, marker at end of line, empty value, trailing
  carriage return. The carriage return case matters on Windows and is the kind
  of thing the current code handles by accident.

`scenarios/harness_test.go`
- `waitStatus` and `waitForMarker` rewritten on `waitFor`.
- `markerPollInterval`, `markerWaitTimeout`, `apiPollInterval`, and
  `apiWaitTimeout` stay where they are. They are scenario policy.

`scenarios/registrationapi_test.go`
- `waitForAPI` gains an abort based on the machine's process and takes the
  optional diagnostic that `waitForManagedAPI` needed.

`scenarios/warm_standby_test.go`
- Delete `waitForManagedAPI`. Call `waitForAPI`.
- `parseFabricConfigs` rewritten on `logscan.Fields`.

`scenarios/two_machine_eventfabric_test.go`
- `journalOf` rewritten on `logscan.After`.

`scenarios/four_machine_storage_test.go`
- `waitForConnectionEvent` rewritten on `waitFor`.

## Risks

- **Losing a diagnostic.** `statusDiagnostics` (`harness_test.go:635`)
  distinguishes a status file that was never written from one reporting an
  unreachable journal, and that distinction is the difference between a
  startup problem and a topology problem. It must survive the rewrite intact.
  The `lazily` mechanism (`harness_test.go:921`) exists because an eagerly
  rendered diagnostic captured process output before the failure it was meant
  to explain. Keep `waitFor` taking a `fmt.Stringer`, not a string.
- **Changed failure timing.** Adding abort to `waitForAPI` means some
  currently-slow failures become fast failures with different text. That is
  the intent. Confirm the new text is at least as informative.

## Verification

- `task test` covers both new packages in the fast gate.
- `task scenarios` passes, parallel, three times.
- Deliberately break a machine at startup and confirm `waitForAPI` reports the
  exit within a second or two instead of after `apiWaitTimeout`.
- Deliberately fail an assertion in each rewritten wait and compare the
  diagnostic against the current output side by side.

## Done when

- One poll implementation remains in the harness.
- Log parsing has unit tests that run under `-short`.
- Every wait fails fast when the process it depends on has exited.
</content>

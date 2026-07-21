# Stage 7: API transport and documentation

## Goal

Remove the repeated JSON-over-HTTP plumbing from the registration API helpers,
then bring the harness documentation back in line with what the harness
actually does.

## Why

### Transport duplication

`scenarios/registrationapi_test.go` makes four HTTP calls, and each one
repeats the same six steps: build a request with context, set headers, do it,
defer a body close, check the status, decode JSON.

- `submitRegistration` (`:116`), POST, returns value plus code plus error.
- `fetchRegistration` (`:140`), GET, returns value plus code plus error.
- `listRegistrations` (`:182`), GET, asserts on failure.
- `listConflicts` (`:197`), GET, asserts on failure.

The first two report errors instead of asserting, and the reason is documented
carefully at `:107`: a `require.*` on a `require.Eventually` goroutine calls
`runtime.Goexit`, so the poll never reports back, silently stops, and burns its
timeout while hiding the transport error that was the real failure. The last
two assert because they only run on the test goroutine.

That split is correct and should stay. What should not stay is four copies of
the plumbing beneath it.

Note that stage 6 removes the underlying hazard, since `waitfor.Poll` runs its
condition on the caller's goroutine. Keep the reporting variants anyway: a
function that returns an error is the better shape regardless, and the comment
explaining the history is worth preserving as a warning.

### Types stay where they are

`registration`, `platformInstance`, `proposalAccepted`, `conflict`, and
`processStatus` are deliberately re-declared rather than imported from the
platform. `scenarios/doc.go:122` states why: a scenario checks the published
contract, so a change to it fails here instead of silently recompiling. These
do not move to `utils` and they do not become shared. That is the point of
them.

### Documentation drift

By this stage, `scenarios/doc.go` describes a harness that no longer exists in
several places:

- `doc.go:39` to `:58`, "Where the NATS ports come from", describes reserving
  ephemeral ports and holding the listeners through rendering and building.
  Stage 1 replaces that with a fixed band outside the ephemeral range, and the
  reasoning for the band is exactly the sort of thing this section exists to
  record.
- `doc.go:89` to `:97`, "Ports, storage, and who stores what", says the harness
  "reserves ephemeral ports and a temporary directory per machine and passes
  them as runtime overrides". The port half is wrong after stage 1, and the
  runtime-override half is already contradicted by `doc.go:39` and by
  `platformConfig` (`scenarios/harness_test.go:717`), which states that the
  runtime rejects a configuration file setting NATS addresses. This section is
  stale today, before any of this plan lands.
- Nothing in `doc.go` mentions that scenarios run concurrently, or that a
  machine budget bounds them. A contributor adding a scenario needs to know
  both.

## Design

A small generic helper in the scenarios package, next to the types it decodes:

```go
// getJSON issues a GET and decodes a 200 body into T. A non-200 is returned as
// a code with no value. Transport failures are returned, never asserted, so
// this is safe to call from inside a poll.
func getJSON[T any](ctx context.Context, url string) (T, int, error)

// postJSON is the same for a JSON request body.
func postJSON[T any](ctx context.Context, url, body string) (T, int, error)
```

Then:

- `submitRegistration` becomes one `postJSON` call.
- `fetchRegistration` becomes one `getJSON` call.
- `listRegistrations` and `listConflicts` become a `getJSON` call plus the two
  `require` lines they already have.

This stays in `scenarios` rather than moving to `utils`. It is four lines of
`net/http` around a generic decode, it has no logic worth testing on its own,
and putting it in `utils` would invite production module tests to depend on it.
Accept the small duplication with any future HTTP helper elsewhere; the rule of
three has not been met.

## Changes

`scenarios/registrationapi_test.go`
- Add `getJSON` and `postJSON`.
- Rewrite the four call sites.
- Keep the comment at `:107` explaining why the poll-safe variants report
  rather than assert, updated to note that stage 6 removed the original
  trigger but that the shape is still the right one.

`scenarios/doc.go`
- Rewrite "Where the NATS ports come from" to describe the band, why it sits
  below the ephemeral range, and why ports are never reused inside a process.
- Delete the stale port and runtime-override claims from "Ports, storage, and
  who stores what". Keep the storage-selection paragraph, which is still
  accurate and still valuable.
- Add a section on concurrency: scenarios run in parallel, the machine budget
  bounds the total number of platform processes, `deploySite` charges the
  budget automatically, and a new scenario needs to do nothing except call
  `t.Parallel()`.
- Add a pointer to the `utils` packages the harness now sits on, so the next
  reader knows where the generic half went.

`taskfile/README.md`
- Note that the scenario suite runs in parallel and how to serialize it for
  debugging.

`docs/README.md`
- Add `plan/` to the "Additional material" list, or delete `docs/plan/` if the
  plan is considered spent once implemented. Decide at the end rather than
  now.

## Verification

- `task all` passes.
- Read `scenarios/doc.go` against the code, top to bottom. Every claim it
  makes should be checkable in the file it refers to. It is the entry point
  for anyone touching this suite, and a stale entry point is worse than none.

## Done when

- Each registration API call is one line of transport plus its own assertions.
- `scenarios/doc.go` contains no claim contradicted by the harness.
- The concurrency model is documented where a new scenario author will find it.
</content>

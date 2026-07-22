# Stage 80: remove the duplicate operations model

Effort: M, about 1 to 2 days. Complexity: Medium.

## Goal

Finish the cutover so the platform contains one payload contract and one
serialized wrapper.

## Operations package

Keep `internal/operations` as the local stderr and JSONL writer. Remove its event
model responsibilities.

Delete:

- `operations.Event`;
- `operations.Level` and level constants;
- generic attribute-name constants;
- `Recorder.Emit`;
- identity storage based on a partially populated `operations.Event`;
- tests that construct or decode the old wrapper.

The recorder keeps:

- concurrent serialization;
- stderr output;
- optional JSONL retention;
- path reporting;
- close, sync, and error reporting;
- context attachment and the discard recorder;
- terminal best-effort fallback for sink failures.

Its typed business-facing method uses the shared factory and writes the resulting
canonical envelope. The low-level file writer accepts only an envelope.

## Repository cleanup

Search the platform for:

- `operations.Event`;
- `Recorder.Emit`;
- `operations.Level`;
- `operations.Attribute`;
- `events.Meta`;
- `events.Record`;
- `StampRecord`;
- raw operational event type literals outside `events.go` files;
- `map[string]any` used as an event payload.

All results must be removed or justified as unrelated code.

Do not retain conversion functions or aliases for old JSON. This is a POC and
backward compatibility is explicitly out of scope.

## Tests

- Rewrite recorder tests around canonical envelopes.
- Verify stderr and JSONL receive identical bytes for one occurrence.
- Verify every line decodes as `events.Envelope`.
- Verify concurrent recording remains safe.
- Verify encoding and sink failures reach the terminal fallback.
- Verify close remains idempotent and reports sync and close errors.

## Acceptance criteria

- `events.Envelope` is the only serialized event wrapper in the platform.
- All retained events implement the canonical payload contract.
- All event output can be decoded without selecting a model first.
- No generic event attribute map remains.
- Run operations tests and repository searches, then `task all` during
  implementation.

# Multi-backend delivery semantics

## Option A: synchronous fan-out - recommended for the POC

Publication performs these steps in the caller's context:

1. Validate and stamp the typed event once.
2. Call the mandatory JSONL backend.
3. Call every additional configured backend with the same envelope.
4. Attempt all configured backends even if an earlier backend fails.
5. Return one error that preserves each backend's contextual error.

All configured backends are required. An additional backend is not a fallback.
A successful publication means every configured backend accepted the envelope.

### Pros

- Small and deterministic.
- No queues, workers, checkpoints, or shutdown draining.
- The caller immediately knows that one or more configured destinations failed.
- The identical canonical envelope is visible in every successful backend.

### Cons

- There is no transaction across JSONL and a distributed backend.
- A partial write is possible. Retrying may append another JSONL line.
- Publication latency is at least the slowest backend latency.
- A temporarily unavailable optional backend makes publication fail.

### Partial-write rule

Do not claim atomic multi-backend storage. Errors must say which backends
failed. Successful backends do not roll back. Consumers use envelope ID or the
event's stable identity to recognize repeats where that matters.

The fan-out publisher must not stamp a second envelope while processing one
call. All backend attempts for that call use the same envelope even after one
attempt fails.

## Option B: JSONL as an outbox

Publication appends to JSONL first and returns after the local durable write. A
background worker copies unprocessed envelopes to other backends and maintains a
checkpoint for each destination.

### Pros

- Producers are isolated from remote backend latency and temporary failure.
- Failed remote delivery can retry the same envelope without creating a new
  event ID.
- JSONL becomes the durable source for eventual fan-out.

### Cons

- JSONL is no longer just another equal backend; it becomes authoritative.
- Requires replay, per-backend checkpoints, retry policy, corruption handling,
  compaction or retention, and shutdown draining.
- A publish success no longer means all configured backends contain the event.
- Readiness and health need an explicit replication-lag model.

### Recommendation

Defer this option until synchronous fan-out causes a measured availability or
latency problem.

## Option C: independent producer calls

The producer calls JSONL and Event Fabric separately.

### Pros

- No fan-out component.

### Cons

- Producers know storage details.
- Each path can stamp a different envelope.
- Failure handling is duplicated and inconsistent.
- Adding a backend changes every producer.

### Recommendation

Reject this option.

## Failure and lifecycle policy

- Backend constructors fail startup if their required files, directories,
  credentials, topology, or connections cannot be initialized.
- `Store` honors caller cancellation and returns contextual errors.
- Fan-out preserves all errors rather than returning only the first one.
- Domain command code returns publication errors because the requested fact was
  not stored everywhere required by the blueprint.
- Infrastructure code may explicitly choose best-effort behavior, but that is a
  caller policy. It must not select a different publisher or backend.
- Backend failures are not published as events through the failing pipeline.
- Shutdown closes backends in reverse construction order and preserves all
  close errors.

## Ordering

The publisher guarantees only the call order seen by each backend within one
process. It does not define a global order across backends.

JSONL file order is local to one instance. Event Fabric ordering is site-wide
and remains represented by Event Fabric delivery metadata, not by the canonical
envelope.


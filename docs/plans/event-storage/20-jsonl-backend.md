# Step 20 - Mandatory JSONL backend

Effort: 0.5-1 day

Complexity: Medium

## Goal

Implement local JSONL as a normal storage backend, independent of event
production and stderr.

## Changes

1. Add `platform/internal/events/storage/jsonl` with a documented concrete
   `Backend` implementing `storage.Backend`.

2. Accept the platform instance `data_dir`, derive the fixed directory
   `<data_dir>/events`, and use the fixed file
   `<data_dir>/events/events.jsonl`.

3. Reject an empty or invalid root before creating files. Create only the
   `events` subdirectory. Return contextual path errors.

4. Append one canonical `events.Envelope` per line using `events.Encode`. Do not
   define another JSON shape or marshal the envelope independently.

5. Serialize writes inside the backend so concurrent publications cannot
   interleave JSON objects.

6. Make a successful `Store` mean that the complete line was written and synced
   to the file. If per-event `Sync` becomes a measured bottleneck later, change
   the durability contract explicitly rather than silently buffering.

7. Make `Close` sync and close the file, preserve both errors, and be
   idempotent. Reject later stores with a stable closed error.

8. Do not write events to stderr. Stderr remains available for ordinary process
   diagnostics when the event pipeline itself fails.

## Tests

- Opening creates only the expected subdirectory and file.
- One stored envelope decodes with `events.Decode`.
- Several events produce several complete lines in call order.
- Concurrent stores produce valid, non-interleaved lines.
- Invalid envelopes are rejected before a line is written.
- Write, sync, and close failures preserve their causes.
- Close is idempotent and store after close fails.
- Reopening appends rather than truncates previous events.
- Windows paths with spaces work.

## Completion criteria

- `jsonl.Backend` satisfies `storage.Backend` at compile time.
- The package contains no event catalog, factory, publisher, stderr writer, or
  blueprint types.
- Focused JSONL tests pass.


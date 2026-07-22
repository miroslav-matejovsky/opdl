# Step 90 - Full validation and plan removal

Effort: 0.5 day

Complexity: Low

## Goal

Verify the complete refactor, remove this temporary plan, and leave no partial
migration behind.

## Validation

1. Run focused tests for:

   - `internal/events`;
   - `internal/events/storage`;
   - `internal/events/storage/jsonl`;
   - `internal/events/storage/eventfabric`;
   - `internal/events/storage/nats`;
   - application composition;
   - registration and HTTP behavior;
   - builder blueprint, resolver, and deployment descriptors;
   - conformance tests.

2. Run real NATS integration tests. Confirm storage, replay, ordering,
   redelivery, deduplication, health, startup, shutdown, and restart behavior.

3. Run primary and standby scenarios. Confirm separate directories, projection
   catch-up, failover, and shutdown.

4. Inspect representative JSONL and JetStream records. Confirm they contain the
   same canonical envelope and event ID.

5. Run repository-wide searches for legacy package paths, configuration keys,
   receipt types, stale directory meanings, and ignored publication errors.

6. Run `git diff --check`.

7. Run `task all`. It must pass without changing tracked generated files.

## Cleanup

After every earlier completion criterion and validation item passes, delete the
entire `docs/plans/event-storage` directory. Do not add a completed-plan link to
any README.

If implementation remains incomplete, keep the relevant step files and record
the remaining concrete work in the repository root `.todo`.

## Completion criteria

- `task all` passes.
- The working tree contains no unintended generated or cache changes.
- No legacy storage path remains.
- This plan directory has been removed.


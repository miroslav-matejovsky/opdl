# Step 80 - Remove legacy paths and update documentation

Effort: 0.5-1 day

Complexity: Medium

## Goal

Remove obsolete storage paths and make permanent documentation describe only
the implemented design.

## Changes

1. Delete `platform/internal/operations` after all imports are gone.

2. Delete old `platform/internal/eventfabric` and
   `platform/internal/eventfabric/nats` paths after their moves are complete.
   Do not leave forwarding packages or aliases because compatibility is out of
   scope.

3. Remove `operations.event_dir` and old NATS data-directory settings from
   runtime TOML models, examples, summaries, tests, and comments.

4. Update permanent architecture documentation and package docs:

   - one producer-facing event API;
   - one canonical envelope;
   - mandatory JSONL under `<data_dir>/events`;
   - synchronous multi-backend fan-out;
   - Event Fabric as a capability;
   - NATS as its current implementation;
   - explicit `jetstream_store_dir`;
   - partial-write and bootstrap behavior;
   - backend-specific sequence remains delivery metadata.

5. Update example blueprints, deployment descriptor examples, operational
   guidance, and configuration samples. Use Windows paths only.

6. Update `.go-arch-lint.yml` to remove legacy components and enforce the new
   dependency directions.

7. Search for stale names and claims, including:

   - `operations.Recorder`;
   - `operations.event_dir`;
   - old Event Fabric import paths;
   - `eventfabric.Publisher`, `Appender`, and `Receipt`;
   - `DataDir` described as JetStream storage;
   - NATS `data_dir` instead of `jetstream_store_dir`;
   - JSONL described as optional or fallback storage;
   - events described as stderr-only.

## Tests

- Configuration rejects removed keys as unknown rather than ignoring them.
- Architecture checks enforce the new package boundaries.
- Documentation examples pass blueprint validation.
- Generated API documentation contains no removed proposal sequence.

## Completion criteria

- Legacy packages and configuration are absent.
- Permanent documentation matches code and contains no plan links.
- Repository-wide stale-name searches return no unintended matches.


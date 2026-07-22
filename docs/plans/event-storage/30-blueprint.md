# Step 30 - Blueprint and descriptor storage model

Effort: 1-1.5 days

Complexity: High

## Goal

Make the blueprint explicitly describe the platform data root and the NATS
JetStream store while preserving per-instance isolation.

## Changes

1. Change the blueprint model so each deployed instance has:

   - required `data_dir` as the general platform data root;
   - required `event_storage` for the current runtime;
   - required `eventfabric` inside `event_storage`;
   - required `nats` inside `eventfabric`;
   - required NATS `client_port`, `cluster_port`, and
     `jetstream_store_dir`.

   The structure supports another typed Event Fabric adapter later. It does not
   introduce string-based plugin configuration.

2. Apply the same shape to the primary fields under `platform` and to a deployed
   `platform.standby`. Reject all standby storage fields when standby is
   disabled.

3. Rename the meaning of descriptor `Instance.DataDir` from JetStream storage
   to the general platform data root.

4. Move resolved NATS storage onto the descriptor NATS record as
   `JetStreamStoreDir` with JSON name `jetstream_store_dir`.

5. Propagate the new field through builder resolution, embedded descriptors,
   platform descriptor decoding, descriptor conformance checks, summaries, and
   test fixtures.

6. Update validation:

   - every deployed instance requires both directories;
   - primary and standby cannot share `data_dir`;
   - primary and standby cannot share `jetstream_store_dir`;
   - the two directories on one instance may be related or unrelated;
   - do not require `jetstream_store_dir` to be below `data_dir`;
   - retain the existing port and topology checks.

7. Point the existing NATS runtime configuration at
   `descriptor.Instance.Nats.JetStreamStoreDir` immediately so the changed
   `data_dir` meaning never sends JetStream to the platform root.

8. Remove `operations.event_dir` from runtime TOML configuration. Backend
   selection and storage paths come from the blueprint. Keep runtime-only NATS
   values such as timeouts and credentials separate from the deployment
   topology. Rename their TOML section only if needed for consistency; do not
   let that section enable or disable a backend.

9. Update all example and test blueprints to the accepted nested shape and use
   distinct Windows paths for every deployed instance.

## Tests

- Valid primary-only and primary-plus-standby blueprints decode and resolve.
- Missing `data_dir`, `event_storage`, `eventfabric`, `nats`, or
  `jetstream_store_dir` fails with the full field path.
- A disabled standby rejects its storage blocks and directories.
- Shared primary and standby platform roots are rejected.
- Shared primary and standby JetStream stores are rejected.
- A JetStream store outside the platform root is accepted.
- Builder and platform descriptor models remain conformant.
- Runtime configuration cannot override blueprint storage paths or backend
  selection.

## Completion criteria

- Generated deployment descriptors show both directory purposes clearly.
- Startup summaries print `data_dir` and `jetstream_store_dir` separately.
- No comment still describes `Instance.DataDir` as the JetStream store.
- Builder, platform config, resolve, and conformance tests pass.


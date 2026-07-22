# Step 60 - Runtime composition

Effort: 1-1.5 days

Complexity: High

## Goal

Build one publisher per process from the blueprint-defined backends and manage
their lifecycle in one place.

## Changes

1. In application composition, create one `events.Factory` for the process.

2. Open the mandatory JSONL backend from the running instance's resolved
   `data_dir`. Failure to open JSONL fails process startup.

3. Construct the NATS backend from the nested Event Fabric NATS descriptor and
   runtime-only settings. Use its explicit `jetstream_store_dir`.

4. Create one fan-out publisher with this deterministic backend order:

   1. JSONL;
   2. NATS Event Fabric;
   3. future blueprint-defined backends in descriptor order.

5. Give producers only `events.Publisher`. Give projection and handler runners
   the same NATS object through `eventfabric.Fabric`.

6. Complete NATS startup using the bootstrap behavior defined in step 50. Do
   not create a second full publisher and do not register NATS twice.

7. Keep active and standby process composition independent. Each uses its own
   platform data root, JSONL file, JetStream store, factory origin, and backend
   instances.

8. Publish shutdown events before closing storage. Then close the fan-out
   publisher once. Reverse construction order closes NATS before JSONL and
   preserves every close error.

9. Make startup and shutdown errors preserve backend identity and original
   causes. Ordinary diagnostics about pipeline failure go to stderr and are not
   republished as events.

## Tests

- Every normally published runtime event reaches JSONL and NATS with the same
  envelope ID and payload.
- JSONL is first and mandatory.
- NATS is present exactly once.
- A backend failure still attempts remaining backends and returns a joined
  error.
- Primary and standby write separate JSONL files and use separate JetStream
  stores.
- Startup fails when JSONL cannot open.
- Shutdown closes all backends and preserves all close errors.
- Standby and failover scenarios retain existing Event Fabric behavior.

## Completion criteria

- Runtime composition is the only place that knows the concrete backend list.
- No producer chooses JSONL, Event Fabric, or NATS.
- Focused application composition and integration tests pass.

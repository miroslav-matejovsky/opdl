# Blueprint alternatives

## Current meaning of `data_dir`

Each deployed instance already has a required `data_dir` in the blueprint and
resolved descriptor. It currently means that instance's JetStream file-store
directory. Local JSONL is instead controlled by the runtime TOML
`operations.event_dir` and may be disabled.

The new requirement changes both meanings:

- JSONL is mandatory.
- Storage backend selection belongs to the blueprint.
- `operations.event_dir` is removed.
- NATS storage must not open the same directory as the JSONL writer.

## Option A: `data_dir` is the instance storage root - recommended

```hcl
platform {
  data_dir = "D:/opdl/customer-a/north/sensor/primary"

  event_storage {
    eventfabric {
      nats {
        client_port  = 4222
        cluster_port = 6222
      }
    }
  }
}
```

The builder derives backend paths:

```text
<data_dir>/events/events.jsonl
<data_dir>/eventfabric/nats/
```

JSONL is enabled by the required `data_dir`; it needs no block or enable flag.
The optional `event_storage.eventfabric` block enables the additional Event
Fabric destination. Its required nested `nats` block selects the current
adapter.

The Event Fabric block must contain exactly one supported adapter block. A
future adapter adds another typed block and validation that exactly one is
selected. It does not require a generic string-to-configuration registry.

For the POC, the existing mandatory `platform.nats` block can move into this
shape. Backwards compatibility is not required.

### Pros

- One authored root per instance.
- JSONL is always present and cannot be disabled accidentally.
- Backends cannot collide because their child paths are fixed.
- A future backend can receive another derived child directory.
- Primary and standby remain isolated because they already require distinct
  `data_dir` values.

### Cons

- Existing `data_dir` changes meaning from JetStream directory to storage root.
- Operators cannot place JSONL and NATS on different volumes without a later
  override.
- Moving the NATS block changes the blueprint and descriptor shape.

## Option B: explicit backend list

```hcl
event_storage {
  backend "jsonl" {
    data_dir = "D:/opdl/events"
  }
  backend "eventfabric" {
    adapter = "nats"
  }
}
```

### Pros

- Every destination is explicit.
- Each backend can have an independent storage location.
- The shape can represent many future backends without adding named blocks.

### Cons

- Validation becomes string-driven.
- Adapter-specific fields require weak maps or another nested schema.
- JSONL must be validated as exactly one required entry.
- This is a generic plugin configuration before a second backend exists.

### Recommendation

Do not use this shape during the POC. Prefer typed HCL blocks and add another
typed block when a real backend is introduced.

## Option C: retain runtime TOML backend selection

### Pros

- A deployment can switch storage without rebuilding.
- Less builder and descriptor work.

### Cons

- It violates the requirement that backends are defined by the blueprint.
- Two instances reading one runtime file can need different directories.
- The embedded descriptor no longer describes the deployed storage topology.

### Recommendation

Reject this option.

## Additional rules

- A deployed instance must have a non-empty `data_dir`.
- Primary and standby `data_dir` values must differ.
- The builder resolves backend selection into the deployment descriptor. The
  runtime does not rediscover it from loose strings.
- JSONL opens for every running instance, including passive standby instances.
- NATS storage opens only where the resolved Event Fabric topology requires it.
- Stderr is not an implicit event storage backend. Backend failures may be
  reported as ordinary process diagnostics, but must not be fed recursively
  into event publication.

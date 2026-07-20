# Stage 3: Simplify runtime NATS ownership and failover

## Outcome

Use the same NATS endpoint set for every process role. Disable the NATS monitor
listener. Make the client-only standby connect to the active machine endpoint,
then let the promoted fence owner reuse that endpoint.

This stage fixes the root cause of the observed warm standby failure.

## Complexity and estimate

- Complexity: High
- Estimate: 1 to 1.5 developer days
- Dependencies: Stage 2
- Primary areas: NATS adapter, runtime composition, configuration loading,
  redundancy integration tests

## Root cause to remove

The current WIP calls `DefaultConfig(descriptor, active)` and selects the
standby slot's NATS configuration for a client-only standby. The manifest
fixture gives that slot client port 4223. While the primary owns the fence, only
the primary server on port 4222 exists. The standby repeatedly tries an address
where no server is running and never reaches a caught-up standby status.

The in-process app test hides the defect by manually giving the standby slot a
4223 listener but a server list containing 4222. The resolver does not produce
that combination. Remove this discrepancy rather than reproducing it in more
fixtures.

## Adapter configuration

Change `platform/internal/eventfabric/nats.DefaultConfig` back to one argument:

```go
func DefaultConfig(descriptor deployment.Descriptor) (Config, error)
```

Read `descriptor.EventFabric.Nats` regardless of process role. Keep storage-node
selection and replica calculation derived from the descriptor peers.

Remove `MonitorAddress` from `nats.Config` and from all validation. Credential
exposure checks should examine only client addresses, cluster addresses,
servers, and routes.

Keep these validation rules:

- every process has at least one server address;
- only storage nodes may have routes and listener addresses;
- a storage node has valid client and cluster addresses;
- a storage node's server list contains its own client address first;
- data directory and journal limits are required only for storage owners;
- non-loopback client or route traffic requires configured credentials under
  the current authentication model.

## Embedded server options

Update `platform/internal/eventfabric/nats/server.go`:

- parse only the client address unconditionally;
- parse the cluster address only when routes are non-empty;
- do not set `HTTPHost`, `HTTPPort`, `HTTPSPort`, or profiling options;
- assert in tests that `HTTPPort == 0` and `HTTPSPort == 0`;
- keep client authentication and route construction behavior unchanged unless
  a separate reviewed security change is made.

The cluster listener must remain conditional on `len(cfg.Routes) > 0`.
Configuring a cluster port in the blueprint is not permission to bind it when
the site topology has no server peer.

## Active and standby composition

Change `platform/internal/app/site.go` as follows:

1. Remove the `active` parameter from `natsConfig`.
2. Compose the same adapter configuration for primary, standby, promotion, and
   reclamation.
3. Keep `clientOnly` for a process that does not hold the fence.
4. `clientOnly` must retain the full resolved server list.
5. `clientOnly` must clear server ownership, local listener fields, routes, and
   the data directory.
6. When the fence is acquired, close the client-only fabric before opening the
   active fabric.
7. Active open then binds the shared endpoint and reconnects.

No process-role branch may select a different server list or listener address.

Keep the existing fence ordering as an invariant:

```text
active closes HTTP and Event Fabric
active embedded NATS stops
active releases fence
waiter acquires fence
waiter opens embedded NATS on the same endpoints
waiter catches up and serves HTTP
```

If a bind fails after fence acquisition, record a failed status with the bind
error. Do not fall back to a random or alternate port.

## Strict runtime configuration

The WIP removed NATS socket override fields from the runtime TOML schema, but
BurntSushi TOML currently ignores unknown keys. The scenario configuration
continues to write `client_address`, `cluster_address`, `monitor_address`,
`routes`, and `servers`; they are silently ignored. This made the failure slow
and hard to diagnose.

Change `platform/internal/config/file.go` to reject undecoded TOML keys. Use
decoder metadata and return an error that lists the unknown qualified keys. Add
tests for an obsolete NATS socket override and a general unknown top-level key.

Update `platform/config.toml` to remove the obsolete override comments. Runtime
TOML should contain only site-owned values such as storage placement, timeouts,
instance directory, API address, and secret-file location.

## Focused tests

Update or add tests in:

- `platform/internal/eventfabric/nats/config_test.go`
- `platform/internal/eventfabric/nats/nats_test.go`
- `platform/internal/app/app_test.go`
- `platform/internal/config/config_test.go`
- `platform/internal/redundancy` tests when lifecycle assertions change

The tests must prove:

- no monitor listener is configured;
- a one or two machine site has no cluster listener configuration;
- a three storage node site has cluster routes;
- active and standby use the same resolved server list;
- client-only conversion retains every storage server and binds nothing;
- a standby becomes caught up while the primary is active;
- promotion reuses the same client and cluster addresses;
- preferred-primary reclamation reuses the same addresses;
- unknown TOML socket overrides fail at load time;
- the process records useful status on connection or bind failure.

Avoid hard-coded ports in integration tests. Continue using reserved loopback
ports in tests that construct descriptors directly.

## Operational logging

At startup, log the effective descriptor-derived client address, whether a
cluster listener is required, the server list, storage ownership, and replica
count. Never log credentials. Do not log a monitor address because none exists.

This output must make it possible to distinguish:

- active storage server;
- client-only local standby;
- client-only non-storage machine;
- storage activation after promotion.

## Exit criteria

- `DefaultConfig` has no process-role input.
- A standby connects to the active endpoint produced by the resolver.
- Promotion and reclamation reuse the same endpoint set.
- NATS starts no monitoring listener.
- Obsolete runtime socket keys fail fast instead of being ignored.
- Focused platform tests pass.


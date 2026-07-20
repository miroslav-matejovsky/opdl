# Stage 1: Freeze the NATS and standby contract

## Outcome

Adopt one explicit machine-level NATS configuration and one mandatory standby
policy. Do not assign separate NATS listeners to the primary and standby
processes.

Target blueprint:

```hcl
platform {
  nats {
    client_port  = 4222
    cluster_port = 6222
  }

  standby {
    disabled = false
  }
}
```

Target primary-only blueprint:

```hcl
platform {
  nats {
    client_port  = 4222
    cluster_port = 6222
  }

  standby {
    disabled = true
  }
}
```

The builder combines each port with `machine.ip` by using `net.JoinHostPort`.
The resulting addresses are embedded in the deployment descriptor. The
standby block and its `disabled` attribute are always required.

## Complexity and estimate

- Complexity: Medium
- Estimate: 0.5 developer day
- Dependencies: None
- Blocks: All later stages

## Decisions

### Keep the NATS client endpoint

The client endpoint cannot be removed from the current architecture.

- The active platform process uses the NATS client protocol to publish and
  consume Event Fabric records.
- A local warm standby connects as a NATS client while it follows the journal.
- Machines that are not selected as JetStream storage nodes connect as NATS
  clients to the selected storage nodes.
- The NATS cluster endpoint does not accept client protocol connections. It is
  only for server-to-server routes between storage nodes.

Name the field `client_port`, not `local_client_port`. On a storage node the
listener must be reachable by other platform machines. Calling it local would
hide an important security and firewall requirement.

### Keep the cluster endpoint, but bind it only when routes exist

The cluster port is needed only when the site has three selected storage nodes.
The existing topology selects one storage node for sites with one or two
machines and three storage nodes for larger sites.

The descriptor may always contain `cluster_address`, but the runtime must create
the cluster listener only when the resolved route list is non-empty. Therefore:

- one-machine site: one NATS client listener, no cluster listener;
- two-machine site: one NATS client listener on the selected storage machine,
  no cluster listener;
- site with three or more machines: one client and one cluster listener on each
  of the three selected storage machines;
- non-storage machine: no embedded NATS listeners.

### Remove NATS monitoring

Remove `monitor_address` from the blueprint, both descriptor models, runtime
configuration, adapter configuration, examples, fixtures, and documentation.
Leave `server.Options.HTTPPort` and `server.Options.HTTPSPort` at zero. NATS
then starts no HTTP monitoring listener.

The runtime does not depend on the NATS HTTP monitor. It already reads
connection, journal high-water, projection progress, and lag through the Event
Fabric client API and writes them to local process status files. Those status
files are the immediate operational replacement. A future remote operational
API should expose sanitized platform-owned state, not proxy the NATS monitor.

### Share endpoints between primary and standby

The primary and standby are mutually exclusive server owners. The machine fence
is released only after active resources, including the embedded NATS server,
are closed. The process that next acquires the fence can safely bind the same
client and cluster endpoints.

Shared endpoints have these properties:

- the client-only standby connects to the currently active server address;
- promotion does not change the address advertised to other machines;
- returning primary processes connect to the promoted server without needing a
  second endpoint list;
- the firewall has one client and at most one cluster port per storage machine;
- route and server lists do not need primary and standby variants.

Separate slot endpoints are rejected. They double the endpoint inventory and
require every client and route list to contain both inactive and active slot
addresses. The current WIP does not do that. It gives the standby its own
inactive client address, which is why the warm standby scenario cannot reach
the journal.

### Derive routes and server lists

Do not expose `routes` or `servers` in authored HCL. They are consequences of
the site topology:

- `servers` is the ordered client-address list of selected storage machines;
- `routes` is the cluster-address list of the other selected storage machines;
- a selected storage machine lists its own client address first;
- order is deterministic by machine name.

Allowing authors to override these lists can silently split a site, omit a
storage node, or point a machine at a different journal. Keep topology derived
and validate it in the resolved descriptor.

### Use numeric ports

Use HCL numbers and Go `int` fields. Validate the range `1..65535` and require
`client_port != cluster_port`. Numeric ports avoid accepting whitespace and
non-numeric strings that are invalid by construction.

## Security boundary

Port reduction does not make the current non-loopback transport production
secure by itself.

- Bind only to the exact `machine.ip`. Never derive `0.0.0.0` or `::`.
- Restrict the client port to OPDL machines that need Event Fabric access.
- Restrict the cluster port to the selected storage machines.
- Keep NATS credentials outside the descriptor and blueprint.
- Document that the current username/password configuration does not encrypt
  client or cluster traffic.
- Track TLS or mutual TLS for both client and route traffic before claiming that
  non-loopback NATS is suitable for a hostile or untrusted network.

Network ACLs and TLS are deployment security controls. They are not reasons to
add more NATS listeners.

## Open questions and recommendations

### Is `machine.ip` always the NATS-reachable interface?

Recommendation: Treat it as the canonical platform interface and use port-only
HCL. This matches the original resolver and removes duplicated host values. If a
machine needs separate identity, bind, and advertised addresses, stop and model
those concepts explicitly. Do not reintroduce generic runtime overrides.

### Should a single-machine primary-only deployment use no client listener?

NATS supports `server.Options.DontListen` and `nats.InProcessServer`. That could
remove the last NATS port only when there is no standby and no remote client.

Recommendation: Defer it. It creates a topology-specific connection path for a
small special case. First land the uniform shared-endpoint model and measure the
value of the extra branch.

### Should `standby` use `enabled` instead of `disabled`?

Recommendation: Keep `disabled` because it matches the requested explicit
opt-out. Make the attribute required so omission cannot silently enable or
disable redundancy.

### Should a platform monitoring endpoint be added now?

Recommendation: Do not add another network endpoint in this initiative. Use the
existing status files for local operational monitoring. Design any remote admin
API separately with authentication, authorization, and a minimal sanitized
schema.

## Exit criteria

- The contract above is copied into the architecture documentation.
- Every later stage uses shared machine-level NATS endpoints.
- No later stage introduces authored route lists, server lists, monitor ports,
  or per-slot NATS listeners.


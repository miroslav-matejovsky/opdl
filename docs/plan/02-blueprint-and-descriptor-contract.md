# Stage 2: Implement the blueprint and descriptor contract

## Outcome

Make NATS ports and standby intent explicit in every blueprint. Resolve one
machine-level NATS topology into every deployment descriptor. An omitted
platform, NATS, standby, or standby decision must fail validation.

## Complexity and estimate

- Complexity: High
- Estimate: 1 to 1.5 developer days
- Dependencies: Stage 1
- Primary areas: `builder/internal/blueprint`, `builder/internal/resolve`, both
  `deployment` packages, pack metadata, descriptor conformance

## Blueprint model

Change `builder/internal/blueprint/topology.go` to this logical model:

```go
type Platform struct {
    Nats    *Nats    `hcl:"nats,block"`
    Standby *Standby `hcl:"standby,block"`
}

type Nats struct {
    ClientPort  int `hcl:"client_port"`
    ClusterPort int `hcl:"cluster_port"`
}

type Standby struct {
    Disabled bool `hcl:"disabled"`
}
```

Do not include `monitor_address`, `routes`, `servers`, or a standby `nats`
block.

Validation must reject:

- missing `platform` block;
- missing `platform.nats` block;
- missing `platform.standby` block;
- missing `platform.standby.disabled` attribute at HCL decode time;
- client or cluster port outside `1..65535`;
- equal client and cluster ports;
- invalid or unspecified `machine.ip` under the existing machine rules.

Keep validation errors fully qualified, for example
`machine "node": platform.nats.client_port must be in range 1-65535`.

Update `builder/internal/blueprint/doc.go` with both enabled and disabled
examples. State that a false `disabled` value enables a second local process,
not a second NATS endpoint.

## Resolved descriptor model

Keep slot intent explicit and non-null in JSON. Replace the current optional
standby pointer with a mandatory resolved slot record:

```go
type Slots struct {
    Primary Slot `json:"primary"`
    Standby Slot `json:"standby"`
}

type Slot struct {
    Disabled bool `json:"disabled"`
}
```

Resolution always sets `Primary.Disabled` to false and copies the authored
standby decision into `Standby.Disabled`. Descriptor validation must reject a
disabled primary.

Move NATS out of the slots because the fence owner shares one machine-level
endpoint set:

```go
type EventFabric struct {
    Nats  EventFabricNats   `json:"nats"`
    Peers []EventFabricPeer `json:"peers"`
}

type EventFabricNats struct {
    ClientAddress  string   `json:"client_address"`
    ClusterAddress string   `json:"cluster_address"`
    Routes         []string `json:"routes"`
    Servers        []string `json:"servers"`
}
```

Use the same JSON shape in `builder/deployment/deployment.go` and
`platform/deployment/deployment.go`.

The platform descriptor unmarshaller must require all of these fields to be
present, including `slots.primary.disabled`, `slots.standby.disabled`, and
`event_fabric.nats`. Missing JSON must not decode into a valid false value by
accident.

## Resolver algorithm

Refactor `builder/internal/resolve/resolve.go` around one site-wide derivation.

1. Sort site machines by name.
2. Select the first machine for sites of size one or two, otherwise the first
   three machines, using the existing storage rule.
3. For every machine, derive its local addresses with
   `net.JoinHostPort(machine.IP, strconv.Itoa(port))`.
4. Derive `servers` from the selected storage machines' client addresses.
5. Put the current machine's client address first when it is a storage machine.
6. For a storage machine, derive `routes` from the other selected storage
   machines' cluster addresses.
7. For a non-storage machine, resolve an empty route list.
8. Resolve empty lists as non-null empty JSON arrays to keep descriptors
   deterministic and explicit.

Remove `machineSlotNats`, `peerSlotNats`, the `primary bool` switches, and all
authored route/server override branches. There is only one NATS topology per
machine.

Validate resolved invariants:

- at least one server exists;
- all addresses are valid host-port values;
- routes and servers contain no duplicates;
- a storage machine's own client address is first in `servers`;
- no route points to the current machine's cluster address;
- non-storage machines have no routes;
- a site smaller than three machines has no routes;
- all peers and derived lists remain deterministic under declaration reorder.

## Packaging and conformance

Update packaging to use `!descriptor.Slots.Standby.Disabled` when deciding
whether to emit the optional standby launch. Keep the primary launch mandatory.

Update `conformance-tests/deployment-descriptors/descriptors.go` to prove that:

- builder JSON contains primary and standby slot records;
- both `disabled` fields are present;
- machine-level NATS data round-trips into the platform descriptor;
- monitor data is absent;
- enabled and disabled standby policies survive the round trip.

Regenerate `platform/embedded/deployment.json` through the repository's normal
generation path during implementation. Do not edit generated JSON by hand if a
generator owns it.

## Files to update

- `builder/internal/blueprint/topology.go`
- `builder/internal/blueprint/topology_test.go`
- `builder/internal/blueprint/load_test.go`
- `builder/internal/blueprint/doc.go`
- `builder/internal/blueprint/testdata/project.hcl`
- `builder/internal/resolve/resolve.go`
- `builder/internal/resolve/resolve_test.go`
- `builder/deployment/deployment.go`
- `builder/deployment/deployment_test.go`
- `builder/internal/pack/metadata.go`
- `builder/internal/pack/pack.go`
- related pack tests
- `platform/deployment/deployment.go`
- `platform/deployment/deployment_test.go`
- `conformance-tests/deployment-descriptors/descriptors.go`
- `platform/embedded/deployment.json`
- every HCL example and scenario fixture

## Required tests

Add or update focused tests for:

- HCL enabled standby;
- HCL explicit disabled standby;
- missing standby block;
- missing disabled attribute;
- missing NATS block;
- invalid ports and colliding ports;
- deterministic storage, server, and route derivation;
- one, two, three, and four machine site topology;
- descriptor presence checks;
- manifest launch presence for enabled and disabled standby;
- builder-to-platform JSON conformance.

Do not retain tests for authored `routes`, authored `servers`, monitor addresses,
or separate standby NATS blocks.

## Exit criteria

- Every checked-in blueprint has mandatory `platform.nats` and
  `platform.standby` blocks.
- The descriptor contains one machine-level NATS topology.
- The descriptor contains explicit primary and standby slot decisions.
- No descriptor or manifest reader infers standby policy from field omission.
- Focused builder, pack, deployment, and conformance tests pass.


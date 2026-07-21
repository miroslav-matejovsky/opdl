# Stage 04: Independent configuration per instance

**Effort:** Medium. **Complexity:** High. **Depends on:** stages 01-03.

The first stage that changes what a machine binds. Small change, large blast
radius.

## Intent

Each instance has its own configuration context (goal:67) and its own network
ports (goal:68). Nothing a single instance binds or writes is described in a
place both instances read.

## Why the shared configuration no longer makes sense

The deployment descriptor already resolves everything an instance binds onto that
instance:

```json
"instances": {
  "primary": {"api_address": "127.0.0.1:8080", "nats": {...}},
  "standby": {"api_address": "127.0.0.1:8081", "nats": {...}}
}
```

The platform ignores all of it. `platform/internal/config/config.go` loads one
TOML file per machine and `Config.Address()` returns `address` from that file.
Both instances load the same file, so both would bind the same API address, and
only the Active one binds at all.

That is two sources of truth for the same value, and they can disagree with no
symptom. The repo already refuses this shape for the NATS sockets: `config/file.go`
rejects a configuration file that sets them, on the stated reasoning that a
silently dropped setting looks exactly like an applied one. The API address is the
same problem and has not been closed. `instance_dir` is the same problem in a
quieter form: it is not a listener, so a wrong value does not fail to bind — two
instances just write over each other's evidence.

## What is genuinely per-machine and must stay shared

Not everything in the TOML is an instance's. Sorting this correctly is the whole
design of the stage.

| Setting | Whose | Why |
| --- | --- | --- |
| `address` | instance | Each binds its own; the descriptor already states it |
| `instance_dir` | instance | Each writes its own status file; see D3 below |
| `event_fabric.nats.data_dir` | see stage 05 | One journal per machine today; two servers changes this |
| `credentials_file` | machine | A site credential, identical for both |
| `read_header_timeout`, `shutdown_timeout` | either | No conflict; simplest to keep machine-level |
| `lag_bound` | either | A policy, not a resource |
| `operations.event_dir` | machine | Both append; records carry the instance role |

So the split is not "give each instance a whole config file". It is: **remove from
the file everything the descriptor already states, and let the runtime read its own
instance's record.**

## The API is loopback-only, and the blueprint says so

This is the answer to D1, and it is authored rather than merely documented.

The blueprint attribute is named `local_port`, not `port`:

```hcl
platform {
  api { local_port = 8080 }

  standby {
    api { local_port = 8081 }
  }
}
```

The builder joins it with `127.0.0.1`, never with the machine's `ip`. A reader of
the blueprint sees the constraint in the name of the thing they are authoring, so
there is no way to author an API port while believing it will be reachable from
the network.

**Nothing about this stage exposes anything.** The earlier draft of this stage
treated the descriptor address as routable and called that a security-relevant
change; it is not one, because the address is now loopback by construction. The
platform API is a machine-local interface: it is how an operator or a co-located
service on that host asks this instance about itself. Cross-machine traffic is the
Event Fabric's, and the Event Fabric's ports are the ones derived from the machine
`ip`.

The descriptor carries the consequence, so it is checkable rather than assumed:
`instances.*.api_address` must have a loopback host, and descriptor validation
rejects one that does not.

### The port map stays strict

An API port on `127.0.0.1` and a NATS port on the machine `ip` can be the same
number without either failing to bind, so `validateMachinePorts` is now
technically over-strict. It keeps rejecting a repeat anyway. A machine's port map
stays readable as one list, an over-strict rule fails loudly at build time, and
moving the API off loopback later cannot turn a latent collision into a runtime
failure. Rejecting a valid deployment is the affordable direction of that trade.

### `peers[].api_address` is dropped

A peer's API address was the machine `ip` joined to its API port. Under
loopback-only that value would be `127.0.0.1:8080` for every machine in the site,
which read from another machine points the reader at itself, and which descriptor
validation would reject as a duplicate listener address across the site.

Keeping it as `ip:port` would be worse: an address that no process listens on,
carried in the descriptor as though it did. That is the exact shape the repo
already refuses for NATS socket overrides.

So `Peer` loses `api_address` in both descriptors. Peers exist for Event Fabric
membership, and the API is not a site concern. Nothing in the runtime read the
field; only descriptor validation and the conformance fixtures did.

## `instance_dir` becomes the instance's `runtime_dir`

This reverses the earlier D3 recommendation. The reasoning is below; the decision
record is D3.

Each deployed instance authors its own directory in the blueprint:

```hcl
platform {
  runtime_dir = "C:/ProgramData/opdl/customer-a/north/local-server/primary"
  api         { local_port = 8080 }

  standby {
    runtime_dir = "C:/ProgramData/opdl/customer-a/north/local-server/standby"
    api         { local_port = 8081 }
  }
}
```

It is a plain attribute rather than a block. Every other authored platform policy
is a block because it groups several values; this is one path, and `runtime_dir =`
reads the way `ip =` does.

The name is `runtime_dir` because the runtime already calls it that internally —
`redundancy.StatusPath(runtimeDir, ...)`, "the local runtime directory" — and it
stays distinct from `data_dir`, which is the journal's storage, and `event_dir`,
which is operational event retention. `workdir` was considered and rejected: it
suggests scratch space or a working directory, and this is neither.

### What this simplifies

`StatusPath` composed a machine-scoped subdirectory and a role-scoped filename out
of a shared root:

```text
<instance_dir>/<project>-<environment>-<site>-<machine>/process-<role>.status
```

Every one of those qualifiers existed to keep two instances, and several machines
on one host, from colliding inside one shared directory. With the directory
authored per instance, the qualification is in the authored path and the
composition collapses to:

```text
<runtime_dir>/process.status
```

`StatusPath` takes one argument. `machineDir` goes away.

The collision it was protecting against does not: the builder rejects a machine
whose two instances author the same `runtime_dir`, the same way it rejects two
instances sharing a port or a service name. That is the check that keeps two
processes from writing one status file.

Two *machines* authoring the same `runtime_dir` is not rejected, for the same
reason stage 03 does not reject a duplicate `windows_mutex` across machines: two
machines are two hosts. It is only dangerous when several machines run on one
host, which is the scenario harness, and the harness renders a distinct directory
per instance for the same reason it renders a distinct mutex name.

## Target

`address` and `instance_dir` leave the TOML file. The runtime resolves both from
its own instance's descriptor record.

`Config` gains nothing. Neither `Load` becoming role-aware nor a role-taking
accessor on `Config` is right: the runtime is already handed its descriptor
explicitly, so `app.instanceOf(descriptor, role)` returns the running instance's
own record and both values are read from there. A `Config` accessor would be a
second route to the same field, and whichever one a caller reached for would be
the one that could be wrong. `Config` keeps only what the TOML supplies.

The role is known before anything is composed: `app.go` resolves it from
`-instance`, after loading the descriptor, because `resolveRole` has to know
whether the machine deploys a standby at all.

An unknown key in the configuration file is already a startup error, so a
deployment that still sets `address` or `instance_dir` fails loudly rather than
being ignored. That existing behavior is what makes this change safe to ship.

The startup summary states both instances' addresses and marks which one this
process is, so a two-instance machine's two logs are told apart at a glance.

## What this stage deliberately does not do

Under the goal architecture each instance binds its own port for its whole
lifetime, so an operator can ask a Passive instance about itself. Today only the
Active instance listens, and **after this stage that is still true.**

Serving from a Passive instance is a separate design question, not a consequence
of splitting the configuration. It must not serve the full API: it holds no
ownership, its projection is not authoritative, and answering domain queries from
it would make the ownership rule meaningless. Deciding which routes it does answer,
and opening its listener at startup rather than on activation, is carried out of
this stage. See the carried findings in [README](README.md).

What this stage delivers is that the Passive instance's address is already
resolved, already its own, and already distinct from the Active instance's — so
the follow-up is a listener and a handler, not another configuration change.

## Decisions

**D1. Addressing.** Settled: loopback-only, authored as `api { local_port }` and
resolved to `127.0.0.1:<local_port>`. Descriptor validation enforces the loopback
host. `peers[].api_address` is dropped.

**D2. What does a Passive instance's API answer?** Deferred with the listener
itself; see above. The recommendation stands for whoever picks it up: health,
status, and the instance's own identity, with every domain route returning `503`
and a body naming the Active instance's address. Under D1 that address is on
loopback, and both instances share the host, so it remains a followable pointer.

**D3. Does `instance_dir` stay shared?** Settled: no. It becomes `runtime_dir`,
authored per instance in the blueprint and carried on the instance's descriptor
record. This reverses the earlier recommendation in this document, which argued a
shared directory is what lets an operator see the pair. That is now the authored
paths' job: two sibling directories under one parent show the pair just as well,
and they do it without two processes writing into one directory. Stage 08's ruling
on filesystem state is unaffected — status files remain operational evidence and
take no part in the ownership decision.

**D4. Do the timeouts and `lag_bound` become per-instance?** Recommendation: no.
They are policy, they are identical in every deployment, and splitting them adds
configuration nobody would vary.

## Work

1. Blueprint: rename `api.port` to `api.local_port`; add `runtime_dir` to
   `platform` and to `platform.standby`, required when the instance is deployed
   and rejected when it is not. Reject a machine whose two instances share a
   `runtime_dir`.
2. Resolver: join the API port with `127.0.0.1` rather than the machine `ip`;
   carry `runtime_dir` onto each instance record; stop resolving
   `peers[].api_address`.
3. Both descriptors, in lockstep: add `instances.*.runtime_dir`, remove
   `peers[].api_address`, and validate that an API address is on loopback and that
   the two instances' runtime directories differ. The conformance check in
   `conformance-tests/deployment-descriptors` fails if only one side moves.
4. Remove `address` and `instance_dir` from `config/file.go`, from
   `platform/config.toml`, and from every configuration fixture, including
   `scenarios/harness_test.go`.
5. Make the API address and the runtime directory come from the running
   instance's descriptor record, through `app.instanceOf`, and drop
   `Config.Address` and `Config.InstanceDir`.
6. Collapse `StatusPath` to `<runtime_dir>/process.status` and delete `machineDir`.
7. Update the startup summary: name both instances' addresses and mark which one
   this process is.
8. Update `examples/customer-a/project.hcl`,
   `builder/internal/blueprint/testdata/project.hcl`,
   `platform/embedded/deployment.json`, and `scenarios/testdata/project.hcl.tmpl`,
   where the runtime directory becomes a per-run, per-instance path.
9. Update `docs/operations/deployment.md` for the new configuration file shape,
   `docs/operations/monitoring.md` for the status file path, and
   `docs/operations/troubleshooting.md`.

## Validation

- `task all` passes. The scenario suite is still off; see the note below.
- A blueprint authoring `api { port = ... }` fails to load, because the attribute
  no longer exists.
- A machine whose two instances share a `runtime_dir` is rejected at build time
  with a message naming both.
- A descriptor whose `api_address` is not on loopback is rejected.
- A configuration file that still sets `address` or `instance_dir` fails startup
  with the unknown-key error.
- Both instances of a machine write their status files to their own directories,
  and neither path depends on the other's.
- Failover does not change either instance's API address, because neither instance
  ever binds the other's. `TestFailoverAndFailback` asserts both directions: the
  taking-over instance serves on its own address, and the stopped instance's
  address answers nothing.

### What the scenario suite is still blocked on

The harness was updated for this stage: the blueprint template renders each
instance's `local_port` and `runtime_dir`, API ports are reserved on `127.0.0.1`
for the whole site rather than per machine ip, and the configuration file it
writes no longer states an address or a directory. It compiles and the
standby-less scenarios pass.

The suite as a whole does not, and not because of this stage. The first run
surfaced a stage 03 defect: the blueprint authors a `Global\`-prefixed mutex name
and `winmutex.Open` applies that prefix itself and rejects a name containing a
backslash, so every machine deploying a standby fails at startup. It is recorded
in [README](README.md) as an unowned carried finding. Restoring the gate means
fixing that first.

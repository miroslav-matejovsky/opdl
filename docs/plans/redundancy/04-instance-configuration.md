# Stage 04: Independent configuration per instance

**Effort:** Medium. **Complexity:** High. **Depends on:** stages 01-03.

The first stage that changes what a machine binds. Small change, large blast
radius.

## Intent

Each instance has its own configuration context (goal:67) and its own network ports
(goal:68). Nothing a single instance binds is described in a place both instances
read.

## Why the shared configuration no longer makes sense

The deployment descriptor already resolves everything an instance binds onto that
instance:

```json
"instances": {
  "primary": {"api_address": "10.0.1.11:8080", "nats": {...}},
  "standby": {"api_address": "10.0.1.11:8081", "nats": {...}}
}
```

The platform ignores all of it. `platform/internal/config/config.go` loads one TOML
file per machine and `Config.Address()` returns `address` from that file. Both
instances load the same file, so both would bind the same API address, and only the
Active one binds at all.

That is two sources of truth for the same value, and they can disagree with no
symptom. The repo already refuses this shape for the NATS sockets: `config/file.go`
rejects a configuration file that sets them, on the stated reasoning that a
silently dropped setting looks exactly like an applied one. The API address is the
same problem and has not been closed.

## What is genuinely per-machine and must stay shared

Not everything in the TOML is an instance's. Sorting this correctly is the whole
design of the stage.

| Setting | Whose | Why |
| --- | --- | --- |
| `address` | instance | Each binds its own; the descriptor already states it |
| `instance_dir` | machine | Both write status files there; it is a directory, not a listener |
| `event_fabric.nats.data_dir` | see stage 05 | One journal per machine today; two servers changes this |
| `credentials_file` | machine | A site credential, identical for both |
| `read_header_timeout`, `shutdown_timeout` | either | No conflict; simplest to keep machine-level |
| `lag_bound` | either | A policy, not a resource |
| `operations.event_dir` | machine | Both append; records carry the instance role |

So the split is not "give each instance a whole config file". It is: **remove from
the file everything the descriptor already states, and let the runtime read its own
instance's record.**

## Target

`address` leaves the TOML file. The runtime resolves its own API address from
`descriptor.Instances.Get(role).APIAddress`.

`config.Load` becomes role-aware, or `Config` gains an accessor that takes the
role. The role is already known at startup: `app.go` resolves it from `-instance`
before anything is composed.

An unknown key in the configuration file is already a startup error, so a
deployment that still sets `address` fails loudly rather than being ignored. That
existing behavior is what makes this change safe to ship.

## The consequence nobody should discover at runtime

The TOML address is typically `127.0.0.1:8080`, which binds loopback only. The
descriptor's address is derived from the machine's `ip`, which is routable.

**Taking the descriptor's address exposes the platform API on the network** for
every deployment that was previously loopback-only. That is a security-relevant
change and it is the reason this stage was not folded into the builder work.

Options, and this is decision D1:

- Bind the descriptor address as-is, and treat network exposure as intended. The
  API is how services on other machines would reach the platform, so this may be
  the actual intent.
- Keep a bind-host override in the TOML, so the port comes from the descriptor and
  the interface stays a site decision. Reintroduces a small second source of truth,
  but only for the host, never the port.
- Derive from the descriptor and require the API to be authenticated before any
  non-loopback bind, mirroring what the Event Fabric adapter already does for NATS
  credentials.

## Both instances now bind

Today only the Active instance listens. Under the goal architecture each instance
binds its own port for its whole lifetime, so an operator can ask a Passive
instance about itself.

That means a Passive instance serves *something*. It must not serve the full API:
it holds no ownership, its projection is not authoritative, and answering domain
queries from it would make the ownership rule meaningless.

Recommendation: a Passive instance serves health and status only, and returns a
clear refusal on every domain route. Which routes those are is decision D2.

## Decisions

**D1.** Which addressing option above? This is the security decision in the stage.

**D2.** What does a Passive instance's API answer? Recommendation: health, status,
and the instance's own identity. Every domain route returns `503` with a body that
names the Active instance's address, so a caller can follow it.

**D3.** Does `instance_dir` stay shared? Recommendation: yes. Both instances
writing separate files into one directory is what lets an operator see the pair,
and it is not coordination; see stage 08's ruling on filesystem state.

**D4.** Do the timeouts and `lag_bound` become per-instance? Recommendation: no.
They are policy, they are identical in every deployment, and splitting them adds
configuration nobody would vary.

## Work

1. Remove `address` from `config/file.go` and from every configuration fixture,
   including `scenarios/harness_test.go`.
2. Make the API address come from the running instance's descriptor record.
3. Bind per D1.
4. Serve the Passive instance's reduced API per D2, with its own listener that is
   opened at startup rather than on activation.
5. Update the startup summary so it states which address this instance is on and
   which the other instance is on.
6. Update `docs/operations/deployment.md` for the new configuration file shape and
   the exposure change from D1.

## Validation

- `task all` passes with the gate enabled.
- A scenario starts both instances of one machine and reaches each on its own API
  port at the same time.
- The Passive instance answers health and refuses a domain route with a pointer to
  the Active address.
- A configuration file that still sets `address` fails startup with a message that
  names the descriptor as the source.
- Failover does not change either instance's API address, because neither instance
  ever binds the other's.

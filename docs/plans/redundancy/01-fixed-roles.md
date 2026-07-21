# Stage 01: Fixed roles and instance identity

**Effort:** Medium. **Risk:** Medium. **Depends on:** nothing.

The foundation. Every later stage assumes the role model this stage settles.

## Status: done

`task all` passes. What shipped:

- `redundancy.ProcessRole` is `redundancy.InstanceRole`, in `role.go`. Values
  `primary` and `standby` are unchanged, as are the `-instance` flag, the status
  file names, and the `role` on every operational event.
- `manifest.role` is `manifest.machine_role`, removing the collision with the
  instance role.
- A `winservice` block per deployed instance in the blueprint, carried through the
  descriptor's slots into `manifest.json`.
- Each instance prints its own service name in its startup summary.
- `docs/operations/deployment.md` section 6 names the services rather than only
  their arguments; `docs/01-architecture.md` gains a fixed-instance-roles section.

### Deviation from D1: names are authored, not derived

The plan recommended deriving service names as the ownership object is derived.
They are authored instead, in `platform.winservice` and
`platform.standby.winservice`.

The reasoning that makes derivation right for the ownership object does not carry
over. A duplicate ownership object is a silent split brain, so a blueprint must not
be able to state one. A duplicate service name is different: names need only be
unique on one host, two machines are two hosts, and a real collision is an
installation Windows refuses. The failure is loud, so readability wins.

The one collision that is not loud is a machine naming its own two instances the
same, because those really do share a host. Both the builder's blueprint
validation and the descriptor's `Validate` reject it.

### Not implemented, deliberately

No Service Control Manager integration, no installation, no start or stop. The
blueprint blocks and the manifest fields are a declaration for whoever installs
the services, and a visible statement that the platform is intended to run under
the Service Control Manager. Nothing in the runtime acts on them; it only prints
the name so a process can be matched to a service. D3 answered as recommended.

## Intent

Each local process has a fixed role. There are exactly two: **Primary Instance**
and **Standby Instance**.

The roles are static and intentional:

- decided at build time from the machine's blueprint, not at runtime;
- carried in the deployment package;
- named in the Windows Service that runs the process (windows process is not important for now, focus only on process);
- unchanged for the life of the installation.

A Standby Instance that takes over does not become the Primary Instance. It
operates in the Active state while the Primary Instance is unavailable, and
returns to Standby when ownership goes back. The role is the identity; the state
is the situation. Stage 02 covers states.

This stage is first because it is what operations sees: the service in the
services list, the arguments in the manifest, the file it reads status from, and
the `role` field on every operational event.

## Current state

### What is already right

The two roles exist, are static, and are already named primary and standby.

| Element | Location | Note |
| --- | --- | --- |
| Role values `primary`, `standby` | `platform/internal/redundancy/slot.go:10` | already the target words |
| Launch flag `-instance primary\|standby` | `builder/internal/pack/metadata.go:42` | already uses "instance" |
| Role decided from the descriptor, not runtime | `platform/internal/app/runtime.go` `resolveRole` | already static |
| A machine with no standby rejects the standby role | `runtime.go` `resolveRole` | already enforced |
| Per-role status files | `redundancy.StatusPath` | `process-primary.status`, `process-standby.status` |
| `role` on every operational event | `platform/internal/operations` | already present |

The intention is largely implemented. What is missing is the operator-facing
identity and one naming collision.

### Gap 1: there is no Windows Service identity anywhere

`builder/internal/pack/metadata.go:13` defines the package manifest. It carries
project, site, machine, machine role, platform, binary, services, deployment
descriptor, and two `Launch` records holding argument lists:

```json
"primary": { "args": ["-instance", "primary"] },
"standby": { "args": ["-instance", "standby"] }
```

**Nothing names the service.** The manifest says how to invoke each process and
not what to call it, so every deployment invents its own service names. The
result is that the same two fixed roles appear under different names on different
machines, which is the opposite of "static and recognizable".

This is the largest operations-visible gap in the plan and the only new
capability in it. It is also the natural home for the missing Service Control
Manager integration already recorded in `.todo`, though that remains separate
work.

### Gap 2: "role" already means something else

`manifest.role` (`metadata.go:18`) is the **machine** role from the blueprint,
for example `sensor-node` or `all-in-one`. `-instance primary` is the **instance**
role.

An operator reading a manifest sees `"role": "all-in-one"` next to
`"primary": {...}` and has to know these are different axes. The same word is
used for the machine's purpose and for the process's identity.

`redundancy.ProcessRole` (`slot.go:6`) is a third use of the word for the second
meaning.

## Target

### Naming

| Now | Target | Why |
| --- | --- | --- |
| `redundancy.ProcessRole` | `redundancy.InstanceRole` | the vocabulary's noun is Instance, and the CLI flag already says `-instance` |
| `RolePrimary`, `RoleStandby` | unchanged values `primary`, `standby` | already correct, and they are an operator-visible contract |
| `ProcessRole.OperationalName` | `InstanceRole.OperationalName` | mechanical |
| `manifest.role` | `manifest.machine_role` | removes the collision with the instance role |
| `Manifest.Primary`, `Manifest.Standby` | unchanged | already the role names |

Values stay `primary` and `standby` lowercase. They appear in launch arguments,
status file names, and event payloads, and they are correct as they are. This
stage renames types and the colliding field, not the vocabulary the operator
types.

### Service identity

Add a stated service identity per instance to the manifest, so both instances of
every machine are named the same way everywhere:

```json
"machine_role": "all-in-one",
"primary": {
  "service_name": "<derived>",
  "display_name": "<derived>",
  "args": ["-instance", "primary"]
},
"standby": {
  "service_name": "<derived>",
  "display_name": "<derived>",
  "args": ["-instance", "standby"]
}
```

Derived by the builder from deployment identity, not authored, for the same
reason the ownership object is derived: two machines given the same service name
would be an installation conflict that looks like a working manifest.

The exact derivation is a decision below.

## Decisions

**D1. Service name derivation.** It must be unique per machine per instance,
stable across rebuilds, recognizable in a service list, and legal as a Windows
service name (no forward slash or backslash, practical length limit around 256).

Options:

- `opdl-<machine>-primary`, readable, unique only if machine names are unique
  across everything installed on the host. Machine names are unique within a
  project, not globally.
- `opdl-<project>-<machine>-primary`, readable and unique in practice.
- `opdl-<digest>-primary`, guaranteed unique and unreadable in a service list,
  which defeats "recognizable".

Recommendation: `opdl-<project>-<site>-<machine>-<instance>`, with a documented
length bound and a build-time error rather than truncation when it is exceeded.
Readability matters more than absolute collision-proofing here, because the
failure mode is a visible install conflict rather than a silent split-brain.

**D2. Display name.** Recommendation: a human-oriented string carrying the same
identity, for example
`OPDL <project> <site> <machine> (Primary Instance)`, so the services list is
readable without decoding a slug.

**D3. Does the platform install the services, or only state their names?**
Stating the names is a small change and makes deployments consistent. Installing
them is a larger capability that overlaps the missing SCM integration.
Recommendation: state the names in this stage; keep installation and SCM
integration as separate work, and do not let it expand this stage.

**D4. `manifest.role` rename.** It is a published package artifact. Renaming to
`machine_role` breaks anything reading manifests today. Given the experimentation
phase, recommendation: rename, and update `scenarios/manifest_contract_test.go`
and `docs/operations/deployment.md` in the same change.

## Work

1. `redundancy.ProcessRole` becomes `InstanceRole`; update references. Values and
   the `-instance` flag are untouched.
2. `Manifest.Role` becomes `MachineRole` with JSON `machine_role`.
3. Add `service_name` and `display_name` to `Launch`, derived in
   `builder/internal/pack`.
4. Validate derived service names at build time: character set and length.
5. Print the instance's service name in the startup summary, next to the existing
   `instance role=... standby=...` line, so an operator can match a running
   process to the service that started it.
6. Update `docs/operations/deployment.md` section 6 to name the services rather
   than only their arguments.

## Validation

- Manifest contract scenario asserts both instances carry a service name and that
  the two differ.
- Two machines of one site derive different service names, with a test.
- The same machine derives the same service name across two builds, with a test.
- A machine whose identity produces an over-long service name fails the build with
  a specific error rather than truncating.
- `task all` passes.

## Out of scope

Service Control Manager integration, service installation, and graceful stop
through the SCM. Recorded in `.todo` and referenced from
`docs/operations/upgrade.md`. This stage names the services; it does not install
or manage them.

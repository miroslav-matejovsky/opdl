# Stage 07: Ownership configuration under standby

**Effort:** Medium. **Risk:** Medium. **Depends on:** stage 01. Best executed
before stage 03, so the ownership rename touches these lines once, and together
with stage 04, whose failback policy lands in the same `standby` block.

The only stage that changes a derived value.

## Intent

Ownership configuration belongs to the local high-availability decision, not
beside it. Configuring an ownership mechanism without stating the redundancy it
exists for is not a meaningful deployment, so the two are authored as one block
and the block is required.

## Current state

```hcl
platform {
  nats { client_port = 4222, cluster_port = 6222 }
  standby { disabled = false }
  fence { namespace = "opdl" }   # optional, sibling of standby
}
```

`platform.fence` is a sibling of `platform.standby` and optional, defaulting to
`opdl` through `blueprint.DefaultFenceNamespace` and `Machine.FenceNamespace()`
(`builder/internal/blueprint/topology.go`). Validation lives in `validateFence`.

The builder derives `<namespace>.fence.<digest>` in
`builder/internal/resolve/resolve.go` and records it as a top-level descriptor
field `fence.object` in both `builder/deployment` and `platform/deployment`.

## Target blueprint

```hcl
platform {
  nats { client_port = 4222, cluster_port = 6222 }

  standby {
    disabled = false

    ownership {
      namespace = "opdl"
    }
  }
}
```

`standby` becomes the machine's complete local high-availability policy: whether a
Standby Instance is deployed, and how Primary Ownership is scoped. Both the block
and its `namespace` are required, and the `opdl` default is deleted. A required
field with a default is not required.

### Why required works when no Standby Instance is deployed

Worth stating, because it reads as a contradiction.

The `standby` block is already mandatory on every machine including one with
`disabled = true`, so that redundancy is never an inherited default. Primary
Ownership is meaningful on such a machine too: with one process, ownership is what
stops a second copy of the same machine's Primary Instance from being started by
mistake, and the runtime already refuses that case with a specific error.

So `standby { disabled = true, ownership { namespace = "opdl" } }` is coherent: no
Standby Instance is deployed, and Primary Ownership still has a scope.

## Target descriptor, and a deliberate deviation

The blueprint change is straightforward. The descriptor needs a decision, because
mirroring the blueprint literally would produce a misleading structure.

Placing the resolved object under `slots.standby` would say the Standby Instance
owns it. It does not: both instances contend for one object, exactly one holds it,
and under the Preferred Primary policy that one is normally the Primary Instance.

Recommended shape:

```json
"slots": {
  "primary":   { "disabled": false },
  "standby":   { "disabled": false },
  "ownership": { "object": "opdl.ownership.<digest>" }
}
```

Ownership sits beside the two instance records rather than inside either. The
blueprint authors policy under `standby`; the builder resolves it to a
machine-level record, because the resolved object belongs to the machine and to
neither instance.

The alternative, `slots.standby.ownership.object`, mirrors the blueprint exactly
at the cost of a path that names the wrong instance.

## Derived name

`<namespace>.fence.<digest>` becomes `<namespace>.ownership.<digest>`.

**This changes the ownership object name for every machine.** Two instances only
exclude each other if they agree on it, so both instances of a machine must be
upgraded together. That is already required and already stated in
`docs/operations/upgrade.md`, but this release must call out that the object name
itself changes, because a partial upgrade would leave two instances contending for
different objects and both becoming Active.

That makes this the one stage with a real failure mode if deployed carelessly.

## Validation rules

`validateFence` becomes `validateOwnership`, called from the standby validation:

| Rule | Behavior |
| --- | --- |
| `standby.ownership` block missing | error |
| `namespace` missing or blank | error, no default |
| `namespace` has leading or trailing whitespace | error |
| `namespace` longer than 64 characters | error |
| `namespace` contains a slash or backslash | error |

`blueprint.DefaultFenceNamespace` and `Machine.FenceNamespace()` are deleted.

## Blueprints and fixtures to update

Every machine block gains an `ownership` block.

| File | Note |
| --- | --- |
| `examples/customer-a/project.hcl` | all machines; it is the schema reference |
| `builder/internal/blueprint/testdata/project.hcl` | all machines |
| `scenarios/testdata/project.hcl.tmpl` | one template block; already renders a per-run namespace |
| `platform/embedded/deployment.json` | the neutral descriptor's object field |
| `platform/deployment/deployment_test.go` | descriptor JSON fixtures |
| `builder/deployment/deployment_test.go` | `validDescriptor` |

## Decisions

**D1.** Descriptor shape: `slots.ownership.object` as recommended, or
`slots.standby.ownership.object` to mirror the blueprint exactly?

**D2.** Block name `ownership` inside `standby`, or `primary_ownership`?
Recommendation: `ownership`. The enclosing block already scopes it, and
`standby.primary_ownership` reads oddly.

**D3.** Should the derived name change to `.ownership.` at all, given it forces
both instances to be upgraded together? Recommendation: yes, and do it in this
stage rather than later. The constraint exists once; splitting it across two
releases means paying it twice.

## Work

1. `builder/internal/blueprint/topology.go`: move `Fence` into `Standby` as
   `Ownership`, make it required, delete the default and `FenceNamespace()`,
   rewrite validation.
2. `builder/internal/resolve/resolve.go`: derive `.ownership.` and place it per
   D1.
3. `builder/deployment` and `platform/deployment`: move and rename the field,
   update the required-field decoding and `Validate`.
4. Update every blueprint and fixture above.
5. Update `docs/operations/deployment.md`, `docs/01-architecture.md`, and the
   upgrade runbook's note about upgrading both instances together.

## Validation

- A blueprint omitting `standby.ownership` is rejected, with a test.
- A blueprint with a blank or malformed namespace is rejected, with tests.
- The existing derivation tests still hold: stable across rebuilds, unique per
  machine identity, unaffected by IP or machine role, namespace honored.
- The descriptor conformance check passes, which proves the builder and platform
  representations moved together.
- `task all` passes.

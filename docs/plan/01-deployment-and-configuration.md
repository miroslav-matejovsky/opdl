# Stage 1: Deployment and configuration

Estimate: 5 person-days.

## Objective

Replace the unused redundancy feature flag with an explicit per-machine
platform instance topology. Make all production endpoints concrete in the
resolved descriptor and allow instance-specific runtime socket overrides.

## Implementation steps

1. Change the authored blueprint model in `builder/internal/blueprint`.
   Add the Stage 0 machine-level platform settings. Represent omitted
   `secondary_enabled` separately from explicit `false`, then resolve omission
   to `true`. Reject duplicate platform blocks and unknown instance concepts
   through normal HCL decoding and validation.
2. Remove `Redundancy` from `blueprint.Features`, builder deployment
   `Features`, platform deployment `Features`, summaries, fixtures, examples,
   and conformance signatures. Keep the unrelated `Chaos` feature unchanged.
3. Add explicit platform instance endpoint types to both deployment descriptor
   modules. Use closed instance names, not arbitrary strings accepted without
   validation. The resolved local list must contain primary first and no more
   than one secondary.
4. Resolve API, Olric client, and Olric memberlist addresses from the machine IP
   and approved ports. Carry corresponding instance identity and endpoints in
   each fabric peer record. Sort by machine, then primary before secondary.
5. Validate the complete site endpoint set during builder resolution. Reject
   malformed host:port values, duplicate instance names, duplicate addresses,
   missing primary, secondary without primary, more than two entries, a peer
   from another site, and inconsistent views of the same instance.
6. Update `platform/embedded/deployment.json` to a neutral two-instance local
   descriptor. Add decoding tests for both redundant and primary-only forms.
7. Change the TOML schema to the approved per-instance override shape. Common
   durations remain common. Socket overrides may move a selected process but
   cannot enable secondary or change its identity.
8. Define configuration precedence in code and documentation:
   embedded descriptor endpoint, then matching runtime instance override. An
   omitted override retains the descriptor value. An explicit empty address is
   invalid rather than a request for a generated default.
9. Validate the effective endpoints for each configured instance before a
   listener opens. Include the selected instance and endpoint source in the
   startup summary without calling either instance leader or follower.
10. Update builder planning output, descriptor conformance round trips, package
    descriptor checksums, examples, testdata, and nearby package documentation.

## Suggested resolved shape

Final field names come from Stage 0. The shape should express these facts:

```json
{
  "machine": "node-a",
  "ip": "10.0.1.10",
  "platform_instances": [
    {
      "name": "primary",
      "api_address": "10.0.1.10:8080",
      "fabric_client_address": "10.0.1.10:3320",
      "fabric_memberlist_address": "10.0.1.10:3322"
    },
    {
      "name": "secondary",
      "api_address": "10.0.1.10:8081",
      "fabric_client_address": "10.0.1.10:3321",
      "fabric_memberlist_address": "10.0.1.10:3323"
    }
  ]
}
```

Do not treat this example as the final JSON contract until Stage 0 approves the
names. Avoid a separate `redundancy_enabled` field in the resolved descriptor.

## Tests

- Blueprint omission produces primary and secondary.
- Explicit disable produces primary only for that machine and leaves other
  machines redundant by default.
- The old project `features.redundancy` attribute is rejected and is absent from
  every emitted descriptor.
- Site views contain the same ordered instance membership.
- All six local sockets for a redundant machine are distinct.
- Invalid or colliding ports fail before packaging.
- Builder and platform descriptor JSON signatures and round trips match.
- Runtime overrides affect only the named instance and never descriptor
  identity.
- A secondary override on a primary-only machine fails fast.
- IPv4 and, if approved in Stage 0, IPv6 address joining is correct.
- `task plan` output clearly shows one or two platform instances per machine.

## Documentation

Update `builder` package docs, `platform/deployment/doc.go`,
`platform/internal/config/doc.go`, the example HCL comments, and
`platform/config.toml` in this stage. Do not update `docs/01-architecture.md` to
claim working redundancy yet. It may describe the new descriptor contract as
foundational work in progress.

## Risks and controls

- Defaulting a Go bool directly would make omitted and false indistinguishable.
  Use a pointer or optional block representation and test both cases.
- Repeating endpoint data can drift. Derive all per-machine descriptor views
  from one resolved site model and compare them in tests.
- Runtime overrides can create collisions the builder cannot see. Validate the
  effective local set at startup and report both colliding field names.

## Exit criteria

- The unused feature flag is fully removed.
- Every descriptor explicitly describes exactly one or two platform instances.
- Secondary is enabled by default and can be disabled only per machine.
- Descriptor defaults and TOML override precedence are tested and documented.
- `task all` passes.

# Stage 2: Descriptor and package contract

## Outcome

Make one warm standby the explicit default for every machine, with a per-machine
opt-out, and describe exactly which processes a deployment package must launch.

Complexity: Medium.

Estimated time: 3-5 engineering days.

Depends on: [Stage 1](01-instance-contract.md).

## Work

1. Remove project-level `features.redundancy` from the blueprint and from both
   deployment descriptor representations. Do not preserve a compatibility alias.
2. Add an optional `platform` subsection to `blueprint.Machine` carrying
   `warm_standby` as a pointer or equivalent presence-aware value. An omitted
   `platform` block, or an omitted `warm_standby` inside it, resolves to `true`;
   explicit `false` disables the standby process for that machine. Grouping the
   attribute under `platform` keeps platform-runtime policy explicit for a
   blueprint reader.
3. Add an explicit descriptor section in builder and platform modules:

   ```go
   type InstancePolicy struct {
       WarmStandby bool `json:"warm_standby"`
   }
   ```

   `Descriptor.Instances` always exists in generated JSON.
4. Update resolution tests to prove default-on, explicit opt-out, mixed machines
   in one project, and declaration-order independence.
5. Update descriptor validation and cross-module conformance signatures. The
   platform rejects JSON that omits `instances` or `instances.warm_standby`, so
   an incomplete descriptor cannot silently decode as an opt-out.
6. Add explicit `primary` and optional `standby` launch definitions to the
   deployment manifest. Both use the same binary and descriptor. Their arguments
   are `-instance primary` and `-instance standby`. Omit `standby` when warm
   standby is disabled.
7. Deployment starts the primary and waits for active status before starting the
   standby. Process roles make a separate shutdown-strategy field unnecessary.
8. Update examples, test blueprints, neutral embedded descriptor, plan output,
   package metadata tests, and blueprint package documentation.
9. Keep runtime socket and directory locations out of the descriptor. The
   descriptor states desired instances; local configuration states where they
   bind or store local files.

## Exit criteria

- Omission produces `instances.warm_standby: true` in every descriptor.
- One machine can opt out without changing other machines.
- The package manifest tells a deployer exactly how to launch the primary and
  optional standby processes.
- The old project-wide redundancy flag no longer exists.

## Open questions and recommendations

- Should the builder generate OS-specific service definitions?
  Recommendation: not in this stage. Make the manifest the portable launch
  contract. Scenarios consume it directly; deployment tooling can translate it
  to Windows services or systemd later.
- Should enabled descriptors carry an instance count instead of a boolean?
  Recommendation: no. The supported model is exactly one active plus one warm
  standby. A count suggests unsupported N-way election.
- Should `-instance` default to `primary`?
  Recommendation: only when standby is disabled. Require an explicit role when
  standby is enabled so two service definitions cannot share one identity.

## Risks

- Default-on doubles process memory and journal-consumer load on every machine.
- A plain bool in the blueprint cannot distinguish omitted from explicit false.
- A package that declares a standby without deployment tooling launching it
  gives a false redundancy expectation; surface desired and observed processes in
  diagnostics.

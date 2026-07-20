# Stage 6: Validate the complete change and hand it off

## Outcome

Run the full repository gate, inspect generated and authored artifacts, and
leave the repository documentation consistent with the implemented contract.

## Complexity and estimate

- Complexity: Medium
- Estimate: 0.5 to 1 developer day, including scenario runtime
- Dependencies: Stages 1 through 5
- Risk: Scenario and cross-platform timing failures can extend the estimate

## Pre-validation review

Before running commands, inspect the final diff and confirm:

- no unrelated user changes were overwritten;
- no `monitor_address`, `MonitorAddress`, or NATS HTTP port remains in OPDL
  configuration and descriptor code;
- no authored `routes` or `servers` remain in HCL;
- no standby-specific NATS endpoint model remains;
- every HCL machine has explicit NATS ports and a mandatory standby decision;
- runtime TOML contains no descriptor-owned socket topology;
- generated artifacts match their source models;
- package and public API documentation describe all changed exported types;
- no compatibility shim preserves the old experimental contract.

Use repository searches for the old names. Expected references to the word
`monitor` in unrelated monitoring documentation are acceptable. NATS monitor
configuration references are not.

## Focused validation order

Run focused checks first so failures are local and cheap to diagnose.

1. Blueprint and resolver tests.
2. Builder deployment and pack tests.
3. Platform deployment unmarshal tests.
4. NATS adapter configuration and server option tests.
5. Runtime active, standby, promotion, and reclamation integration tests.
6. Descriptor conformance tests.
7. Manifest scenario.
8. Warm standby scenario.
9. Two-machine scenarios.
10. Three-storage-node failure scenario.

Do not add sleeps or retry broad failures by increasing timeouts. Use the phase
instrumentation from Stage 5 to identify the blocked boundary.

## Required final gate

From the repository root, run:

```text
task all
```

It must pass. This includes tidy, vet, format, dead-code analysis, lint,
architecture rules, unit tests, .NET SDK validation, blueprint validation, and
black-box scenarios.

If formatting or generation changes files, inspect those changes and rerun the
complete gate. Do not report completion from focused tests alone.

## Manual contract inspection

Inspect at least one enabled-standby and one disabled-standby build plan or
package.

Enabled standby must show:

- primary launch present;
- standby launch present;
- `slots.primary.disabled = false`;
- `slots.standby.disabled = false`;
- one machine-level NATS endpoint set;
- no monitor endpoint.

Disabled standby must show:

- primary launch present;
- no standby launch;
- `slots.primary.disabled = false`;
- `slots.standby.disabled = true`;
- one machine-level NATS endpoint set;
- no monitor endpoint.

For one, two, and four machine sites, inspect the resolved topology:

| Site size | Storage servers | Cluster listeners | NATS monitor listeners |
| ---: | ---: | ---: | ---: |
| 1 | 1 | 0 | 0 |
| 2 | 1 | 0 | 0 |
| 4 | 3 | 3 | 0 |

Confirm that non-storage machines have a server list but no local listener or
route ownership.

## Failure handling

When validation fails:

- preserve the first useful error and process logs;
- inspect status `LastError` and phase timings;
- verify rendered blueprint ports before changing runtime code;
- verify fence ownership before treating an address-in-use error as a port
  allocation failure;
- do not restore runtime socket overrides to make a scenario pass;
- do not add separate standby endpoints as a workaround.

If required implementation work remains at handoff, record only concrete
unfinished work in the repository root `.todo`, as required by `AGENTS.md`.
Remove temporary notes once the work is complete.

## Final documentation checklist

- Root and platform overviews are consistent.
- Blueprint package documentation contains current HCL.
- Both deployment packages document the resolved JSON contract.
- NATS package documentation states which protocol uses each port.
- Redundancy documentation states that the fence owner reuses shared endpoints.
- Scenario documentation names the topology and failover proofs.
- Backlog items are removed only when their acceptance criteria are proven.
- No documentation claims a failover SLO from one development measurement.

## Exit criteria

- `task all` passes after the final file change.
- Enabled and disabled standby artifacts match the explicit contract.
- Listener counts match the table above.
- The warm standby regression and three-storage-node scenario pass.
- Documentation and backlog state match the verified implementation.
- No changes are committed.


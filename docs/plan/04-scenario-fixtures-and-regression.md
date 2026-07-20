# Stage 4: Make black-box scenarios exercise blueprint-owned ports

## Outcome

Refactor the scenario harness so NATS endpoints come from the blueprint while
remaining collision-resistant on developer and CI machines. Add a direct
regression for the warm standby connection failure.

## Complexity and estimate

- Complexity: High
- Estimate: 1 to 1.5 developer days
- Dependencies: Stages 2 and 3
- Primary areas: `scenarios/harness_test.go`, scenario fixtures, warm standby
  scenario, test network utility

## Problem in the current harness

`prepareSite` reserves ephemeral client, cluster, and monitor ports and writes
them as runtime TOML overrides. Stage 3 makes those keys invalid because the
blueprint is the deployment source of truth. Fixed ports in checked-in HCL are
also unsafe for black-box tests because another test or local process may own
them.

The scenario must reserve ports before the builder runs and render those ports
into a temporary blueprint.

## Harness refactor

Implement a small scenario blueprint staging helper.

1. Keep source fixtures under `scenarios/testdata`, but make their NATS ports
   template inputs. A `.hcl.tmpl` extension prevents the builder from loading an
   unrendered file.
2. Before building, reserve client and cluster addresses for each named machine.
3. Reserve on that machine's fixture IP. Extend `utils/testnet` with a focused
   helper such as `ReserveOn(ctx, host, count)` rather than embedding socket
   allocation in the scenario package.
4. Extract numeric ports from the reservations.
5. Render a temporary `<blueprints>/<project>/project.hcl` containing the
   numeric ports and explicit standby policy.
6. Build from the temporary blueprint root.
7. Hold reservations through rendering and building, then release them
   immediately before the first process that needs them starts.
8. Continue reserving the public HTTP API address through runtime TOML because
   that address remains a site-owned runtime setting.

Keep template data typed and minimal. Do not perform broad string replacement
on arbitrary HCL. Use `text/template` or construct a dedicated test fixture
model.

Refactor the scenario `sockets` structure:

- retain `api`, `dataDir`, and `instanceDir` as runtime values;
- retain resolved client and cluster addresses only for diagnostics and
  assertions;
- remove `monitor`;
- remove runtime `routes` and `servers` generation.

Refactor `platformConfig` so `[event_fabric.nats]` contains no socket, route,
server, or monitor fields.

## Fixture policy

Every rendered machine must contain both mandatory blocks.

- `scenario`: `standby.disabled = true`;
- `two-machine`: `standby.disabled = true` on both machines so the scenario
  isolates site coordination from local process redundancy;
- `manifest-contract`: `standby.disabled = false`;
- customer example: choose each machine explicitly. Do not use a default.

The warm standby fixture has one shared NATS client port and one shared cluster
port. It must not define 4222/4223 slot pairs.

## Warm standby regression

Update `TestWarmStandbyFailoverAndPreferredPrimary` to prove the exact failed
path before continuing with the existing lifecycle:

1. Start the preferred primary and wait for active status.
2. Start the standby while the primary remains active.
3. Require standby status with `promotable=true` and no last error.
4. Assert logs show that both processes used the same resolved server endpoint.
5. Assert the standby was client-only and did not bind storage or NATS
   listeners.
6. Continue forced failover, planned reclamation, repeated transfer, canceled
   waiter, and full-machine shutdown assertions already in the scenario.
7. After promotion, assert the server endpoint reported by the promoted process
   is the same endpoint previously reported by the primary.

Improve `waitStatus` diagnostics:

- fail immediately when the managed process exits;
- retain and print the last decoded status, including `LastError`;
- include process logs and expected endpoint data;
- distinguish missing status file from a status that reports an unavailable
  journal;
- keep polling for retryable standby connection errors while the process lives.

Do not weaken the timeout or accept `StateStandby` with a non-empty error.

## Other scenario updates

Update these black-box flows to use rendered blueprints:

- `TestBuildAndRunSingleMachine`
- restart and unusable-storage scenarios
- manifest launch contract
- two-machine Event Fabric
- two-machine registration
- .NET SDK end-to-end scenario
- warm standby failover and reclamation

Correct stale comments that say a non-storage machine runs a plain NATS server.
Under the documented topology it runs no server and connects as a client.

## Scenario assertions for port minimization

Add assertions at the lowest reliable level:

- manifest and embedded descriptor contain no monitor field;
- runtime startup output contains no monitor endpoint;
- a one-machine site reports no cluster routes;
- a two-machine site reports one storage server and no cluster routes;
- a non-storage machine reports client-only behavior;
- standby and active roles report the same client endpoint;
- the obsolete runtime override keys are absent from generated TOML.

Do not use a flaky scan of all operating-system sockets as the primary proof.
Unit tests of server options and black-box effective-configuration logs provide a
deterministic contract.

## Exit criteria

- Scenario NATS ports are authored in the rendered blueprint.
- Runtime TOML carries no NATS socket topology.
- No scenario reserves a monitor port.
- Warm standby reaches a healthy standby state while primary is active.
- Full warm standby failover and reclamation completes.
- All existing black-box scenarios use the same blueprint contract as customer
  builds.


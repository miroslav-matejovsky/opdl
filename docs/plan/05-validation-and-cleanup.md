# Stage 5: Resilience validation and cleanup

## Outcome

Prove warm standby behavior from built packages, document the operational
contract, and remove every obsolete project-level redundancy reference.

Complexity: High.

Estimated time: 3-5 engineering days.

Depends on: [Stage 4](04-promotion-and-handover.md).

## Work

1. Add descriptor and package tests for default-on standby, per-machine opt-out,
   two launch entries, explicit slot arguments, and standby-first stop order.
2. Add deterministic unit tests for lifecycle transitions, activation gating,
   lag gating, cancellation, cleanup order, and machine-scoped handler identity.
3. Add real cross-process fence tests. Verify one owner, cancelled wait, release
   after graceful close, and release after forced process death.
4. Extend the black-box harness to launch both slots for one built machine and
   observe their local status independently.
5. Add black-box scenarios for:
   - default descriptor launches active plus caught-up standby;
   - explicit opt-out launches one process;
   - killing the active promotes the standby and preserves registrations;
   - a proposal around failover produces one logical machine decision;
   - planned handover drains and restores the API on the same address;
   - a storage-machine standby does not open the journal until promotion;
   - full machine shutdown does not promote;
   - repeated `a` to `b` to `a` failover remains deterministic.
6. Measure and report promotion time, listener-unavailable time, journal catch-up
   time, and standby memory. Do not declare an SLO until scenarios provide a
   baseline.
7. Update root architecture, platform, builder, scenario, deployment, app,
   Event Fabric, NATS, and registration documentation. Include launch and stop
   order in package metadata documentation.
8. Remove the old `features.redundancy` vocabulary, stale examples, and temporary
   single-process compatibility paths.
9. Run repository searches for the legacy field and for active capabilities used
   outside the fenced runtime boundary.
10. Run `task all` and resolve format, lint, architecture, unit, conformance,
    integration, SDK, build, and scenario failures.

## Exit criteria

- Every completion criterion in the plan overview is covered by a deterministic
  test or black-box scenario.
- Package output is sufficient for deployment tooling to launch and stop slots
  in the safe order.
- Documentation describes the implemented process model without referring to
  the removed project feature.
- The measured failover gap and resource cost are recorded for the next design
  decision.
- `task all` passes.

## Open questions and recommendations

- What failover-time SLO should the platform promise?
  Recommendation: none before measurement. Record scenario percentiles first,
  then set a bound that includes journal reopen and catch-up.
- Must fence tests run on both Windows and Linux?
  Recommendation: yes before production. Keep OS-specific locking behind one
  contract and run the same behavior suite on both CI lanes.
- Should a failed standby make the active unready?
  Recommendation: report degraded redundancy but keep a healthy active serving.
  Redundancy health and service health are separate facts.

## Risks

- Process and port scenarios can become timing-dependent. Wait on status and
  observable API state, never sleeps.
- Cached scenarios can hide process behavior changes. Ensure failover scenarios
  always execute when tagged integration validation runs.
- Removing the old flag before all examples and descriptor fixtures move will
  break builds, which is acceptable but must be completed in one stage.

# Stage 05: Validation and observability

**Effort:** Medium. **Complexity:** Medium. **Risk:** Low.

Depends on every earlier stage. The feature is not done when it works; it is done
when a black-box scenario proves it works and an operator can see it.

## Why this is its own stage

The repository's standard for a redundancy claim is already set. The warm-standby
scenario builds a package, launches both processes, kills and hands over
repeatedly, and asserts the endpoint contract directly
(`docs/01-architecture.md:353`). The four-machine storage scenario is named in the
architecture document as the evidence for its topology
(`docs/01-architecture.md:187`).

A leadership feature with only unit tests would be held to a lower standard than
the code it sits next to.

## Scenarios

Black-box, driving built binaries as external processes, in the `scenarios`
module.

| Scenario | Proves |
| --- | --- |
| Single active per unit type | Across a multi-machine site, exactly one machine reports `Active` for a unit type, and every other reports `Standby`. |
| Failover on machine loss | Killing the holder grants the unit type elsewhere, and the epoch increases. |
| No overlap during failover | The old holder reports `Standby` before the new holder reports `Active`. This is the assertion the whole feature exists for. |
| Clean release | A gracefully stopped holder releases immediately rather than waiting for lease expiry. |
| Return of a lost machine | A machine that returns does not steal leadership from a live holder. |
| Notification latency | Time from grant to the unit observing it through the pipe, recorded as a measurement. |
| Platform failover transparency | Replacing the active platform process drops and restores the pipe, and site leadership is unaffected. |

The no-overlap scenario is the one that justifies the stage. It needs care to
write honestly: proving absence of overlap requires observing both machines across
the transition with timestamps that can be compared, not merely observing that
each machine eventually reports the right thing.

## Measurements, not an SLO

Record failover and notification latency as measurements. Do not state an SLO.

`docs/backlog/redundancy.md` is unambiguous about why: the existing warm-standby
numbers are three samples on one developer machine, and the backlog requires runs
on more than one Windows host plus percentiles before any SLO is stated. The same
bar applies here, and the same trap is available. The earlier 29.9 second measurement
in that document was a symptom of a misconfiguration rather than a performance
property, which is a good reason to treat any first number from this feature as a
question rather than an answer.

## .NET end-to-end

Extend `scenarios/dotnet_sdk_e2e_test.go` and `sdk-dotnet/tests/Opdl.Sdk.E2E` to
drive leadership through the SDK as a consumer, matching how registration is
already proven from the consumer's seat.

The existing harness passes base URLs through environment variables and
coordinates through a control directory. The leadership test needs the same
harness plus the pipe name, and it should assert the SDK's service-visible state
rather than the wire messages: what matters is what a client service sees.

## Observability

Requirement OR-42 asks that redundancy state and failover events be observable
through operational tooling (`docs/drafts/requirements.md:94`).

Emit local structured operational events through `internal/operations` for claim,
grant, renewal failure, self-demotion, expiry, release, and pipe client connect
and disconnect.

`internal/operations` is the correct home specifically because it does not depend
on the Event Fabric (`docs/01-architecture.md:59`). Leadership transitions are
most interesting when the journal is unreachable, which is exactly when an
Event-Fabric-dependent signal would be unavailable. This mirrors the existing
reasoning about why connection and journal diagnostics do not go through NATS.

## Documentation

- New `platform/internal/leadership/doc.go` covering the model, the lease, the
  epoch, the clock assumption, and the invariant that self-demotion precedes
  possible expiry. Per `AGENTS.md`, it must be self-contained.
- A new numbered document under `docs/`, following `01-architecture.md` and
  `02-registration.md`, describing the leadership protocol as registration is
  described.
- `docs/README.md` reading-order entry.
- `docs/01-architecture.md` runtime boundaries table gains the leadership package.
- `sdk-dotnet/README.md` per stage 04.

## Work

1. Scenarios above, in the `scenarios` module.
2. .NET E2E extension and harness plumbing.
3. Operational events.
4. Documentation.
5. Confirm `task all` passes: tests, format, lint, scenarios.

## Exit criteria

- `task all` passes.
- The no-overlap scenario passes repeatedly, on more than one Windows host.
- Failover and notification latency are recorded as measurements with the sample
  count and host stated, and no SLO is claimed.
- An operator can explain a leadership change from operational events alone,
  without reading the journal.

# Warm-standby plan decisions

This document is the prioritized decision list for the warm-standby plan. It
replaces the earlier chronological working notes.

The repository already contains substantial Stage 3 code. Therefore, the
"before Stage 3" items below are now Stage 3 completion gates. Do not start
Stage 4 until they are resolved and the Stage 3 exit criteria pass.

## Priorities

| Priority | Meaning |
| --- | --- |
| P0 | Correctness or contract issue that blocks the named stage gate. |
| P1 | Design or documentation issue that must be resolved in the named stage. |
| P2 | Cleanup that may wait until Stage 5. |

## Required sequence

1. Reconcile the plan status with the implementation.
2. Resolve P0 Stage 3 items and verify every Stage 3 exit criterion.
3. Resolve the shutdown and promotion P0 items before implementing Stage 4.
4. Complete the P1 contract decisions while implementing their named stage.
5. Finish P2 documentation and validation in Stage 5.

## P0: Stage gates

### P0.1 Reconcile stage status before more implementation

**Problem:** The plan overview marks every stage as "Not started", while the
repository contains Stage 1 and Stage 2 contracts and substantial Stage 3
runtime code. The old inconsistency notes also describe several items as
resolved in Stage 3.

**Recommended resolution:** Audit Stages 1 through 3 against their exit
criteria. Update the overview status from evidence, not from file presence.
Treat Stage 3 as incomplete until every P0 Stage 3 item below is resolved and
`task all` passes.

**Deadline:** Before any Stage 4 work.

### P0.2 Use slot terminology in the deployment manifest

**Problem:** The registration API uses "platform instance" for an expected
machine's progress. The descriptor and manifest also use "instance" for local
warm-standby processes. The same external term therefore describes a machine
voter and a local process.

**Recommended resolution:** Keep `instances.warm_standby` in the descriptor as
the resolved machine policy. Rename the manifest's `instances` collection to
`slots`, and rename its Go launch-entry type to `Slot` or `SlotLaunch`. This
keeps registration identity machine-scoped and makes the launch contract match
the plan's established slot terminology. Backward compatibility is out of
scope for this POC.

**Deadline:** Before Stage 3 is complete. Later deployment and scenario tooling
must not be built on the ambiguous manifest name.

### P0.3 Verify the manifest arguments against the runtime

**Problem:** `builder/internal/pack` defines slot tokens and `-instance`.
`platform/internal/redundancy` and `platform/internal/app` define them again.
The modules cannot import each other's internal packages, so the launch
contract can drift silently.

**Recommended resolution:** Add a cross-module black-box conformance test that
builds or reads a package manifest and launches the packaged binary with each
slot entry's arguments. Assert that enabled packages accept slots `a` and `b`,
and opt-out packages accept only slot `a`. Keep unit tests on both sides. Do not
add a shared package solely for three constants; that would introduce a code
dependency where behavioral conformance is the real requirement.

**Deadline:** Before Stage 3 is complete.

### P0.4 Reject a missing instance policy at the wire boundary

**Problem:** `InstancePolicy` contains only a bool, so every in-memory value is
valid. However, JSON that omits `instances` or `warm_standby` decodes to
`false`. That silently converts an incomplete descriptor into an opt-out and
violates the rule that every resolved descriptor states the policy explicitly.

**Recommended resolution:** Keep the resolved bool. Make platform descriptor
decoding presence-aware, using a wire type or custom unmarshal logic, and reject
a missing `instances` object or `warm_standby` field. Add conformance coverage
that generated JSON always contains both fields. Update Stage 2 work item 5 to
describe wire-presence validation instead of an "impossible" bool value.

**Deadline:** Before Stage 3 is complete.

### P0.5 Enable lag enforcement by configuration contract

**Problem:** Stage 3 requires a live lag bound and says a lagging projection
cannot activate or continue serving. The current `lag_bound` setting is optional
and disabled when omitted or zero. A default configuration therefore does not
meet the Stage 3 exit criterion.

**Recommended resolution:** Require `lag_bound` and require a positive duration.
Use a documented provisional value in development and scenario configuration;
do not call it an SLO until Stage 5 measurements exist. Test that an active
stops serving and a standby becomes non-promotable after the bound.

**Deadline:** Before Stage 3 is complete. The end-to-end stalled-projection
scenario may remain in Stage 5, but enforcement must not be optional.

### P0.6 Do not ignore status-file write failures

**Problem:** Status writes in `runStatus` and `writeFailedStatus` discard errors.
Stage 4 expects deployment tooling to trust these files when selecting a
standby for handover and when determining full-machine shutdown order. A stale
file can produce an unsafe operational decision.

**Recommended resolution:** Write the initial status synchronously and fail
startup if it cannot be written. Treat later write failures as runtime failures:
cancel serving or standby operation, close owned resources, and return the
error. If writing a final failed status also fails, report that error without
losing the original failure.

**Deadline:** Before Stage 3 is complete.

### P0.7 Replace static stop order with live-role shutdown

**Problem:** The manifest currently declares `stop_order: ["b", "a"]`. Slots
are symmetric. After promotion, slot `b` can be active and slot `a` can be the
standby. Stopping `b` first then looks like an active failure and can cause an
unwanted promotion during full-machine shutdown.

**Recommended resolution:** Remove the claim that a static slot order is safe.
Define full-machine shutdown as a state-aware operation: read current slot
status, stop the live standby, wait for it to exit, then stop the active. The
manifest should describe this strategy instead of listing fixed slot names. If
deployment tooling cannot implement a state-aware stop, add an explicit local
machine-shutdown intent that prevents promotion while both slots are stopped.
Test shutdown with both `a` active and `b` active.

**Deadline:** Before Stage 4 promotion is implemented.

### P0.8 Define storage-standby behavior across active server loss

**Problem:** A standby on a storage machine currently connects only to its
co-located active server. When that process dies, the standby loses its
projector connection at the same time that it must wait for and acquire the
fence. In a multi-storage site it also ignores healthy peer servers.

**Recommended resolution:** Preserve all derived storage-node client addresses
for client-only standby composition, with the local server preferred while it
is available. During promotion, wait for the fence independently of the
projector connection. After fence acquisition, discard the client-only
composition, open the local server and storage, and perform the required fresh
catch-up before activating. A single-storage-node site cannot stay connected
during active loss, so its promotion path must explicitly support this cold
connection gap.

**Deadline:** Before Stage 4 promotion is implemented.

## P1: Resolve within the named stage

### P1.1 Put slot identity in lifecycle payloads, not `events.Node`

**Problem:** Stage 1 asks for slot identity in operational event node identity,
while the event package defines `Node` as machine-scoped domain identity. Adding
the slot to every event node would make one machine appear to be two domain
voters.

**Recommended resolution:** Keep `events.Node` machine-scoped. Add `slot` and
active state to the ready and stopping lifecycle event payloads only. Use
`redundancy.OperationalName` in logs and keep slot state in the local status
file. Update Stage 1 item 6 and Stage 4 item 8 to state this boundary directly.

**Deadline:** Decide before changing lifecycle events in Stage 4.

### P1.2 Make the package boundary name consistent

**Problem:** Stage 1 and Stage 3 name `internal/instance`, but the implemented
boundary is `platform/internal/redundancy`.

**Recommended resolution:** Keep `internal/redundancy`. It cohesively owns
slots, states, fencing, status, and lag. Update the Stage 1 and Stage 3 plan
prose instead of splitting the package.

**Deadline:** Update while closing Stage 3 documentation.

### P1.3 Update architecture when Stage 3 is accepted

**Problem:** `docs/01-architecture.md` says OPDL has no fencing or redundancy.
That is already inaccurate for the contract and becomes inaccurate for runtime
behavior when Stage 3 is accepted.

**Recommended resolution:** Until Stage 3 passes, state that the contract exists
but runtime delivery is incomplete. When Stage 3 passes, replace "No
redundancy" with the implemented active and projection-only standby model.
Reserve promotion and failover claims for Stage 4.

**Deadline:** Stage 3 documentation closeout, not Stage 5.

## P2: Stage 5 cleanup

### P2.1 Keep the neutral embedded descriptor as an explicit dev opt-out

`platform/embedded/deployment.json` sets `warm_standby` to `false`, while
generated descriptors default to `true`. Keep this exception while `task run`
launches one development process. Document it as a development fixture. Change
it only when the development launcher consumes the package slot manifest and
can start both slots.

### P2.2 Complete resilience scenarios and operational measurements

The pure lag rules and fence behavior have unit coverage, but live lag
enforcement, promotion, repeated failover, full-machine shutdown, and resource
measurements require the Stage 5 black-box scenarios. These are planned
validation work, not reasons to weaken the earlier stage contracts.

## Closed decisions

- The runtime parses and validates `-instance`. The remaining cross-module drift
  risk is P0.3.
- A single-slot package explicitly passes `-instance a`; the runtime may also
  default to slot `a` only when warm standby is disabled.
- Opening the fence is the effective `instance_dir` writability probe and occurs
  before active sockets or journal storage open. Do not add a separate
  check-then-use probe. Improve the Stage 3 wording to require this ordering.
- Promotion is intentionally Stage 4 work. A one-time startup role decision is
  acceptable only until Stage 4 begins.
- The resolved policy remains a bool. The missing validation concern is wire
  presence, addressed by P0.4, not bool value validation.

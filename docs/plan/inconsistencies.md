# Plan inconsistencies and risks

Working notes collected while implementing the warm-standby plan. Each entry is a
gap, tension, or risk found against the original plan documents that needs a
decision or follow-up. This file is for investigation; it is not itself a plan.

Status legend: **open** (needs a decision), **accepted** (deliberate divergence,
recorded), **later-stage** (expected to be resolved by a named stage).

Found during: Stage 1 (instance and fencing contract), Stage 2 (descriptor and
package contract), and Stage 3 (warm standby runtime).

## 0. The fencing package is internal/redundancy, not internal/instance

- **Where:** the plan names the boundary `internal/instance` (Stage 1 item 4,
  Stage 3 item 3). The implementation is `platform/internal/redundancy`.
- **Reason:** requested consolidation. One cohesive package now owns the slot,
  state, fence, status, and lag mechanism, and "slot" is the dominant term, so a
  separate "instance" package name would split the same concern and clash with the
  descriptor's `instances` policy and the registration API's `platform_instances`.
- **Status:** accepted (deliberate divergence). Plan prose still says
  `internal/instance`; treat it as `internal/redundancy`.

---

## 1. The manifest declares `-instance`, but the runtime does not parse it yet

- **Where:** `builder/internal/pack/metadata.go` (`launchInstances`, `slotFlag`)
  emits per-slot args `["-instance", "a"]`; `platform/internal/app/app.go`
  (`Run`) only accepts `-config`.
- **Tension:** Stage 2's manifest is described as the portable launch contract, so
  a deployer copies its args verbatim. Before Stage 3 the binary rejected
  `-instance` at `flag.Parse`.
- **Status:** RESOLVED in Stage 3. `app.Run` now parses `-instance` and validates
  it with `redundancy.ParseSlot` (`resolveSlot`). One residual risk remains: the
  manifest's flag name (`pack.slotFlag = "-instance"`) and the runtime's flag name
  are still two independent string literals (see entry 2).

## 2. The slot contract is duplicated across two modules that cannot import each other

- **Where:** `platform/internal/redundancy` defines `SlotA`/`SlotB` and the
  runtime's `-instance` flag; `builder/internal/pack` re-defines `slotA`/`slotB`
  and `slotFlag = "-instance"`. The builder cannot import a platform `internal/`
  package.
- **Risk:** the two definitions can drift (a slot rename or a flag rename on one
  side is silent on the other), and nothing fails when they disagree. Stage 3
  resolving entry 1 makes this the live coupling between the manifest and the
  binary.
- **Status:** open. Options: (a) a small exported (non-internal) shared constants
  package both modules import; (b) a conformance-tests check asserting the
  manifest's slot tokens and flag match the platform's `redundancy` package and
  `-instance` flag.

## 3. "Instance" now has two externally visible meanings

- **Where:** the registration query API already exposes `platform_instances` /
  "platform instance" meaning each *expected machine's* progress (see
  `platform/internal/registration` and `scenarios/registrationapi_test.go`).
  Stage 2 adds `Descriptor.Instances` (`InstancePolicy`) and manifest `instances`,
  where "instance" means a *local slot/process*.
- **Risk:** a reader of the JSON can conflate a machine's two slots with the
  registration voters. The plan itself uses "instances" for the descriptor section
  (`instances.warm_standby`), so this is inherited from the plan, not introduced
  against it.
- **Status:** open. Decide whether to rename the manifest's `instances` to `slots`
  (leaving the descriptor's `instances.warm_standby` as the plan specifies), or to
  document the two meanings prominently. Domain identity is unaffected either way
  (registration stays machine-scoped, see `internal/instance` docs).

## 4. Descriptor validation of the instance policy has no content

- **Where:** Stage 2, work item 5: "Reject an impossible or incomplete instance
  policy before building." `InstancePolicy` is `{ WarmStandby bool }`.
- **Tension:** a bool is always present and always valid, so `Descriptor.Validate`
  has nothing to reject. This is a direct consequence of the plan's own (correct)
  decision to use a bool rather than an instance count. The work item implies
  validation that cannot exist.
- **Status:** accepted. No validation added. Reduce the work item, or confirm the
  policy stays a bool so "impossible/incomplete" remains unreachable.

## 5. The neutral embedded descriptor opts out of warm standby, against default-on

- **Where:** `platform/embedded/deployment.json` sets
  `instances.warm_standby: false`.
- **Tension:** the plan's headline is default-on. The embedded descriptor is a
  hand-authored placeholder used by `task run`, which launches a single
  all-in-one dev process, so `false` matches what actually runs.
- **Status:** accepted (single-process dev). Revisit when Stage 3 wires the
  runtime, in case dev should exercise two slots locally.

## 6. Static stop order can stop the active first after a failover

- **Where:** manifest `StopOrder` is `["b", "a"]` (Stage 1 item 7: "stops slot b
  before slot a"; Stage 2 item 7: "standby candidate first, current active
  second").
- **Tension:** the symmetric-slot design lets ownership change, so after a
  promotion slot `a` may be the standby and slot `b` the active. A static
  `["b", "a"]` then stops the *active* first — exactly the "looks like an active
  failure, so promote" case the plan warns against (README core decision: "Full
  machine shutdown stops the standby before the active"). A static manifest cannot
  know the live active.
- **Risk:** a full-machine shutdown after a failover could trigger an unwanted,
  though bounded, re-promotion.
- **Status:** later-stage (Stage 3/4). Reconcile the static stop order with a
  runtime "full machine shutdown" signal, or accept and document a harmless
  bounded re-promotion during shutdown.

## 7. `docs/01-architecture.md` "No redundancy" section is going stale

- **Where:** `docs/01-architecture.md`, "## No redundancy": "OPDL currently has no
  primary/secondary instance, election, fencing, failover, or zero-downtime
  upgrade mechanism."
- **Tension:** after Stage 1 there is a fencing *contract* (`internal/instance`),
  and after Stage 2 the descriptor and manifest declare warm standby. The runtime
  still runs a single active process, so "no failover" is true at runtime, but
  "no fencing" is now inaccurate.
- **Status:** later-stage (Stage 5, "cleanup"), or add a "contract defined,
  runtime pending" note sooner. Not assigned by the plan.

## 8. Slot identity is defined for operational events but not yet stamped

- **Where:** Stage 1 item 6: "Add slot identity to operational event node identity
  and logs." Delivered: `redundancy.OperationalName`, the slot in the startup
  summary, and the slot in the per-slot status file. Not done: `events.Node` is
  still unchanged, so the operational `ready`/`stopping` events carry machine
  identity without a slot. Only the active slot publishes those, so the slot is
  currently inferable but not stated.
- **Status:** partially addressed; `events.Node` stamping still open. Decide
  whether `events.Node` gains a slot field or whether the slot rides only in logs,
  the status file, and the ready/stopping payloads, keeping domain identity
  machine-scoped. `redundancy.OperationalName` exists for this but is so far used
  only in tests.

## 9. The fence path's runtime directory

- **Where:** `redundancy.FencePath(runtimeDir, ...)` needs a runtime directory.
  Stage 2 item 9 keeps runtime directory locations out of the descriptor.
- **Status:** RESOLVED in Stage 3. `config` now requires `instance_dir` and
  `app.runSlot` derives the fence and per-slot status paths from it. Residual:
  Stage 3 item 2 also asked for an up-front writability check; `OpenFence` creates
  the directory and the OS lock effectively probes it, but an unwritable
  `instance_dir` fails when the fence is opened rather than in config validation.

## 10. Manifest is explicit for the single-slot case

- **Where:** Stage 2 open question: "Should `-instance` default to slot a?
  Recommendation: only when standby is disabled." The manifest emits an explicit
  `["-instance", "a"]` even for a disabled (single-slot) machine.
- **Status:** RESOLVED / consistent. Stage 3's `resolveSlot` defaults `-instance`
  to slot a only when warm standby is disabled and rejects slot b there, matching
  the recommendation. The manifest stays explicit, which is compatible.

## 11. Promotion is not implemented; role is decided once at startup

- **Where:** Stage 3 `runSlot` uses `Fence.TryAcquire` once at startup: the winner
  is active, a loser is a standby, and a standby never later takes the fence.
- **Tension:** this is correct for Stage 3 ("run a projection-only second process
  under fencing") but means a standby does not yet promote when the active exits.
  The blocking `Fence.Acquire` and the state machine's `activating` transition
  exist for exactly this.
- **Status:** later-stage (Stage 4, "promotion and handover"), by design.

## 12. Lag enforcement is wired but off by default and unexercised end-to-end

- **Where:** `app.runStatus` tracks lag and, for an active slot, stops serving
  when it crosses `config.LagBound`; a standby is marked not promotable. But
  `lag_bound` defaults to disabled, and no automated scenario drives a real lag
  past the bound.
- **Status:** later-stage (Stage 5, "resilience validation"). The pure lag logic
  (`redundancy.LagState`, `redundancy.Exceeds`) is unit-tested; the live
  enforcement path is not yet exercised against a stalled projection.

## 13. A storage-machine standby follows only its co-located active's server

- **Where:** `app.clientOnly` keeps `cfg.Servers`, which for a storage node the
  descriptor derived as `[own client address]` — the co-located active's server.
  So a storage-machine standby reads the journal through the active on the same
  machine.
- **Tension:** if that active is down, the standby cannot follow. That is
  acceptable for Stage 3 (the active is up) and is the moment the standby should be
  promoting anyway, but a promoted-then-standby topology or a multi-storage site
  might prefer the standby to fall back to another storage node's server.
- **Status:** later-stage (Stage 4). Revisit standby server selection alongside
  promotion.

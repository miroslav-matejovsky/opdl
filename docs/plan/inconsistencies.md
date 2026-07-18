# Plan inconsistencies and risks

Working notes collected while implementing the warm-standby plan. Each entry is a
gap, tension, or risk found against the original plan documents that needs a
decision or follow-up. This file is for investigation; it is not itself a plan.

Status legend: **open** (needs a decision), **accepted** (deliberate divergence,
recorded), **later-stage** (expected to be resolved by a named stage).

Found during: Stage 1 (instance and fencing contract) and Stage 2 (descriptor and
package contract).

---

## 1. The manifest declares `-instance`, but the runtime does not parse it yet

- **Where:** `builder/internal/pack/metadata.go` (`launchInstances`, `slotFlag`)
  emits per-slot args `["-instance", "a"]`; `platform/internal/app/app.go`
  (`Run`) only accepts `-config`.
- **Tension:** Stage 2's manifest is described as the portable launch contract, so
  a deployer copies its args verbatim. Today the binary would reject `-instance`
  at `flag.Parse`. The manifest promises something the binary cannot yet honor.
- **Status:** later-stage (Stage 3, "Warm standby runtime"). Stage 3 must add
  `-instance` to `app.Run`, validate it with `instance.ParseSlot`, and confirm the
  flag name equals the manifest's `slotFlag`.

## 2. The slot contract is duplicated across two modules that cannot import each other

- **Where:** `platform/internal/instance` defines `SlotA`/`SlotB`;
  `builder/internal/pack` re-defines `slotA`/`slotB` and `slotFlag`. The builder
  cannot import a platform `internal/` package.
- **Risk:** the two definitions can drift (a slot rename or a flag rename on one
  side is silent on the other), and nothing fails when they disagree.
- **Status:** open. Options: (a) a small exported (non-internal) shared constants
  package both modules import; (b) a conformance-tests check asserting the
  manifest's slot tokens and flag match the platform's `instance` package.

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
  and logs." Delivered: `instance.OperationalName` plus documentation. Not done:
  `events.Node` is unchanged and nothing stamps a slot, because no slot value
  exists at runtime until `-instance` is parsed (Stage 3).
- **Status:** later-stage (Stage 3). Decide whether `events.Node` gains a slot
  field or whether the slot rides only in logs and the ready/stopping payloads,
  keeping domain identity machine-scoped.

## 9. The fence path has no configured runtime directory yet

- **Where:** `instance.FencePath(runtimeDir, ...)` exists; nothing supplies
  `runtimeDir`. Stage 2 item 9 correctly keeps runtime directory locations out of
  the descriptor, so it must come from the platform's TOML config, which has no
  such field yet.
- **Status:** later-stage (Stage 3). Add a local runtime-directory setting to
  `platform/internal/config` and wire `FencePath` at composition.

## 10. Manifest is explicit for the single-slot case, diverging from the `-instance` default recommendation

- **Where:** Stage 2 open question: "Should `-instance` default to slot a?
  Recommendation: only when standby is disabled." The manifest emits an explicit
  `["-instance", "a"]` even for a disabled (single-slot) machine.
- **Rationale:** a launch contract should be explicit rather than rely on a runtime
  default a deployer cannot see.
- **Status:** accepted. When Stage 3 implements the runtime default (slot a when
  standby disabled), keep the manifest explicit; the two are compatible.

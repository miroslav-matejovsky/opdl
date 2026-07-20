# Issues observed while implementing the NATS and standby contract

Findings from implementing stages 1 to 6. Each entry records what was observed,
the evidence, and what was done or is recommended. Nothing here is speculative:
every item was reproduced.

Status values: **Fixed** in this change, **Open** and needing a decision, or
**Resolved as a side effect** with evidence.

---

## 1. Three-storage-node sites could not start at all

**Status:** Fixed.

**Observed.** The first four-machine scenario never started. Every storage
machine failed at startup with:

```
nats: open on 127.0.0.1:62017,...: nats: look up journal OPDL_..._EVENTS:
context deadline exceeded
```

The resolved topology was correct: three storage machines, two routes each,
`replicas=3`, and a client-only fourth machine.

**Root cause.** `ensureJournal` made one `js.Stream()` call and treated anything
that was not `ErrStreamNotFound` as fatal. On a site with three storage nodes the
JetStream metadata group has no leader until a quorum of the three servers has
found each other, and until then the JetStream API does not answer: the request
times out rather than reporting a missing stream. Every storage node starts at
once, so on a cold site that timeout is the normal first answer.

A second transient appeared once the first was fixed: immediately after the
election the metadata leader may not yet know about enough peers, and answers
stream creation with API error 10005, `no suitable peers for placement, peer
offline`.

**Evidence that clustering itself was fine.** A direct probe of three embedded
servers built from the adapter's own `serverOptions` elected a metadata leader in
under one second and created a three-replica stream successfully. The defect was
entirely in how startup interpreted the API's answers.

**Fix.** `ensureJournal` now retries until the site's journal is reachable,
bounded by the same `StartupTimeout` that bounds connecting, and distinguishes
"the cluster is still forming" from "this site has no reachable journal".
`retryableJournalError` retries a missing stream, no responders, a request
deadline, JetStream-not-enabled, and error 10005. An incompatible journal is
still refused immediately, because waiting cannot make it compatible.

**Note.** This was pre-existing and independent of the endpoint contract. It is
why `docs/backlog/event-fabric.md` could say the three-replica topology was
unproven: it had never been started.

---

## 2. Writes are rejected briefly after a storage machine is lost

**Status:** Open. Behaviour documented; scenario asserts it honestly.

**Observed.** Immediately after stopping one of three storage machines, a
registration POST to a surviving machine intermittently returned a non-202. It
succeeded on retry within a short window. Reproduced in roughly one run in three.

**Cause.** The journal's replica group must elect a new leader before it can
accept a write again. The platform does not retry internally, so the failure is
returned to the client.

**Current handling.** The scenario uses `proposeEventually`, which retries as a
real client would, and its comment states that losing a storage machine is not
transparent to a writer.

**Decision needed.** Whether the platform should absorb this window internally
with a bounded publish retry, or whether it stays a documented client
responsibility. Absorbing it makes single writes more reliable but hides
back-pressure; leaving it means every client needs retry logic. This was not
decided here because it changes the publish contract, which is outside this
initiative's scope.

---

## 3. A client-only machine may stop projecting when its storage node dies

**Status:** Open. This is the one acceptance criterion of the three-storage-node
backlog item that is not met.

**Observed.** In the four-machine scenario, after stopping storage machine
`node-c`, the client-only machine `node-d` intermittently failed to confirm a new
proposal within the 60 second scenario bound, while the surviving storage
machines `node-a` and `node-b` confirmed promptly:

```
PlatformInstances:[
  {Machine:node-a Status:accepted}
  {Machine:node-b Status:accepted}
  {Machine:node-c Status:pending}   <- deliberately stopped
  {Machine:node-d Status:pending}   <- client-only, did not recover in time
]
```

Intermittent, not constant: `node-d` recovered in other runs.

**Likely cause, not yet confirmed.** A non-storage machine's server list is the
three storage client addresses, and the NATS client randomises its server
selection by default. When `node-d` happens to be connected to the storage node
that is killed, it must reconnect and its ordered consumer must resume from its
last applied sequence. The runs that failed are consistent with that resume being
slower than the bound or not happening; the runs that passed are consistent with
`node-d` having been connected to a machine that stayed up.

**Not investigated further** because it is a pre-existing Event Fabric client
resilience question rather than part of the endpoint or standby contract, and
confirming it needs client-side connection instrumentation the runtime does not
currently emit.

**Current handling.** The scenario requires the surviving *storage* machines to
keep accepting and projecting, and requires every machine including `node-d` to
reconverge to the same state once the site is whole. It does not assert that a
client-only machine keeps projecting across the loss of its own server. The
backlog item is rewritten to name this as the remaining gap rather than claiming
the topology is fully proven.

**Recommended next step.** Log the connected server address and every reconnect
in the Event Fabric client, then re-run the scenario pinning `node-d` to the
machine that gets killed. That turns an intermittent scenario failure into a
deterministic one.

---

## 4. The Windows forced-kill promotion gap appears to have been a symptom

**Status:** Resolved as a side effect. Evidence recorded; backlog item rewritten
rather than deleted.

**Background.** `docs/backlog/redundancy.md` recorded a 2026-07-18 baseline of
about 29.9 seconds from forced primary death to standby activation, close to the
configured 30 second Event Fabric startup bound, against about 287 ms for a
planned handover.

**Observed after the fix.** Three consecutive Windows runs of the warm standby
scenario:

| Run | Catch-up | Promotion | Listener unavailable | Handover |
| --- | ---: | ---: | ---: | ---: |
| 1 | 110.8 ms | 182.6 ms | 192.9 ms | 275.8 ms |
| 2 | 108.3 ms | 126.8 ms | 133.5 ms | 266.6 ms |
| 3 | 91.9 ms | 149.4 ms | 155.2 ms | 177.8 ms |

**Interpretation.** The old number being within a rounding error of the 30 second
startup bound is the tell. The standby was never actually connected: it was given
its own client address, 4223, while the only running server was the primary's on
4222, so it retried against an address nothing was listening on. On forced primary
death the promoted process was therefore not warm at all, and had to complete a
cold startup bounded by that same 30 seconds.

**Caution.** Three samples on one Windows developer machine are evidence that the
cause was the endpoint defect. They are not a failover SLO, and no percentile or
Linux sample was collected. The backlog item is rewritten to require
cross-platform CI percentiles before any SLO is stated.

---

## 5. Obsolete runtime configuration keys were silently ignored

**Status:** Fixed.

**Observed.** The scenario harness wrote `client_address`, `cluster_address`,
`monitor_address`, `routes`, and `servers` into runtime TOML long after the
runtime stopped reading them. BurntSushi TOML ignores unknown keys by default, so
the platform started, reported a healthy configuration, and behaved as though the
file had never mentioned them.

**Why it mattered.** This is what made issue 4 slow to diagnose. A setting that is
silently dropped is indistinguishable from one that was applied, so the harness
and the runtime disagreed about the machine's addresses and nothing said so.

**Fix.** `loadFile` decodes with metadata and rejects undecoded keys, naming every
one of them. `platform/config.toml` documents that socket topology is deployment
data and cannot be set at a site.

---

## 6. The in-process app test encoded a topology the resolver cannot produce

**Status:** Fixed.

**Observed.** `TestPromotionAndPrimaryReclamation` built a standby slot with
client address `127.0.0.1:4223` and a server list containing `127.0.0.1:4222`.
The resolver never produced that combination. The test passed while the black-box
warm standby scenario failed, because the fixture handed the standby a working
server list that its own slot configuration contradicted.

**Fix.** The test now enables the standby and changes nothing else, so both
processes derive the same endpoints from one descriptor. `app_test.go` moves the
descriptor's addresses onto reserved ports through `descriptorOnFreePorts` rather
than overriding sockets in the runtime configuration, which keeps the descriptor
the single source of the machine's topology.

---

## Summary

| # | Issue | Status |
| --- | --- | --- |
| 1 | Three-storage-node sites could not start | Fixed |
| 2 | Write window after storage loss | Open, decision needed |
| 3 | Client-only machine projection across server loss | Open, backlog item |
| 4 | Windows forced-kill promotion gap | Resolved as a side effect |
| 5 | Silently ignored runtime configuration keys | Fixed |
| 6 | App test encoded an unreachable topology | Fixed |

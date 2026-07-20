# Issues observed while implementing the NATS and standby contract

Findings from implementing stages 1 to 6. Each entry records what was observed,
the evidence, and what was done or is recommended. Nothing here is speculative:
every item was reproduced.

Status values: **Fixed** in this change, **Open** and needing a decision, or
**Resolved as a side effect** with evidence.

**Gate status.** `task all` passes as of 2026-07-20, including all 11 scenarios.
Issue 2 remains an explicit product decision. Issues 3, 7, and 8 were resolved
by the resilience and observability follow-up recorded below.

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

**Status:** Fixed and deterministically covered.

**Observed.** The client-only `node-d` intermittently stopped confirming after a
storage node was killed. The NATS client randomized its configured server list,
so the scenario could not tell whether it had killed the connected server. The
default reconnect cadence was also unrelated to the platform's projection-lag
safety bound.

**Fix.** Client configuration now preserves the resolver's ordered server list,
retries every 250 ms, and keeps reconnecting while the platform process remains
live. The platform's lag bound, not a transport retry count, decides when serving
is no longer safe. A `RECONNECTING` transition explicitly stops the projector and
handler iterators. After connection recovery, the projector is recreated at the
next unapplied sequence and the durable handler is reattached. Transient or
ambiguous consumer-creation timeouts are retried. Structured operational events
record every connection and consumer reset with the selected server and resume
sequence.

**Evidence.** `TestFourMachineStorageTopologyAndFailure` now proves the behavior
rather than inferring it. It verifies that `node-d` initially connects to the
first resolved storage server, `node-a`, kills `node-a`, waits for an
`event_fabric.client_reconnected` event naming `node-b` or `node-c`, and requires
`node-d` to project and confirm a new proposal while `node-a` remains offline.
The isolated scenario and the full gate pass.

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

## 7. A surviving storage machine can stop serving when another one is killed

**Status:** Fixed.

**Root cause.** The resolver deliberately places a storage machine's own server
first, but the NATS client randomized the list by default. A storage process
could therefore attach its projector and handlers through a remote peer. Killing
that peer made a healthy local server unnecessarily dependent on the failed
machine and exposed consumer interruption behavior.

**Fix.** `nats.DontRandomize()` preserves local-first resolution. Storage
machines connect to their own embedded server, while client-only machines use the
stable sorted storage order. Unexpected iterator closure with a live context is
now returned as an error instead of being reported as a clean stop. Fabric state
read failures advance the same lag clock as a behind projection, so an active
process cannot serve an indefinitely stale view during a transport outage.

**Evidence.** The four-machine storage-loss scenario passes in isolation and as
part of `task all`. It now exercises the stronger case of killing `node-a` while
`node-b`, `node-c`, and client-only `node-d` continue processing.

---

## 8. An interrupted build leaves the staged descriptor in the working tree

**Status:** Fixed.

**Observed.** After a scenario run was interrupted mid-build,
`platform/embedded/deployment.json` was left holding a scenario machine's
descriptor (`project: four-machine`, `machine: node-c`) instead of the neutral
mock.

**Cause.** Packaging stages each machine's descriptor into the embedded file,
compiles, and restores the snapshot afterwards. The restore does not run when the
process is killed between staging and restoring.

**Why it matters.** The repository is supposed to always compile without a
customer descriptor present. A left-behind descriptor breaks that invariant
silently: the next build starts from the wrong embedded file, and the working
tree shows a modified generated artifact that looks like an intentional change.

**Fix.** Packaging now writes the customer descriptor to a temporary staging
directory and passes a Go build overlay that maps the neutral embedded file to
that staged copy. The working tree is never modified, so process termination
cannot leave customer deployment data behind. The committed embedded descriptor
was restored to the neutral mock. Unit coverage verifies staging leaves it
unchanged.

---

## 9. A newly created journal was not immediately visible on every server

**Status:** Fixed during deterministic resilience testing.

**Observed.** A storage node received a successful stream creation result, then
failed its immediate readiness high-water read with NATS API error 10059,
`stream not found`. Structured startup events showed journal creation had taken
8.2 seconds and the failure occurred on the next operation, before HTTP opened.

**Root cause.** The metadata leader had accepted stream creation, but the local
server used by that machine had not yet converged on the new stream. A creation
acknowledgement alone was therefore not sufficient evidence that the returned
stream handle was locally usable.

**Fix.** `ensureJournal` now calls `Stream.Info` before returning. Error 10059 is
treated as a bounded startup retry, like the other cluster-formation states. A
unit test covers its retry classification. The deterministic four-machine
scenario then passed.

---

## 10. Consumer creation could succeed remotely and time out locally

**Status:** Fixed during repeated resilience testing.

**Observed.** NATS created a durable handler consumer, so startup observed it
with zero pending messages and declared the site ready. The original create
request then timed out and its runner stopped, which correctly caused the API to
close. The event timeline showed `site_ready` followed by a handler create
deadline error five seconds later.

**Fix.** Consumer creation now retries transient and ambiguous responses. The
Fabric also tracks whether the local handler iterator is active. `HandlerPending`
reports `ErrHandlerNotAttached` until both the durable exists and the local loop
is consuming it, so readiness cannot pass on server-side state alone. Projector
creation uses the same bounded retry classification after reconnect.

---

## 11. One .NET end-to-end assertion failed without diagnostic detail

**Status:** Not reproduced; test diagnostics fixed.

**Observed.** One full-gate run reported the named .NET registration end-to-end
test as failed after seven seconds. Both platform processes remained healthy and
their event timelines contained no error-level transition. The test runner's
quiet console output omitted the failed assertion, so the captured evidence could
not distinguish a contract mismatch from a test-runner issue.

**Follow-up.** Four isolated executions of the actual .NET test passed. The
scenario now requests normal console logger detail and verifies that the named
end-to-end test reports `Passed`, rather than matching a generic summary. A
future recurrence will include the assertion, expected value, actual value, and
stack location in the scenario failure. No platform behavior was changed based
on an unreproduced failure.

---

## Summary

| # | Issue | Status |
| --- | --- | --- |
| 1 | Three-storage-node sites could not start | Fixed |
| 2 | Write window after storage loss | Open, decision needed |
| 3 | Client-only machine projection across server loss | Fixed and covered |
| 4 | Windows forced-kill promotion gap | Resolved as a side effect |
| 5 | Silently ignored runtime configuration keys | Fixed |
| 6 | App test encoded an unreachable topology | Fixed |
| 7 | Surviving storage machine stops serving after another is killed | Fixed |
| 8 | Interrupted build leaves the staged descriptor behind | Fixed |
| 9 | New journal briefly invisible on a local server | Fixed |
| 10 | Consumer created remotely but create response timed out | Fixed |
| 11 | .NET end-to-end assertion failed without detail | Not reproduced; diagnostics fixed |

# Step 03 — Integration tests in the redundancy package

Complexity: **Medium** · Effort: **M (~1 day)** · Depends on: step 02

## Why

The redundancy package has good unit coverage of each piece (lease record,
promotion decision, failback window), but nothing that runs **two real
contenders end-to-end over one lease file** through whole lifecycles. That is
exactly the shape a machine runs, and it is cheap to run in-process: two
`Contend` calls, two `recordingRuntime`s, one temp lease file, millisecond
timings. These tests are the package's specification of the redundancy contract.

## Design

A new `integration_test.go` in `redundancy_test` (external test package), built
on the existing helpers (`leaseConfig`, `openLease`, `recordingRuntime`,
`healthyPeer`/`unhealthyPeer`). Each test drives both instances concurrently
and asserts on the *sequence* of states and on invariants sampled continuously.

The health gate is a function pointer, so tests model the peer's health
truthfully: "peer healthy" is derived from whether the other runtime is
currently Active (a small shared probe struct), not hardcoded — that is what
closes the loop between lease state and health state the way the real app wires
it.

## The tests

1. **Normal life** — Primary starts first, takes the lease, is Active; Standby
   starts, stays Passive. Stop both; clean release, no failover events.
2. **Failover** — Primary is Active, then "dies" (its Contend ctx is killed
   without release, or renewal is stopped via a test hook). Lease lapses, probe
   reports the Primary unhealthy, Standby promotes within a bounded time.
   Asserts the `ownership_acquired{abandoned:true}` record.
3. **No promotion while peer healthy** — lease artificially lapsed but probe
   says healthy: Standby declines (asserts `promotion_declined`), never
   activates.
4. **Failback with stabilization** — after failover, the Primary contender is
   restarted (new Contend on the same lease). Standby stays Active through the
   stabilization window, then steps down in place; Primary becomes Active;
   Standby is Passive **and still running** (its runtime records
   `passive start` after `active stop` within the same Contend call).
5. **Flap does not fail back** — during stabilization the probe reports the
   Primary unhealthy once; the window resets; Standby remains Active past the
   original deadline.
6. **Split-brain invariant** — a sampler goroutine polls both runtimes for the
   whole duration of tests 2 and 4 and fails if both are ever Active in the
   same sample. (In-process sampling cannot catch every interleaving, but it
   pins the invariant against regressions.)
7. **In-place cycling** — one Standby Contend call goes
   Passive→Active→Passive→Active across a Primary that dies, returns, and dies
   again; no call ever returns until its ctx is canceled.

## Test discipline

- Follow the house rules: no `require.*` inside `require.Eventually` condition
  functions; drain every Contend goroutine before the test returns so
  `t.TempDir()` cleanup does not race the lease file.
- Timings derive from one `leaseConfig` so the suite stays fast (<5 s total) and
  a single knob widens everything if CI proves noisy.
- Run with `-race` in the normal test task (they are goroutine-heavy by design).

## Done when

- All seven tests pass with `-count=3 -race` locally.
- A deliberate bug (e.g. removing the health gate) fails at least one test.

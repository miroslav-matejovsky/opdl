# Step 02 — In-place Passive↔Active transitions

Status: **implemented**. The entry point is `redundancy.ManageOwnership`. One
design refinement over the sketch below: promotion now reads *whose* grant made
the lease free — a grant released by the other instance is an explicit handover
and promotes at once, a grant this instance released itself needs an unhealthy
peer, and an instance may reclaim its own lapsed grant. That is what lets a
Standby that failed back in place (still running, still healthy) hand the lease
to the Primary without the health gate deadlocking on two healthy instances.

Complexity: **High** · Effort: **L (2–3 days)** · Depends on: nothing (01 can
land first or after)

## Why

Today both self-step-down (an owner that cannot keep its lease) and failback (an
Active Standby handing back to a healthy Primary) end with the process
**exiting**: `ManageOwnership` returns, and a service manager is expected to restart the
instance Passive. That breaks the contract in two ways:

- Passive must mean "still running, still answering health checks". An exited
  Standby answers nothing.
- Without a service manager (dev runs, scenario harness), a failback leaves the
  machine with no standby at all.

The instance must move between Active and Passive **in place**, for its whole
process lifetime, exiting only on a signal or a fatal error.

## Design

Two changes, one in `redundancy`, one in `app`.

### `ManageOwnership` is a loop

The ownership entry point (now `ManageOwnership`) walks the lifecycle in a loop:

```
for ctx alive:
    if lease free (or acquired): run Active
        Active ends because:
          - ctx canceled            -> release, return (process stopping)
          - failback initiated      -> release, continue loop as Passive
          - lease lost / not renewable -> (lease already gone) continue as Passive
          - Active returned an error   -> release, return error (fatal)
    else: run Passive
        Passive ends because:
          - ctx canceled     -> return
          - promotion won    -> continue loop as Active
          - Passive errored  -> return error (fatal)
```

Key points:

- The existing child-context plumbing already distinguishes "process stopping"
  (parent ctx) from "stop being Active" (`stopActive`); the loop formalizes it.
  `startRenewal` and `startFailback` keep calling the same step-down hook — the
  hook now means "cancel Active and loop", not "cancel Active and exit".
- After a failback release, the Standby re-enters Passive and must **not**
  immediately re-promote: the lease was *released*, but the peer (Primary) is
  healthy, so the existing health gate already blocks it. No new mechanism.
- Activation kinds keep working: a Primary re-entering Active after the loop is
  a failback, a Standby is a failover — `activationKind` already derives this
  from role; only "initial" needs to mean "first activation of this process".
- Events: `stepped_down` and `failback_initiated` stay; add nothing unless a
  gap shows up in tests. Each loop turn states its own
  `activation_started/completed`, so the record reads as a sequence of turns.

### The app keeps the listener across transitions

`app.runActive` currently calls `server.shutdown` on its way out, which closes
the listener — correct for a process stop, wrong for a return-to-Passive.

- `instanceServer` gains a drain-without-close: stop routing to the Active
  handler (swap `serveWith` back to the Passive handler first, so new requests
  get the Passive surface), wait for in-flight Active requests to finish, keep
  the listener bound. Simplest workable form: swap the handler, then wait on a
  small in-flight counter (`sync.WaitGroup` incremented in `ServeHTTP`) with the
  existing shutdown timeout. `http.Server.Shutdown` stays reserved for real
  process exit.
- Ordering on a transition out of Active: swap handler → drain in-flight →
  close the site/journal composition → release lease (release is already inside
  `activate`). The site must close after the drain, exactly as the shutdown path
  orders it today.
- `runPassive`/`runActive` are re-entered by the loop; audit them for one-shot
  assumptions (they are mostly clean — each already opens and closes its own
  site).

### Explicit non-goals

- No state machine framework, no new goroutine architecture. The loop is a `for`
  in `ManageOwnership` plus a drain primitive on `instanceServer`.
- Process exit remains the correct outcome for: signal, publisher failure,
  listener death, site open failure while Active.

## Tasks

1. `redundancy`: restructure `ManageOwnership` into the loop; return-vs-continue driven
   by `ctx.Err()` and how Active/Passive ended. Unit tests updated (several
   currently assert one-shot behavior).
2. `app/server.go`: add drain-without-close to `instanceServer`.
3. `app/runtime.go`: `runActive` swaps back to the Passive handler and drains
   instead of shutting down when the process is not stopping; `runProcess` keeps
   one Passive handler value to swap back to.
4. Unit tests: an Active→Passive→Active cycle within one `ManageOwnership` call; the
   listener answers health throughout (no connection-refused window).

## Done when

- Failback: Standby steps down, stays alive, answers `/health` as Passive, and
  the Primary is Active — all without any process exiting.
- Step-down on lost lease behaves the same way.
- A stopping process (ctx canceled) still exits cleanly from either state, with
  the same event record as today.
- All existing redundancy and app tests pass (adjusted for the loop).

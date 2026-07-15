# Platform fabric

Follow-up items for `platform/internal/fabric` and its adapters.

## Create-if-absent is unsafe for a moment after a member joins

**Effort:** large. **Value:** high. **Kind:** known defect.

The fabric promises that `Create` stores a value only when the key is absent, and
that concurrent creates of one key produce exactly one winner. On the Olric
adapter that promise does not hold for a brief window after a member joins the
site.

### What happens

While Olric moves partition fragments to a newly joined member, `Put` with `NX`
consults a fragment that has not received the data yet. It reports the key as
absent, wins, and **overwrites the existing value permanently**. `Get` stays
correct throughout, so the fabric contradicts itself: a caller can read a key and
then successfully create it.

Measured on Olric v0.7.4, two members on loopback:

| | |
| --- | --- |
| Keys written by node A alone | 40 |
| Keys whose `Create` won a second time after node B joined | 26-27 |
| Reads that saw the key absent | 0 |
| Window | one poll round (sub-second), immediately after the join |
| After the window | stable again; no further false wins in 20s of polling |

Roughly two thirds of keys are affected, which matches the share of partitions
that move to the new member. Only keys written **before** the membership change
are exposed: their fragments are the ones in flight.

### Why the adapter is not at fault

`collection.Create` is `dmap.Put(ctx, key, value, olric.NX())`, mapping
`ErrKeyFound` to "did not win". That is the correct use of the API. Olric
describes itself as an AP, eventually consistent store whose atomic operations
are consistent "when the cluster is stable"; a join is precisely when it is not.
The gap is between what the fabric promises and what this backend can give, not a
mistake in translating between them.

### Why it matters

Registration's correctness rests entirely on create-if-absent (see
`platform/internal/registration/doc.go`). Two consequences, in increasing
severity:

1. **Duplicate phase events.** A confirmation re-created in the window is
   byte-identical, so site state stays correct, but the instance states
   `platform.registration.confirmed` twice. That breaks the events' contract that
   a fact is stated once, for a transition that happened once.
2. **Uniqueness can be violated.** A `POST` that should return `409` can instead
   win and overwrite an accepted claim, so two units could each believe they hold
   one registration key. This is the guarantee the whole use case is built on:
   `(unit_type, unit_id)` unique across the site.

The exposure is bounded in practice. A site whose machines all boot before
traffic arrives never opens the window with data in flight. It opens when a
machine joins a site that is already holding registrations, which is exactly what
`scenarios/dotnet_sdk_e2e_test.go` does on purpose.

### Reproducing it

Open two Olric fabrics on loopback with `fabricolric.Open`, write a batch of keys
through the first while it is alone, then open the second with the first's
memberlist address in `Join`. Poll every key with `Get` then `Create` for ~20s.
`Create` wins again for most keys on the first round after the join, and a
follow-up `Get` shows the overwritten value.

### Why it is not fixed here

There is no cheap correct fix at the adapter or the registration layer:

- **Waiting for the routing table to settle before serving** contradicts a fixed
  decision of the registration plan: a site boots in any order, and a machine
  whose peers are absent must start and serve anyway.
- **Olric's distributed lock** is subject to the same AP caveats, so it moves the
  window rather than closing it.
- **Read-back verification** can detect the overwrite but cannot undo it, and two
  racing writers would each verify their own value.

Closing it properly is a backend decision: either registration claims move to a
store that can offer compare-and-set across a membership change, or the fabric
narrows its promise and registration stops relying on it. Both are architecture
work well beyond the stage that found this, and the plan defers quorum and
persistence, which is where a real answer likely lives.

### Until then

`platform/internal/fabric/doc.go` states the limitation next to the promise it
qualifies. The two-node SDK scenario asserts what the platform actually does
rather than what it should do: it requires one `requested` and one `accepted` on
the origin and at least one `confirmed` per expected machine, with a comment
pointing here. Tightening those assertions back to exactly-one is the acceptance
test for this item.

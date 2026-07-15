# Platform fabric

## Olric join behavior and registration mitigation

**Kind:** architecture rationale. **Status:** implementation in progress.

Olric can temporarily violate create-if-absent while a member joins a site that
already holds data. The platform does not treat `Create` as a linearizable,
site-wide claim primitive across that transition. Instead, registration retains
every distinct contender and reconciles one winner after membership stabilizes.
Stages 3 through 6 in `docs/plan/` define and implement that mitigation.

### Measured behavior

While Olric moves partition fragments to a joining member, `Put` with `NX` can
consult a fragment that has not received the value yet. It reports the key as
absent, wins, and overwrites the previous value. In the reproduction, `Get`
continued to find the previous value while `Create` falsely reported success.

Measured on Olric v0.7.4 with two loopback members:

| Observation | Result |
| --- | --- |
| Keys written by node A alone | 40 |
| Keys whose Create won again after node B joined | 26-27 |
| Reads that saw the key absent | 0 |
| Window | One sub-second poll round immediately after join |
| Later polling | No further false wins in 20 seconds |

Only keys written before the membership change are exposed. The affected share
roughly matches the fragments moved to the new member.

### Why the adapter does not change

The Olric adapter correctly maps `Create` to `dmap.Put` with `olric.NX()` and
maps `ErrKeyFound` to a losing create. Olric is an AP store whose atomic
operations are consistent when the cluster is stable; a join is not stable.
Waiting for routing to settle would break the requirement that a static site can
boot in any order. Olric's distributed lock has the same AP limitation, and a
read-back check can observe an overwrite but cannot safely undo a race.

Changing the storage backend for a strong compare-and-set remains a future
architecture option. It is not required for the static, low-contention sites in
scope now.

### Registration response

The relaxed registration contract is:

1. Preserve every distinct proposal under its fingerprint.
2. Keep an accepted incumbent when a later proposal appears.
3. If contenders race before acceptance, choose the earliest platform-observed
   request time; use the fingerprint as a deterministic tie-break.
4. Treat cross-machine "first" as best effort because platform clocks are not
   coordinated.
5. Reconcile every loser to `rejected` with reason
   `registration_key_conflict` and repair current views to the winner.
6. Expose the result through the registrations API, including a dedicated
   conflict query. Push notifications and acknowledgement are later work.

The shared fabric contract suite still asserts exactly one Create winner on a
stable fabric. It deliberately makes no assertion across a member join. The
two-member regression scenario in Stage 6 is the acceptance test for eventual
registration convergence, not for a stronger fabric guarantee.

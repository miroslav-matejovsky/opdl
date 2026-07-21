# Stage 05: Independent Event Fabric per instance

**Effort:** Large. **Complexity:** High. **Depends on:** stage 04.

The largest stage, and the one with the failure this codebase has already had once.

## Intent

Each instance runs its own Event Fabric server on its own ports and joins the
site's cluster in its own right. A machine's two instances route to each other like
any other pair of servers.

## Current state

The descriptor resolves a NATS topology per instance. On a storage machine that
deploys both, the two records point at each other:

```json
"primary": {"nats": {"cluster_address": ":6222", "routes": [":6322"], "servers": [":4222", ":4322"]}},
"standby": {"nats": {"cluster_address": ":6322", "routes": [":6222"], "servers": [":4322", ":4222"]}}
```

The runtime reads neither. `platform/internal/eventfabric/nats/config.go`
`DefaultConfig` takes no instance role and reads
`descriptor.Instances.Primary.Nats` whichever instance is running, with a comment
saying why. `platform/internal/app/site.go` `clientOnly` then strips the standby's
listener addresses, routes, and data directory, keeping the server list whole so it
follows the Active instance's server as a client.

So today there is one server per machine and the Standby Instance is a client of
it.

## Why this must be one change

`clientOnly` exists because of a defect, and the comment in `nats/doc.go` records
it: deriving a separate endpoint for the standby left it retrying against an
address nothing was listening on.

That defect returns if either half of this stage lands alone:

- **Take the per-instance topology, keep `clientOnly`.** The standby's server list
  starts with its own client address, which it never binds. It retries forever.
- **Start the standby's server, keep reading the primary's topology.** Both
  instances bind the same ports. The second fails to start.

There is no intermediate state that works. Plan it as one change with one review.

## What changes

**Both instances start a server.** `clientOnly` is deleted, not adapted. A Passive
instance is a full cluster member; what makes it Passive is that it holds no
ownership, not that it is a lesser NATS participant.

**Each instance reads its own record.** `DefaultConfig` takes the role.

**Data directories split.** Two servers on one host cannot open the same JetStream
store. `nodeDataDir` currently derives from the deployment identity, which is the
machine; it must extend to the instance. This is also what makes the Standby
Instance's journal genuinely warm rather than borrowed.

**Server names split.** `cfg.ServerName` is `descriptor.Machine` today. Two servers
in one cluster cannot share a name.

## Replica placement is the part that will be missed

Storage is selected by **machine**, because a machine is the failure domain. Two
copies of the journal behind one power supply is one copy.

But every instance a storage machine deploys runs a server. A three-machine site
where every machine deploys both instances has **six servers on three failure
domains**, and `Replicas` is computed from the machine count, which stays correct
at three.

Nothing stops JetStream placing two of those three replicas on one host. Losing
that host then loses two of three replicas and the stream with them, which is worse
than the single-server arrangement this stage replaces.

NATS solves this with unique-tag placement: tag each server with its machine and
require replicas to span distinct tags. **This is not optional and it is not a
follow-up.** A site that survives one machine failure today must still survive it
after this stage.

## Quorum, restated

With one server per machine, a three-machine site had a three-member metadata group
and lost quorum at two failures.

With two servers per storage machine it has six members, quorum four. Losing one
machine removes two members and leaves exactly four: quorum holds with no margin.
Losing two machines leaves two: no quorum, same as before.

So machine-failure tolerance is unchanged, which is the right outcome. Confirm it
against a real cluster rather than trusting the arithmetic; JetStream's metadata
group and per-stream groups do not have to have the same membership.

## Decisions

**D1.** Does a Passive instance's server accept clients? It is a cluster member
either way. Recommendation: yes, and let its own projection read from it, which is
what makes failover fast. This is the difference between a warm standby and a cold
one.

**D2.** Placement mechanism: unique tags, or explicit per-stream placement?
Recommendation: unique tags keyed on the machine name. It is declarative, it
survives a site growing, and it cannot be forgotten per stream.

**D3.** Data directory layout. Recommendation: extend the existing per-machine
subdirectory with the instance role, so a machine's two stores sit side by side and
an operator can see both. Note that this doubles a machine's journal footprint, and
say so in `docs/operations/deployment.md`.

**D4.** What happens to a deployment upgrading into this? A machine that previously
ran one server has one store; the Primary Instance keeps it and the Standby
Instance starts empty and catches up. Confirm catch-up from empty is bounded, and
that `catch_up_timeout` is sized for it.

**D5.** Does the site's storage selection stay per-machine? Recommendation: yes,
explicitly, and write the reasoning into the resolver where the selection happens.
It is the least obvious thing in this stage and the most costly to get wrong later.

## Work

1. `DefaultConfig` takes the instance role and reads that instance's record.
2. Delete `clientOnly` and its call site.
3. Split the data directory per D3 and the server name per the machine plus role.
4. Add placement per D2.
5. Update the startup fabric log so it states which server this instance runs,
   which it routes to, and that the sibling is a peer.
6. Re-check `Replicas` against the machine count with the new membership.
7. Update `docs/01-architecture.md` on the site topology, and
   `docs/operations/deployment.md` on the storage footprint.

## Validation

- `task all` passes with the gate enabled.
- A scenario starts both instances of a one-machine site and both servers join one
  cluster, routing to each other.
- A scenario kills the machine hosting two replicas and the site still serves,
  which is what proves placement is constrained.
- Failover no longer involves rebinding a NATS listener, because neither instance
  ever binds the other's.
- The four-machine storage scenario still passes with the new membership.

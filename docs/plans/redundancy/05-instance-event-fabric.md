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

That much is already done and does not need deciding here. `siteIPs` in
`platform/internal/eventfabric/nats/config.go` collapses the descriptor's peers —
which are instances — back to machines before `StorageNodes` and `Replicas` see
them, and carries the reasoning in its own doc comment. `topology` in
`platform/internal/app/site.go` does the same for registration. Both survive this
stage unchanged; what does not survive is the assumption that one storage machine
means one server.

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

**D3.** Data directory layout. Settled: `data_dir` becomes a blueprint setting
authored per instance, exactly like `runtime_dir`, and is resolved onto the
instance's descriptor record. `event_fabric.nats.data_dir` leaves the configuration
file, and `nodeDataDir` is deleted rather than extended.

This is the same move stage 04 made for `runtime_dir`, applied to the last
per-instance resource still resolved somewhere else. Every other thing an instance
binds or writes — its API address, its NATS ports, its runtime directory — is
authored per instance and carried on its own descriptor record. The journal store
was the exception, derived by `nodeDataDir` from a machine-level file setting, and
this stage is where that exception stops working anyway: two instances on one
machine cannot share one derived path.

Authoring beats deriving here for the reason the descriptor exists. A derived path
means an operator looking at a machine's storage has to reproduce
`project-environment-site-machine-role` in their head to know which directory
belongs to which instance. An authored one is readable in the blueprint next to the
`runtime_dir` beside it, and the builder validates it the same way.

**What this trades away**, recorded so it is not rediscovered as a surprise:
relocating the journal to a different disk becomes a blueprint edit and a rebuild
rather than a file edit and a restart. That is consistent with how everything else
about a machine is changed in this architecture, and with the scope note in the plan
README, but it does mean `platform/config/file.go` loses the one setting it
described as a site's own decision. Its doc comment and the package doc both say so
today and both must be rewritten; `credentials_file` becomes the file's remaining
site-owned setting.

**Presence rule.** `data_dir` is authored on every deployed instance and carried on
every instance's descriptor record, whether or not that instance turns out to run a
store. Whether it is used follows `HostsStorage`.

This follows `cluster_address`, which the descriptor already treats exactly this
way: "present on every instance; it is bound only when Routes is non-empty". The
alternative — required on a storage machine's instances and rejected elsewhere —
was rejected because storage selection is *derived*. `siteStorageMachines` picks
the first three machines by sorted name, so making a field's requiredness depend on
it would mean a blueprint author has to reproduce that selection in their head to
know whether to write the field, and adding a machine to a site would silently
change which machines must author one.

Two stores per storage machine doubles its journal footprint. Say so in
`docs/operations/deployment.md`.

## Work

1. Blueprint: add `data_dir` beside `runtime_dir` on the instance, per D3. Validate
   it the way `runtime_dir` is validated, and reject a machine whose two instances
   author the same one.
2. Both descriptors, in lockstep: add `instances.*.data_dir`. The
   conformance-tests module is what proves they stayed in lockstep.
3. Resolver: carry `data_dir` onto each deployed instance's record.
4. Configuration file: remove `event_fabric.nats.data_dir`. A file that still sets
   it fails at `rejectUnknownKeys`, which is the intended outcome and needs no
   special case. Rewrite the `EventFabricNats` doc comment and the `platform/config`
   package doc, both of which currently call it the site's own decision.
5. `DefaultConfig` takes the instance role and reads that instance's record,
   including its `data_dir`. Delete `nodeDataDir`.
6. Delete `clientOnly` and its call site.
7. Split the server name per the machine plus role.
8. Add placement per D2.
9. Update the startup fabric log so it states which server this instance runs,
   which it routes to, and that the sibling is a peer.
10. Re-check `Replicas` against the machine count with the new membership.
11. Update `docs/01-architecture.md` on the site topology,
    `docs/operations/deployment.md` on the storage footprint and the removed file
    setting, and the example blueprint in `examples/customer-a/project.hcl`.

## Validation

- `task all` passes with the gate enabled.
- A scenario starts both instances of a one-machine site and both servers join one
  cluster, routing to each other.
- A scenario kills the machine hosting two replicas and the site still serves,
  which is what proves placement is constrained.
- Failover no longer involves rebinding a NATS listener, because neither instance
  ever binds the other's.
- The four-machine storage scenario still passes with the new membership.
- A Standby Instance whose store is empty reaches the journal's high-water mark
  within `catch_up_timeout`. Every standby starts empty on a machine's first boot
  now that the two instances no longer share a store, so this is the normal path
  rather than an unusual one, and a timeout sized for a warm store would fail it.

# Stage 07: Platform-to-service notifications over Named Pipes

**Effort:** Large. **Complexity:** Medium. **Depends on:** stage 04.

## Intent

The goal splits service communication by direction (goal:74-77):

- service to platform: commands, queries, configuration, health, over the existing
  local API;
- platform to service: notifications and events, over Windows Named Pipes;
- optionally, Primary/Standby coordination and health visibility, also over pipes.

The first is built. The second and third do not exist: there is no named pipe
anywhere in the repository.

## Why this matters more after stage 04

Once both instances bind their own API port, a service on the machine has two
endpoints and must know which one is Active. Polling two HTTP endpoints to find out
is the arrangement this stage exists to avoid.

A notification channel that tells a service the platform it is attached to has
become Active, or is about to stop being Active, is what makes the fixed-role model
usable by the services running on top of it.

## The constraint that shapes everything here

**Ownership decisions must always be based on Windows Named Mutex ownership and
never on inter-process communication state** (goal:77).

A pipe carries notifications *about* ownership. It never participates *in*
ownership. If this stage ends with any code path where a pipe message determines
which instance is Active, it has failed regardless of how well it works.

The practical test: unplug the pipe layer entirely and the platform must still
elect exactly one Active instance and still be correct. Notifications degrade;
ownership does not.

## Scope, and what to leave out

This is the largest greenfield stage in the plan and the easiest to over-build. A
notification channel can grow into a message bus if nobody stops it.

What a service needs, and no more:

| Notification | Why |
| --- | --- |
| This instance became Active | Attach, start work |
| This instance is stopping being Active | Detach cleanly before the transfer |
| This instance's health changed materially | Back off rather than fail |

Domain events are the Event Fabric's job and are already delivered there. This pipe
is about the *local platform instance's lifecycle*, not about the site's facts.
Keeping that line is decision D2 and it is the one that decides whether this stage
is Large or enormous.

## Design questions the stage must answer

**Pipe naming.** A machine runs two instances and each needs its own pipe, or one
pipe needs to disambiguate. Naming should derive from the same identity the
ownership object does, so a service on a machine with two instances can attach to
the right one and two deployments on one host cannot collide.

**Discovery.** How does a service find the pipe? Deriving the name from deployment
identity means a service needs that identity. Publishing it in the deployment
package is one option; a well-known name per machine is another.

**Lifecycle.** A service may start before the platform, outlive it, or reconnect
after a failover. The channel must be reconnectable and must not require ordering
between the two.

**Delivery semantics.** At-most-once is almost certainly right. A notification is a
hint to go and ask the API; it is not a fact to be relied on. Making it reliable
would recreate the Event Fabric on a pipe.

**Security.** A named pipe has a DACL. The ownership mutex already establishes the
pattern for deciding who may open a machine-scoped kernel object; follow it rather
than inventing a second answer.

## Decisions

**D1.** One pipe per instance, or one per machine with the instance identified in
the message? Recommendation: one per instance. It matches the independent-runtime
model, and a service that attached to the wrong instance would otherwise be a
silent misconfiguration.

**D2.** Is the pipe strictly lifecycle notifications, or general platform-to-service
events? Recommendation: strictly lifecycle, as scoped above. Revisit only with a
concrete service requirement that the Event Fabric cannot meet.

**D3.** Is the optional Primary/Standby coordination channel in scope here, or
deferred to stage 06 if it needs it? Recommendation: defer. Stage 06 recommends a
named kernel event for failback signalling precisely so the ownership path never
touches IPC. Building a coordination channel with no consumer would invite one.

**D4.** Does the .NET SDK gain a client for this? It is the obvious consumer, and
the SDK is generated from the OpenAPI document, which does not describe a pipe.
This is a hand-written addition or a separate package. Decide before designing the
wire format.

**D5.** Wire format. JSON lines match the operational event stream and are
inspectable; a binary framing is faster and nothing here is hot. Recommendation:
JSON lines.

## Work

1. Settle D1-D5. This stage is mostly design; the code follows quickly once the
   shape is agreed.
2. A pipe server in the platform, opened at startup by both instances, independent
   of ownership.
3. Notification emission at the existing activation and deactivation points, which
   already emit operational events, so the call sites exist.
4. Naming derived from deployment identity, and a DACL following the ownership
   object's precedent.
5. A client, per D4.
6. Document the channel in `docs/` as a contract: what is delivered, what is not,
   and that it is a hint rather than a source of truth.

## Validation

- `task all` passes with the gate enabled.
- A scenario attaches a fake service to both instances of a machine, kills the
  Primary, and observes the deactivation and activation notifications in order.
- A service that attaches before the platform starts, and one that reconnects after
  a failover, both work.
- Killing the pipe layer entirely leaves ownership and activation unaffected, which
  is the goal:77 test.
- No ownership decision reads pipe state.

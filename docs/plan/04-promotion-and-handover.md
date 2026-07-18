# Stage 4: Promotion and handover

## Outcome

Promote a caught-up standby after active process death or graceful shutdown,
without overlapping active capabilities or duplicating domain decisions.

Complexity: High.

Estimated time: 4-7 engineering days.

Depends on: [Stage 3](03-warm-standby-runtime.md).

## Work

1. Deployment starts the preferred primary first. It acquires the active fence
   before composing the runtime. The optional standby starts afterward, opens a
   projection-only runtime, and waits for the same fence while its projector
   remains live.
   A storage-machine standby keeps every storage-node client address, preferring
   its co-located server while it is available.
   Fence waiting is independent of the projector connection. Losing the local
   server must not make a single-storage-node standby exit before it can acquire
   the fence and reopen that server during promotion.
2. Promotion begins only after fence acquisition. A standby must never infer
   active ownership from a missing status file, closed port, timeout, or NATS
   disconnect alone.
3. Promote in this order:
   - mark the process `activating` locally;
   - stop its client-only Event Fabric composition;
   - on a storage machine, open the embedded NATS server and retained journal
     under the fence;
   - start the projector and catch up to a new high-water mark;
   - attach the machine-scoped durable handlers and drain their retained work;
   - catch up to handler consequences;
   - publish instance-aware readiness and wait until it is projected;
   - bind the existing public HTTP address and mark the process `active`.
4. Keep the fence held until HTTP has drained, handlers and projectors have
   stopped, the embedded NATS server and storage have closed, and active status
   is cleared.
5. Preserve idempotency across the gap. The promoted handler uses the same
   machine-scoped durable consumer and deterministic decision IDs, so an input
   held by the old active can be redelivered safely.
6. Define controlled handover through existing process lifecycle: start or
   verify the new process as caught-up standby, gracefully stop the active through
   the service manager, then wait for the standby status to become active. Do
   not add a remote promotion API.
7. Make the primary permanently preferred. When it returns after standby
   promotion, run it projection-only until caught up, then use the service
   manager to gracefully stop the promoted standby. The primary acquires the
   released fence and activates. It never steals the lock from a live standby.
8. Define full machine shutdown as primary-service stop followed by
   standby-service stop. Bounded standby promotion between those operations is
   allowed. Add explicit logs for promotion and primary reclamation.
9. Add process role and active state to lifecycle event payloads and operational logs.
   Keep the event envelope node and domain payloads machine-scoped.
10. Bound activation with startup and catch-up timeouts. A failed activation keeps
   the fence until opened resources are closed, then exits so the service manager
   can restart it.

## Exit criteria

- Only the fence owner ever runs handlers, server storage, or public HTTP.
- A process kill releases the fence and causes automatic standby promotion.
- Redelivery across failover emits no duplicate logical decision.
- A planned stop drains accepted HTTP work before ownership transfers.
- Stopping the primary and standby services leaves no process running.

## Open questions and recommendations

- How does deployment tooling know the standby is ready for handover?
  Recommendation: read the local status file and require `standby`, caught-up
  sequence, and no last error before stopping the active.
- Should the active transfer its listener directly?
  Recommendation: no for the POC. Close and rebind under the fence. Measure the
  gap and require client retries rather than adding platform-specific descriptor
  passing now.
- Is rollback to an older active supported after the new process publishes newer
  event schemas?
  Recommendation: no. Mixed versions are allowed only while the new process is a
  non-publishing standby. Promotion is the rollback boundary during the POC.

## Risks

- A fence released before NATS storage closes can let two servers touch the same
  journal directory.
- Stopping the active before verifying standby catch-up turns planned handover
  into cold restart.
- A service manager that fails to complete the second stop can leave a standby
  promoted during machine shutdown.

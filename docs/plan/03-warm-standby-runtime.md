# Stage 3: Warm standby runtime

## Outcome

Run a second process that continuously rebuilds and follows local projections
without acquiring active-only capabilities.

Complexity: High.

Estimated time: 5-8 engineering days.

Depends on: [Stage 2](02-descriptor-and-package.md).

## Work

1. Add `-instance primary|standby` parsing and validate it against the embedded
   instance policy before opening any socket or storage.
2. Add a required local `instance_dir` setting. Derive one machine directory for
   the shared fence and one diagnostic/status file per process. Reject a path that
   is not writable. Document that both processes must use the same local filesystem.
3. Implement the exclusive process fence behind `internal/redundancy`, with small
   OS-specific files where required. Test real cross-process exclusion and
   automatic release after process death on supported operating systems.
4. Split runtime composition into projection-only and active layers:
   - projection-only opens an Event Fabric client, starts the continuous
     projector, catches up, monitors live progress, and owns no handler or HTTP
     listener;
   - active adds durable handlers, drains retained work, publishes readiness,
     and serves HTTP.
5. Add a client-only NATS adapter open mode for a standby on a storage machine.
   It must never bind client, cluster, or monitoring ports and must never open
   the shared JetStream data directory.
6. Give each process a distinct NATS connection and projector identity. Keep the
   durable registration-handler identity machine-scoped and attach it only while
   active.
7. Track projector applied sequence, journal high-water sequence, lag duration,
   process state, PID, and last error in memory. Write them atomically to a local
   status file for deployment diagnostics. Initial and later write failures stop
   the process so deployment tooling never acts on known-stale status. The file is
   not an active fence; the OS lock remains authoritative.
8. Require a positive live lag bound. A standby that exceeds it is not
   promotable. An active that exceeds it stops serving rather than answering
   from stale projections.
9. Ensure cancellation stops projector consumption promptly without turning
   cancellation into a processing failure. Fence waiting begins in Stage 4.

## Exit criteria

- The primary and standby can run together while only one owns active capabilities.
- The standby catches up and follows new journal events.
- A storage-machine standby opens neither listeners nor JetStream files.
- No standby publishes registration decisions or node readiness.
- A lagging projection cannot activate or continue serving.

## Open questions and recommendations

- Should standby status be a public HTTP endpoint?
  Recommendation: no. The standby intentionally owns no public listener. Use an
  atomic local status file for the POC and add a local control API only when a
  real deployment tool needs one.
- Can the active and standby share local projection snapshots?
  Recommendation: no. Each process owns its local cache. Full replay remains the
  baseline until measured startup cost justifies snapshots.
- Which process owns the embedded NATS server on a storage machine?
  Recommendation: only the fence owner. The standby uses client-only mode and
  reopens the server during promotion.

## Risks

- Accidentally reusing active NATS configuration in standby can corrupt shared
  storage or fail on duplicate listeners.
- A status file can be stale after a crash. Never treat it as a fence.
- Continuous projection doubles read and decode work. Measure resource usage
  before adding more standby consumers.

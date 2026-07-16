# Stage 3: Durable local storage

Estimate: 9 person-days.

Complexity: high.

## Objective

Persist acknowledged registration state in two owner-local stores and
synchronize primary and secondary directly. Prove that loss of one platform
process does not lose acknowledged state while the machine filesystem remains
available. Do not use Olric to synchronize the local pair.

## Storage model

Each configured platform instance owns a distinct persistent store:

- primary writes only the primary store and reads both stores;
- secondary writes only the secondary store and reads both stores;
- a primary-only machine has one read/write store and no sibling reader;
- a process never writes through the handle opened for its sibling.

An OPDL-owned pull reconciler scans the sibling store and imports missing
immutable facts into its own store. The import is idempotent by stable fact ID.
Start with full deterministic scans because registration has no retention or
high-volume requirement. Do not add cursors, notifications, or a general
replication framework until measurements require them.

Synchronization is asynchronous. A newly acknowledged fact can temporarily
exist only in the writer's store. The surviving process must therefore retain
read access to both stores and import any missing facts after a process loss.

Olric remains the transient site-wide exchange layer between machines. It is
not the local synchronization channel or the durability authority.

## Implementation steps

1. Define a registration-owned storage abstraction with separate reader and
   writer capabilities. Express only fact read, idempotent local write,
   deterministic enumeration, and close behavior. Do not expose file, JSON,
   SQLite, or another backend detail to registration rules.
2. Give the selected process one writer for its own store and readers for its
   own and optional sibling store. Reject duplicate paths, wrong owner identity,
   unsupported format versions, and a writable sibling handle before serving.
3. Implement the initial file backend with a versioned format and an atomic,
   crash-safe write protocol. Flush the file and required directory metadata
   before reporting success. Readers must never consume a partial write.
4. Represent recovery state as immutable facts with stable IDs. Persist the
   minimum facts needed to reconstruct proposals, machine confirmations,
   acceptance evidence, and conflict contenders. Treat mutable request and
   accepted records as rebuildable projections.
5. Implement direct sibling synchronization. At startup and before each normal
   registration reconciliation pass, enumerate the sibling reader and import
   missing facts into the local writer. Reuse the registration reconciliation
   interval instead of adding another timing option.
6. Make scans deterministic, cancelable, safe when both processes synchronize
   concurrently, and quiet when already converged. A fact ID with different
   content is corruption or incompatibility and must fail visibly.
7. Keep local synchronization separate from site-wide fabric exchange. Durable
   facts may be projected into the fabric, and remote facts read from the fabric
   may be persisted locally, but no Olric callback, subscription, event, relay,
   membership result, or replica setting may synchronize the paired stores.
8. Define readiness. A process may serve after its own store opens, startup
   synchronization completes against every readable local store, and durable
   projections are rebuilt. A stopped sibling process is not a blocker when its
   store remains readable. An unreadable configured sibling store fails
   readiness because acknowledged facts might be hidden.
9. Require deployment provisioning to create both empty store locations before
   either process first starts. This prevents a missing store from being treated
   as an empty store after data loss.
10. Add storage and synchronization health diagnostics with machine, instance,
    owner, path, format version, last successful scan, and actionable errors.
    Do not log stored content or one event per copied fact.
11. Add package documentation for the abstraction, file backend, immutable fact
    rules, direct synchronizer, crash guarantees, and excluded failures.

## Tests

- Each instance writes its own store and cannot write through the sibling reader
  capability.
- A fact written by primary is copied into secondary's store, and the reverse,
  with Olric stopped or unavailable.
- Repeated and concurrent synchronization is idempotent and produces identical
  fact sets.
- A hard kill after an acknowledged write leaves a complete fact readable by
  the sibling, including before the periodic copy runs.
- Restart after a hard kill rebuilds the same durable facts from both stores.
- A partial or interrupted file write is never exposed as a valid fact. The
  previous durable state remains readable.
- Unsupported store format, duplicate paths, unreadable sibling storage, and
  fact ID/content mismatch fail with actionable errors.
- Primary-only storage starts, persists, and recovers without sibling sync.
- Full scans honor cancellation and do not depend on enumeration order.
- File backend contract tests run on every supported target OS. An equivalent
  in-memory backend passes the same storage and synchronizer behavior tests.

## Estimate boundary

The estimate includes four days for the abstraction and file backend, three
days for direct synchronization and recovery composition, and two days for
focused failure tests and documentation. It excludes SQLite, migration from a
previous persistent format, retention, compaction, encryption at rest, backup
tooling, and OS-specific service-account or ACL setup.

## Exit criteria

- Primary and secondary stores converge through the direct OPDL synchronizer
  while Olric is unavailable.
- A surviving process reads acknowledged facts from both stores and can repair
  its own store after sibling process loss.
- The approved single-process-loss guarantee has an automated crash test.
- Registration behavior depends on the storage abstraction, not the file
  backend.
- Limitations do not imply disk or whole-machine durability.
- `task all` passes when this implementation stage is executed.

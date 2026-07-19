# `utils/filelock`

## Functionality

Provide an exclusive local file lock that is released explicitly or
automatically when the owning process exits. Support non-blocking acquisition
and context-cancelable waiting on Windows and Unix.

## Extraction source and consumers

- Move the generic lock lifecycle from `platform/internal/redundancy/fence.go`.
- Move `tryLock` and `unlock` from `fence_windows.go` and `fence_unix.go`.
- Keep `FencePath`, `StatusPath`, process roles, OPDL error prefixes, and the rule
  that only a fence holder may become active in `platform/internal/redundancy`.
- Make `redundancy.Fence` a thin domain wrapper around `filelock.Lock`, or use the
  lock directly behind the existing `Fence` API.

## Package boundary

The package accepts a raw path. It must not know machines, roles, active state,
or deployment identity. Its minimal API should support opening a lock,
`TryAcquire`, `Acquire(ctx)`, `Release`, `Held`, and `Path`. Acquisition and
release must be safe for concurrent calls and idempotent for the current owner.

Document these invariants:

- The filesystem must be local and support the selected OS lock primitive.
- Unix locks are advisory. Every contender must use the package.
- A held lock has no lease or expiry.
- Context cancellation stops waiting but never releases a lock already held.
- Closing the process releases the operating-system lock.

Keep the retry interval internal unless a demonstrated consumer needs to tune
it. Use a reusable timer instead of allocating a new timer on every poll.

## Tests and documentation

- Acquire, repeated acquire, release, repeated release, and reacquire.
- Two lock objects in one process remain exclusive.
- Separate processes remain exclusive.
- A waiter acquires after graceful release and after forced owner death.
- A canceled or expired context ends a wait without acquiring.
- Unexpected open, lock, and unlock errors preserve the path and operation.
- Run the process tests on Windows and Unix. Do not replace them with mocks of
  operating-system calls.

## Refactoring steps

1. Create `utils/filelock` with package documentation and cross-process tests.
2. Move only path-independent locking code into it.
3. Adapt `redundancy.Fence` without changing its public behavior or lifecycle
   ordering.
4. Keep the existing redundancy tests as domain-level regression tests.

## Completion criteria

The utility has no OPDL concepts, crash release and exclusivity are proven in
separate processes on supported operating systems, the platform retains its
fence contract, and `task all` passes.

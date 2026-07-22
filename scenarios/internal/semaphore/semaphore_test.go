package semaphore

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSemaphoreAcquireRelease(t *testing.T) {
	s := New(5)
	ctx := t.Context()

	require.NoError(t, s.Acquire(ctx, 3))
	require.NoError(t, s.Acquire(ctx, 2))

	// Next acquire should block
	ctxCancel, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, s.Acquire(ctxCancel, 1), context.DeadlineExceeded)

	s.Release(2)
	require.NoError(t, s.Acquire(ctx, 1))
}

func TestSemaphoreFIFOOrderAndWeighting(t *testing.T) {
	s := New(4)
	ctx := t.Context()

	require.NoError(t, s.Acquire(ctx, 4))

	var first, second atomic.Bool
	var wg sync.WaitGroup

	// Waiter 1 wants 3 units (at head of queue)
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = s.Acquire(ctx, 3)
		first.Store(true)
	}()

	time.Sleep(10 * time.Millisecond)

	// Waiter 2 wants 1 unit (behind Waiter 1 in queue)
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = s.Acquire(ctx, 1)
		second.Store(true)
	}()

	time.Sleep(10 * time.Millisecond)

	// Release 1 unit. Even though Waiter 2 only needs 1 unit, Waiter 1 is at the head of FIFO queue requiring 3 units, so Waiter 2 must not jump the queue.
	s.Release(1)
	time.Sleep(20 * time.Millisecond)
	require.False(t, first.Load(), "waiter 1 needs 3 units and only 1 was released")
	require.False(t, second.Load(), "waiter 2 must not jump ahead of waiter 1")

	// Release 2 more (3 free in total). That is exactly waiter 1's weight, so it
	// is granted and waiter 2 stays blocked. Releasing enough for both at once
	// would say nothing about order: both waiters would be woken by the same
	// notify, and which goroutine runs first after that is up to the scheduler,
	// not the semaphore.
	s.Release(2)
	require.Eventually(t, first.Load, time.Second, time.Millisecond)
	require.False(t, second.Load(), "no capacity is left for waiter 2 yet")

	// Waiter 1's weight coming back is what finally lets waiter 2 through.
	s.Release(3)
	wg.Wait()
	require.True(t, second.Load())
}

// A weight larger than the capacity is clamped rather than blocking forever, and
// releasing that same weight must not hand back more than was taken. The
// scenario suite depends on both halves: it sizes a budget from GOMAXPROCS and
// then acquires one unit per machine, which on a small host exceeds the budget.
func TestSemaphoreWeightLargerThanCapacity(t *testing.T) {
	s := New(2)
	ctx := t.Context()

	require.NoError(t, s.Acquire(ctx, 4))

	// The clamped acquire took the whole capacity, so nothing is left.
	blocked, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, s.Acquire(blocked, 1), context.DeadlineExceeded)

	// Releasing the weight that was requested must restore the capacity exactly,
	// not inflate it. If Release over-credited, the second acquire below would
	// leave room for a third one.
	s.Release(4)
	require.NoError(t, s.Acquire(ctx, 2))

	exceeded, cancelExceeded := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancelExceeded()
	require.ErrorIs(t, s.Acquire(exceeded, 1), context.DeadlineExceeded)
}

func TestSemaphoreContextCancellationAndCleanup(t *testing.T) {
	s := New(2)
	ctx := t.Context()

	require.NoError(t, s.Acquire(ctx, 2))

	cancelCtx, cancel := context.WithCancel(ctx)
	var err error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		err = s.Acquire(cancelCtx, 2)
	}()

	time.Sleep(10 * time.Millisecond)
	cancel()
	wg.Wait()

	require.ErrorIs(t, err, context.Canceled)

	// After canceled acquisition cleaned up, another acquisition should succeed once released.
	var acquired atomic.Bool
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = s.Acquire(ctx, 2)
		acquired.Store(true)
	}()

	s.Release(2)
	wg.Wait()
	require.True(t, acquired.Load())
}

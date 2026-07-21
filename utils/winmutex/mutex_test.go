package winmutex_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/miroslav-matejovsky/opdl/utils/winmutex"
	"github.com/stretchr/testify/require"
)

// uniqueName returns an object name no other test or run uses.
//
// Mutex names live in a machine-wide namespace, so unlike a lock file under
// t.TempDir() they are not isolated by the test framework. Every test must
// generate its own name or parallel tests would contend with each other.
func uniqueName(t *testing.T) string {
	t.Helper()
	suffix := make([]byte, 8)
	_, err := rand.Read(suffix)
	require.NoError(t, err)
	return "opdl-winmutex-test-" + hex.EncodeToString(suffix)
}

func open(t *testing.T, name string) *winmutex.Mutex {
	t.Helper()
	m, err := winmutex.Open(name)
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })
	return m
}

func TestOpenRejectsUnusableNames(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"empty":     "",
		"blank":     "   ",
		"backslash": `Global\already-qualified`,
		"too long":  string(make([]byte, 300)),
	}
	for name, object := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := winmutex.Open(object)
			require.Error(t, err)
		})
	}
}

func TestOpenPlacesTheObjectInTheGlobalNamespace(t *testing.T) {
	t.Parallel()

	name := uniqueName(t)
	m := open(t, name)
	require.Equal(t, `Global\`+name, m.Name())
}

func TestTryAcquireGrantsOwnershipAndExcludesAnother(t *testing.T) {
	t.Parallel()

	name := uniqueName(t)
	holder, contender := open(t, name), open(t, name)

	outcome, err := holder.TryAcquire()
	require.NoError(t, err)
	require.Equal(t, winmutex.Acquired, outcome)
	require.True(t, holder.Held())

	// A second contender must not get in, and that is not an error.
	outcome, err = contender.TryAcquire()
	require.NoError(t, err)
	require.Equal(t, winmutex.NotAcquired, outcome)
	require.False(t, contender.Held())
}

func TestReleaseLetsAnotherContenderIn(t *testing.T) {
	t.Parallel()

	name := uniqueName(t)
	holder, contender := open(t, name), open(t, name)

	_, err := holder.TryAcquire()
	require.NoError(t, err)
	require.NoError(t, holder.Release())
	require.False(t, holder.Held())

	outcome, err := contender.TryAcquire()
	require.NoError(t, err)
	require.Equal(t, winmutex.Acquired, outcome, "a cleanly released mutex must not report abandonment")
}

func TestAcquireWaitsUntilTheHolderReleases(t *testing.T) {
	t.Parallel()

	name := uniqueName(t)
	holder, waiter := open(t, name), open(t, name)

	_, err := holder.TryAcquire()
	require.NoError(t, err)

	acquired := make(chan winmutex.Outcome, 1)
	go func() {
		outcome, waitErr := waiter.Acquire(context.Background())
		if waitErr != nil {
			acquired <- winmutex.NotAcquired
			return
		}
		acquired <- outcome
	}()

	// The waiter must still be waiting while the holder owns the mutex.
	select {
	case <-acquired:
		require.Fail(t, "the waiter acquired a mutex another contender holds")
	case <-time.After(50 * time.Millisecond):
	}

	require.NoError(t, holder.Release())
	require.Equal(t, winmutex.Acquired, <-acquired)
}

// TestHeldDoesNotBlockBehindABlockingAcquire is a regression guard.
//
// Acquire parks the owner thread in a kernel wait for as long as another process
// holds the mutex. A Held that shared Acquire's lock would therefore block until
// the wait it is being asked about had already finished. The standby does exactly
// this: one goroutine waits for the fence while another polls Held to decide
// whether to keep composing, so the deadlock is on the real promotion path.
func TestHeldDoesNotBlockBehindABlockingAcquire(t *testing.T) {
	t.Parallel()

	name := uniqueName(t)
	holder, waiter := open(t, name), open(t, name)

	_, err := holder.TryAcquire()
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waiting := make(chan struct{})
	go func() {
		close(waiting)
		_, _ = waiter.Acquire(ctx)
	}()
	<-waiting

	answered := make(chan bool, 1)
	go func() { answered <- waiter.Held() }()

	select {
	case held := <-answered:
		require.False(t, held, "the waiter does not hold the mutex yet")
	case <-time.After(2 * time.Second):
		require.Fail(t, "Held blocked while Acquire was waiting")
	}
}

func TestAcquireIsCancellable(t *testing.T) {
	t.Parallel()

	name := uniqueName(t)
	holder, waiter := open(t, name), open(t, name)

	_, err := holder.TryAcquire()
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancelled := make(chan error, 1)
	go func() {
		_, waitErr := waiter.Acquire(ctx)
		cancelled <- waitErr
	}()
	cancel()

	require.ErrorIs(t, <-cancelled, context.Canceled)
	require.False(t, waiter.Held(), "a canceled wait must not grant ownership")

	// Canceling the waiter must not have disturbed the holder.
	require.True(t, holder.Held())
}

func TestAcquireAfterCancellationStillWorks(t *testing.T) {
	t.Parallel()

	name := uniqueName(t)
	waiter := open(t, name)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := waiter.Acquire(ctx)
	require.ErrorIs(t, err, context.Canceled)

	// The cancellation event must have been reset, so an uncontested mutex is
	// still acquirable afterwards.
	outcome, err := waiter.Acquire(context.Background())
	require.NoError(t, err)
	require.Equal(t, winmutex.Acquired, outcome)
}

func TestReleaseAndCloseAreIdempotent(t *testing.T) {
	t.Parallel()

	m := open(t, uniqueName(t))
	_, err := m.TryAcquire()
	require.NoError(t, err)

	require.NoError(t, m.Release())
	require.NoError(t, m.Release())
	require.NoError(t, m.Close())
	require.NoError(t, m.Close())
}

func TestCloseReleasesOwnershipItStillHolds(t *testing.T) {
	t.Parallel()

	name := uniqueName(t)
	holder, contender := open(t, name), open(t, name)

	_, err := holder.TryAcquire()
	require.NoError(t, err)
	require.NoError(t, holder.Close())

	// Closing without releasing must hand over cleanly rather than abandoning.
	outcome, err := contender.TryAcquire()
	require.NoError(t, err)
	require.Equal(t, winmutex.Acquired, outcome)
}

func TestOperationsOnAClosedMutexFail(t *testing.T) {
	t.Parallel()

	m, err := winmutex.Open(uniqueName(t))
	require.NoError(t, err)
	require.NoError(t, m.Close())

	_, err = m.TryAcquire()
	require.Error(t, err)
	_, err = m.Acquire(context.Background())
	require.Error(t, err)
}

func TestExistedReportsWhetherAPeerCreatedTheObject(t *testing.T) {
	t.Parallel()

	name := uniqueName(t)
	first := open(t, name)
	require.False(t, first.Existed(), "the first opener created the object")

	second := open(t, name)
	require.True(t, second.Existed(), "the second opener found the first process's object")
}

// TestOwnershipIsNotBoundToTheCallingGoroutine is the thread-affinity guard.
//
// Windows ties mutex ownership to the thread that acquired it. An implementation
// that called the Win32 API directly from whichever goroutine invoked it would
// acquire on one OS thread and release from another, and ReleaseMutex would fail
// with ERROR_NOT_OWNER. Both goroutines below pin themselves to distinct OS
// threads to make that failure deterministic rather than dependent on scheduling.
func TestOwnershipIsNotBoundToTheCallingGoroutine(t *testing.T) {
	t.Parallel()

	m := open(t, uniqueName(t))

	acquired := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		_, err := m.TryAcquire()
		acquired <- err
	}()
	require.NoError(t, <-acquired)
	require.True(t, m.Held())

	released := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		released <- m.Release()
	}()
	require.NoError(t, <-released, "ownership must survive being released from another goroutine")
	require.False(t, m.Held())
}

// TestOwnershipSurvivesGarbageCollection guards the other half of thread
// affinity: the owner thread must stay alive while ownership is held, even when
// nothing in the caller's goroutine references the Mutex in between.
func TestOwnershipSurvivesGarbageCollection(t *testing.T) {
	t.Parallel()

	name := uniqueName(t)
	holder, contender := open(t, name), open(t, name)

	_, err := holder.TryAcquire()
	require.NoError(t, err)

	runtime.GC()
	runtime.Gosched()

	outcome, err := contender.TryAcquire()
	require.NoError(t, err)
	require.Equal(t, winmutex.NotAcquired, outcome, "ownership was lost while the holder was idle")
}

func TestOutcomeReporting(t *testing.T) {
	t.Parallel()

	require.True(t, winmutex.Acquired.Held())
	require.True(t, winmutex.AcquiredAbandoned.Held(), "an abandoned mutex is still owned")
	require.False(t, winmutex.NotAcquired.Held())
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

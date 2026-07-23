package redundancy_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"testing"
	"time"

	"github.com/miroslav-matejovsky/opdl/platform/internal/redundancy"
	"github.com/stretchr/testify/require"
)

// ownershipObject returns an ownership object name no other test or run uses.
//
// The ownership lives in a machine-wide kernel namespace, so unlike the lock file it
// replaced it is not isolated by t.TempDir(). Every test generates its own name or
// parallel tests would contend for each other's ownership.
//
// The name carries the Global\ prefix because that is the shape a deployment
// descriptor holds, and OpenLock's whole job at this seam is to take it off. These
// tests once passed a bare name, which meant every one of them exercised a shape
// no descriptor ever produces: production names were rejected outright and no test
// noticed. Keep the descriptor's shape here.
func ownershipObject(t *testing.T) string {
	t.Helper()
	suffix := make([]byte, 8)
	_, err := rand.Read(suffix)
	require.NoError(t, err)
	return `Global\opdl-ownership-test.` + hex.EncodeToString(suffix)
}

func openLock(t *testing.T, object string, role redundancy.InstanceRole) *redundancy.Lock {
	t.Helper()
	f, err := redundancy.OpenLock(object, role)
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func TestOpenLockRejectsInvalidRole(t *testing.T) {
	t.Parallel()

	_, err := redundancy.OpenLock(ownershipObject(t), redundancy.InstanceRole("other"))
	require.Error(t, err)
}

// TestOpenLockRejectsAnEmptyObject guards the descriptor contract: a machine
// whose descriptor carries an empty lock object fails to open.
func TestOpenLockRejectsAnEmptyObject(t *testing.T) {
	t.Parallel()

	_, err := redundancy.OpenLock("   ", redundancy.RolePrimary)
	require.ErrorContains(t, err, "empty windows_mutex")
}

// TestOpenLockAcceptsTheDescriptorsQualifiedName is the regression test for a
// defect that made every machine deploying a Standby Instance fail at startup.
//
// The builder requires an authored windows_mutex to begin with Global\, and the
// descriptor carries it that way. utils/winmutex takes a bare name and rejects a
// backslash, because it applies the namespace itself. OpenLock passed the
// descriptor's name straight through, so the only names that ever worked were the
// ones no descriptor produces.
//
// Nothing caught it. The compiler cannot; the embedded mock descriptor disables
// its standby and therefore carries no lock at all, so the platform's own tests
// never open one; and these tests generated bare names. Only a machine with a
// standby actually deployed reaches the call, which is a scenario.
func TestOpenLockAcceptsTheDescriptorsQualifiedName(t *testing.T) {
	t.Parallel()

	lock := openLock(t, ownershipObject(t), redundancy.RolePrimary)
	acquired, err := lock.TryAcquire()
	require.NoError(t, err)
	require.True(t, acquired.Held, "a descriptor-shaped name must open and acquire")
	require.NoError(t, lock.Release())
}

// TestOpenLockRejectsAnUnqualifiedObject refuses a name the builder would never
// emit. Ownership is machine-wide: an object in the session-scoped Local\
// namespace, or one with no namespace authored at all, would let both instances
// hold their own object and both be Active, with no error anywhere to say so.
func TestOpenLockRejectsAnUnqualifiedObject(t *testing.T) {
	t.Parallel()

	for name, object := range map[string]string{
		"no namespace":    "opdl-ownership-test.unqualified",
		"session scoped":  `Local\opdl-ownership-test.session`,
		"wrong namespace": `Session\1\opdl-ownership-test.other`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := redundancy.OpenLock(object, redundancy.RolePrimary)
			require.ErrorContains(t, err, `must start with Global\`)
		})
	}
}

func TestOpenLockWithNilYieldsNilSafeLock(t *testing.T) {
	t.Parallel()

	lock, err := redundancy.OpenLock("", redundancy.RolePrimary)
	require.NoError(t, err)
	require.Nil(t, lock)

	acquired, err := lock.TryAcquire()
	require.NoError(t, err)
	require.True(t, acquired.Held)
	require.False(t, acquired.Abandoned)

	acquired, err = lock.Acquire(t.Context())
	require.NoError(t, err)
	require.True(t, acquired.Held)
	require.NoError(t, lock.Release())
	require.NoError(t, lock.Close())
	require.True(t, lock.Held())
	require.False(t, lock.Existed())
	require.Equal(t, "", lock.Name())
	require.Equal(t, redundancy.RolePrimary, lock.Role())
}

func TestOwnershipAcquireReleaseCycle(t *testing.T) {
	t.Parallel()

	object := ownershipObject(t)
	f := openLock(t, object, redundancy.RolePrimary)
	require.Equal(t, redundancy.RolePrimary, f.Role())
	require.Equal(t, object, f.Name(), "the object opened is the descriptor's name, namespace included")
	require.False(t, f.Held())

	acquired, err := f.TryAcquire()
	require.NoError(t, err)
	require.True(t, acquired.Held)
	require.False(t, acquired.Abandoned)
	require.True(t, f.Held())

	// TryAcquire is idempotent for the process that already holds the ownership.
	acquired, err = f.TryAcquire()
	require.NoError(t, err)
	require.True(t, acquired.Held)

	require.NoError(t, f.Release())
	require.False(t, f.Held())

	// Release is idempotent.
	require.NoError(t, f.Release())
}

// TestOwnershipIsExclusive checks two contenders never hold the machine ownership
// together: while one holds it, the other's TryAcquire fails and its blocking
// Acquire waits out its context rather than acquiring a second copy.
func TestOwnershipIsExclusive(t *testing.T) {
	t.Parallel()

	object := ownershipObject(t)
	a := openLock(t, object, redundancy.RolePrimary)
	b := openLock(t, object, redundancy.RoleStandby)

	acquired, err := a.TryAcquire()
	require.NoError(t, err)
	require.True(t, acquired.Held)

	// B finds the ownership held: TryAcquire is a clean "no", not an error.
	acquired, err = b.TryAcquire()
	require.NoError(t, err)
	require.False(t, acquired.Held)
	require.False(t, b.Held())

	// A bounded blocking wait ends in its deadline, not in a second holder.
	waitCtx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	_, err = b.Acquire(waitCtx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.False(t, b.Held())

	// Once A releases, B can become active.
	require.NoError(t, a.Release())
	acquired, err = b.Acquire(t.Context())
	require.NoError(t, err)
	require.True(t, acquired.Held)
	require.False(t, acquired.Abandoned, "a clean release must not look like a crash")
	require.True(t, b.Held())
	require.NoError(t, b.Release())
}

func TestOwnershipAcquireStopsOnCanceledContext(t *testing.T) {
	t.Parallel()

	object := ownershipObject(t)
	holder := openLock(t, object, redundancy.RolePrimary)
	acquired, err := holder.TryAcquire()
	require.NoError(t, err)
	require.True(t, acquired.Held)
	defer func() { require.NoError(t, holder.Release()) }()

	standby := openLock(t, object, redundancy.RoleStandby)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = standby.Acquire(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, standby.Held())
}

// TestOwnershipIdentityIsIndependentOfTheLocalFilesystem is the property the
// file lock could not provide. Ownership comes from the descriptor's object
// name, so two processes given different local directories still exclude each
// other. Under the previous ownership they would each have taken their own lock
// file and both become active.
func TestOwnershipIdentityIsIndependentOfTheLocalFilesystem(t *testing.T) {
	t.Parallel()

	object := ownershipObject(t)
	a := openLock(t, object, redundancy.RolePrimary)
	b := openLock(t, object, redundancy.RoleStandby)

	// Distinct local directories, which is what a misconfigured pair would have.
	require.NotEqual(t, t.TempDir(), t.TempDir())

	acquired, err := a.TryAcquire()
	require.NoError(t, err)
	require.True(t, acquired.Held)

	acquired, err = b.TryAcquire()
	require.NoError(t, err)
	require.False(t, acquired.Held, "the runtime directory must not affect ownership")
}

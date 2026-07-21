package redundancy_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"path/filepath"
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
func ownershipObject(t *testing.T) string {
	t.Helper()
	suffix := make([]byte, 8)
	_, err := rand.Read(suffix)
	require.NoError(t, err)
	return "opdl-ownership-test." + hex.EncodeToString(suffix)
}

func openLock(t *testing.T, object string, role redundancy.InstanceRole) *redundancy.Lock {
	t.Helper()
	f, err := redundancy.OpenLock(object, role)
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// TestStatusPathIsInsideTheInstancesOwnDirectory checks the path is the instance's
// runtime directory and nothing else.
//
// It used to qualify the file by project, machine, and role, because one shared
// directory held every instance on the host. The directory is now authored per
// instance, so two instances are separated by the directory they were given
// rather than by a name the runtime composes.
func TestStatusPathIsInsideTheInstancesOwnDirectory(t *testing.T) {
	t.Parallel()

	primaryDir, standbyDir := t.TempDir(), t.TempDir()
	a := redundancy.StatusPath(primaryDir)
	b := redundancy.StatusPath(standbyDir)

	require.Equal(t, filepath.Join(primaryDir, "process.status"), a)
	require.Equal(t, primaryDir, filepath.Dir(a))
	require.NotEqual(t, a, b, "two instances given their own directories write two files")
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
	require.Equal(t, `Global\`+object, f.Name(), "the ownership must live in the machine-wide namespace")
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

// TestOwnershipIdentityIsIndependentOfTheStatusDirectory is the property the file
// lock could not provide. Ownership comes from the descriptor's object name, so
// two processes given different runtime directories still exclude each other.
// Under the previous ownership they would each have taken their own lock file and
// both become active.
func TestOwnershipIdentityIsIndependentOfTheStatusDirectory(t *testing.T) {
	t.Parallel()

	object := ownershipObject(t)
	a := openLock(t, object, redundancy.RolePrimary)
	b := openLock(t, object, redundancy.RoleStandby)

	// Distinct status directories, which is what a misconfigured pair would have.
	require.NotEqual(t,
		redundancy.StatusPath(t.TempDir()),
		redundancy.StatusPath(t.TempDir()))

	acquired, err := a.TryAcquire()
	require.NoError(t, err)
	require.True(t, acquired.Held)

	acquired, err = b.TryAcquire()
	require.NoError(t, err)
	require.False(t, acquired.Held, "the runtime directory must not affect ownership")
}

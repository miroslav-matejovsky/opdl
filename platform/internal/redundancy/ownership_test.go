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

// fenceObject returns an ownership object name no other test or run uses.
//
// The fence lives in a machine-wide kernel namespace, so unlike the lock file it
// replaced it is not isolated by t.TempDir(). Every test generates its own name or
// parallel tests would contend for each other's ownership.
func fenceObject(t *testing.T) string {
	t.Helper()
	suffix := make([]byte, 8)
	_, err := rand.Read(suffix)
	require.NoError(t, err)
	return "opdl-fence-test." + hex.EncodeToString(suffix)
}

func openFence(t *testing.T, object string, role redundancy.InstanceRole) *redundancy.Fence {
	t.Helper()
	f, err := redundancy.OpenFence(object, role)
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func TestStatusPathIsPerRole(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	a := redundancy.StatusPath(dir, "proj", "env", "site", "machine", redundancy.RolePrimary)
	b := redundancy.StatusPath(dir, "proj", "env", "site", "machine", redundancy.RoleStandby)

	// The processes share one machine directory but write separate status files.
	require.Equal(t, filepath.Dir(a), filepath.Dir(b))
	require.NotEqual(t, a, b)
	require.Equal(t, filepath.Join(dir, "proj-env-site-machine", "process-primary.status"), a)
}

func TestOpenFenceRejectsInvalidRole(t *testing.T) {
	t.Parallel()

	_, err := redundancy.OpenFence(fenceObject(t), redundancy.InstanceRole("other"))
	require.Error(t, err)
}

// TestOpenFenceRejectsAnEmptyObject guards the descriptor contract: a machine
// whose descriptor carries no fence object has no fence at all, and must fail to
// start rather than run without one.
func TestOpenFenceRejectsAnEmptyObject(t *testing.T) {
	t.Parallel()

	_, err := redundancy.OpenFence("   ", redundancy.RolePrimary)
	require.ErrorContains(t, err, "no fence object")
}

func TestFenceAcquireReleaseCycle(t *testing.T) {
	t.Parallel()

	object := fenceObject(t)
	f := openFence(t, object, redundancy.RolePrimary)
	require.Equal(t, redundancy.RolePrimary, f.Role())
	require.Equal(t, `Global\`+object, f.Name(), "the fence must live in the machine-wide namespace")
	require.False(t, f.Held())

	acquired, err := f.TryAcquire()
	require.NoError(t, err)
	require.True(t, acquired.Held)
	require.False(t, acquired.Abandoned)
	require.True(t, f.Held())

	// TryAcquire is idempotent for the process that already holds the fence.
	acquired, err = f.TryAcquire()
	require.NoError(t, err)
	require.True(t, acquired.Held)

	require.NoError(t, f.Release())
	require.False(t, f.Held())

	// Release is idempotent.
	require.NoError(t, f.Release())
}

// TestFenceIsExclusive checks two contenders never hold the machine fence
// together: while one holds it, the other's TryAcquire fails and its blocking
// Acquire waits out its context rather than acquiring a second copy.
func TestFenceIsExclusive(t *testing.T) {
	t.Parallel()

	object := fenceObject(t)
	a := openFence(t, object, redundancy.RolePrimary)
	b := openFence(t, object, redundancy.RoleStandby)

	acquired, err := a.TryAcquire()
	require.NoError(t, err)
	require.True(t, acquired.Held)

	// B finds the fence held: TryAcquire is a clean "no", not an error.
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

func TestFenceAcquireStopsOnCanceledContext(t *testing.T) {
	t.Parallel()

	object := fenceObject(t)
	holder := openFence(t, object, redundancy.RolePrimary)
	acquired, err := holder.TryAcquire()
	require.NoError(t, err)
	require.True(t, acquired.Held)
	defer func() { require.NoError(t, holder.Release()) }()

	standby := openFence(t, object, redundancy.RoleStandby)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = standby.Acquire(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, standby.Held())
}

// TestFenceIdentityIsIndependentOfTheStatusDirectory is the property the file
// lock could not provide. Ownership comes from the descriptor's object name, so
// two processes given different runtime directories still exclude each other.
// Under the previous fence they would each have taken their own lock file and
// both become active.
func TestFenceIdentityIsIndependentOfTheStatusDirectory(t *testing.T) {
	t.Parallel()

	object := fenceObject(t)
	a := openFence(t, object, redundancy.RolePrimary)
	b := openFence(t, object, redundancy.RoleStandby)

	// Distinct status directories, which is what a misconfigured pair would have.
	require.NotEqual(t,
		redundancy.StatusPath(t.TempDir(), "p", "e", "s", "m", redundancy.RolePrimary),
		redundancy.StatusPath(t.TempDir(), "p", "e", "s", "m", redundancy.RoleStandby))

	acquired, err := a.TryAcquire()
	require.NoError(t, err)
	require.True(t, acquired.Held)

	acquired, err = b.TryAcquire()
	require.NoError(t, err)
	require.False(t, acquired.Held, "the runtime directory must not affect ownership")
}

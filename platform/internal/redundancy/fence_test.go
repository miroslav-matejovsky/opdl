package redundancy_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/miroslav-matejovsky/opdl/platform/internal/redundancy"
	"github.com/stretchr/testify/require"
)

func TestFencePathIsSlotIndependent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := redundancy.FencePath(dir, "proj", "env", "site", "machine")

	// The identity, not the slot, decides the fence path: both slots contend for
	// one lock, so the path must not depend on which slot is asking.
	require.Equal(t, filepath.Join(dir, "proj-env-site-machine", redundancy.FenceFileName), path)

	other := redundancy.FencePath(dir, "proj", "env", "site", "other")
	require.NotEqual(t, path, other, "a different machine must have a different fence")
}

func TestStatusPathIsPerSlot(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	a := redundancy.StatusPath(dir, "proj", "env", "site", "machine", redundancy.SlotA)
	b := redundancy.StatusPath(dir, "proj", "env", "site", "machine", redundancy.SlotB)

	// The two slots share one machine directory but write separate status files.
	require.Equal(t, filepath.Dir(a), filepath.Dir(b))
	require.NotEqual(t, a, b)
	require.Equal(t, filepath.Join(dir, "proj-env-site-machine", "slot-a.status"), a)
}

func TestOpenFenceRejectsInvalidSlot(t *testing.T) {
	t.Parallel()

	path := redundancy.FencePath(t.TempDir(), "p", "e", "s", "m")
	_, err := redundancy.OpenFence(path, redundancy.Slot("c"))
	require.Error(t, err)
}

func TestFenceAcquireReleaseCycle(t *testing.T) {
	t.Parallel()

	path := redundancy.FencePath(t.TempDir(), "p", "e", "s", "m")
	f, err := redundancy.OpenFence(path, redundancy.SlotA)
	require.NoError(t, err)
	require.Equal(t, redundancy.SlotA, f.Slot())
	require.Equal(t, path, f.Path())
	require.False(t, f.Held())

	acquired, err := f.TryAcquire()
	require.NoError(t, err)
	require.True(t, acquired)
	require.True(t, f.Held())

	// TryAcquire is idempotent for the slot that already holds the fence.
	acquired, err = f.TryAcquire()
	require.NoError(t, err)
	require.True(t, acquired)

	require.NoError(t, f.Release())
	require.False(t, f.Held())

	// Release is idempotent.
	require.NoError(t, f.Release())
}

// TestFenceIsExclusive checks two slots never hold the machine fence together:
// while one holds it, the other's TryAcquire fails and its blocking Acquire waits
// out its context rather than acquiring a second copy.
func TestFenceIsExclusive(t *testing.T) {
	t.Parallel()

	path := redundancy.FencePath(t.TempDir(), "p", "e", "s", "m")

	a, err := redundancy.OpenFence(path, redundancy.SlotA)
	require.NoError(t, err)
	b, err := redundancy.OpenFence(path, redundancy.SlotB)
	require.NoError(t, err)

	acquired, err := a.TryAcquire()
	require.NoError(t, err)
	require.True(t, acquired)

	// B finds the fence held: TryAcquire is a clean "no", not an error.
	acquired, err = b.TryAcquire()
	require.NoError(t, err)
	require.False(t, acquired)
	require.False(t, b.Held())

	// A bounded blocking wait ends in its deadline, not in a second holder.
	waitCtx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, b.Acquire(waitCtx), context.DeadlineExceeded)
	require.False(t, b.Held())

	// Once A releases, B can become active.
	require.NoError(t, a.Release())
	require.NoError(t, b.Acquire(t.Context()))
	require.True(t, b.Held())
	require.NoError(t, b.Release())
}

func TestFenceAcquireStopsOnCanceledContext(t *testing.T) {
	t.Parallel()

	path := redundancy.FencePath(t.TempDir(), "p", "e", "s", "m")

	holder, err := redundancy.OpenFence(path, redundancy.SlotA)
	require.NoError(t, err)
	acquired, err := holder.TryAcquire()
	require.NoError(t, err)
	require.True(t, acquired)
	defer func() { require.NoError(t, holder.Release()) }()

	standby, err := redundancy.OpenFence(path, redundancy.SlotB)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, standby.Acquire(ctx), context.Canceled)
	require.False(t, standby.Held())
}

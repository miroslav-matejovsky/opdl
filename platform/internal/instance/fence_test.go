package instance_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/miroslav-matejovsky/opdl/platform/internal/instance"
	"github.com/stretchr/testify/require"
)

func TestFencePathIsSlotIndependent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := instance.FencePath(dir, "proj", "env", "site", "machine")

	// The identity, not the slot, decides the path: both slots contend for one
	// lock, so the path must not depend on which slot is asking.
	require.Equal(t, filepath.Join(dir, "proj-env-site-machine", instance.FenceFileName), path)

	other := instance.FencePath(dir, "proj", "env", "site", "other")
	require.NotEqual(t, path, other, "a different machine must have a different fence")
}

func TestOpenFenceRejectsInvalidSlot(t *testing.T) {
	t.Parallel()

	path := instance.FencePath(t.TempDir(), "p", "e", "s", "m")
	_, err := instance.OpenFence(path, instance.Slot("c"))
	require.Error(t, err)
}

func TestFenceAcquireReleaseCycle(t *testing.T) {
	t.Parallel()

	path := instance.FencePath(t.TempDir(), "p", "e", "s", "m")
	f, err := instance.OpenFence(path, instance.SlotA)
	require.NoError(t, err)
	require.Equal(t, instance.SlotA, f.Slot())
	require.Equal(t, path, f.Path())
	require.False(t, f.Held())

	require.NoError(t, f.Acquire(t.Context()))
	require.True(t, f.Held())

	// Acquire is idempotent for the slot that already holds the fence.
	require.NoError(t, f.Acquire(t.Context()))
	require.True(t, f.Held())

	require.NoError(t, f.Release())
	require.False(t, f.Held())

	// Release is idempotent.
	require.NoError(t, f.Release())
}

func TestFenceIsExclusive(t *testing.T) {
	t.Parallel()

	path := instance.FencePath(t.TempDir(), "p", "e", "s", "m")

	a, err := instance.OpenFence(path, instance.SlotA)
	require.NoError(t, err)
	b, err := instance.OpenFence(path, instance.SlotB)
	require.NoError(t, err)

	require.NoError(t, a.Acquire(t.Context()))
	require.True(t, a.Held())

	// While A holds the fence, B waits and never acquires: a bounded wait ends in
	// its context's deadline, not in a second holder.
	waitCtx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	err = b.Acquire(waitCtx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.False(t, b.Held())

	// Once A releases, B can become active.
	require.NoError(t, a.Release())
	require.NoError(t, b.Acquire(t.Context()))
	require.True(t, b.Held())
	require.NoError(t, b.Release())
}

func TestFenceAcquireStopsOnCanceledContext(t *testing.T) {
	t.Parallel()

	path := instance.FencePath(t.TempDir(), "p", "e", "s", "m")

	// A standby holds nothing; canceling its wait ends it without acquiring.
	holder, err := instance.OpenFence(path, instance.SlotA)
	require.NoError(t, err)
	require.NoError(t, holder.Acquire(t.Context()))
	defer func() { require.NoError(t, holder.Release()) }()

	standby, err := instance.OpenFence(path, instance.SlotB)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, standby.Acquire(ctx), context.Canceled)
	require.False(t, standby.Held())
}

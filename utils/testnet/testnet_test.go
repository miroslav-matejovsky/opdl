package testnet_test

import (
	"context"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/miroslav-matejovsky/opdl/utils/testnet"
	"github.com/stretchr/testify/require"
)

func TestReserve_RejectNonPositiveCounts(t *testing.T) {
	tests := []struct {
		name  string
		count int
	}{
		{name: "zero", count: 0},
		{name: "negative", count: -1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := testnet.Reserve(t.Context(), tc.count)
			require.Error(t, err)
			require.Nil(t, res)
		})
	}
}

func TestReserve_ReturnDistinctAddresses(t *testing.T) {
	count := 5
	res, err := testnet.Reserve(t.Context(), count)
	require.NoError(t, err)
	require.NotNil(t, res)
	t.Cleanup(func() { _ = res.Release() })

	addrs := res.Addresses()
	require.Len(t, addrs, count)

	seen := make(map[string]struct{}, count)
	for _, addr := range addrs {
		require.True(t, strings.HasPrefix(addr, "127.0.0.1:"), "expected IPv4 loopback address, got %s", addr)
		seen[addr] = struct{}{}
	}
	require.Len(t, seen, count, "addresses must all be distinct")
}

func TestReserve_KeepListenersUnavailableUntilRelease(t *testing.T) {
	res, err := testnet.Reserve(t.Context(), 2)
	require.NoError(t, err)
	t.Cleanup(func() { _ = res.Release() })

	for _, addr := range res.Addresses() {
		var lc net.ListenConfig
		l, err := lc.Listen(t.Context(), "tcp", addr)
		require.Error(t, err, "expected bind error while reservation is holding listener for %s", addr)
		if l != nil {
			_ = l.Close()
		}
	}
}

func TestReserve_VerifyCallersCanBindAfterRelease(t *testing.T) {
	res, err := testnet.Reserve(t.Context(), 3)
	require.NoError(t, err)
	require.NoError(t, res.Release())

	for _, addr := range res.Addresses() {
		var lc net.ListenConfig
		l, err := lc.Listen(t.Context(), "tcp", addr)
		require.NoError(t, err, "expected to successfully bind %s after release", addr)
		require.NoError(t, l.Close())
	}
}

func TestReserve_ReleaseAfterContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	res, err := testnet.Reserve(ctx, 3)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, res)
}

type countCtx struct {
	context.Context
	calls     *int32
	failAfter int32
}

func (c countCtx) Err() error {
	if atomic.AddInt32(c.calls, 1) > c.failAfter {
		return context.Canceled
	}
	return c.Context.Err()
}

func TestReserve_ReleaseAfterPartialAllocationFailure(t *testing.T) {
	var calls int32
	ctx := countCtx{
		Context:   t.Context(),
		calls:     &calls,
		failAfter: 2, // allows first iteration and first Listen, cancels before second iteration
	}

	res, err := testnet.Reserve(ctx, 3)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, res)
}

func TestReservation_RepeatedReleaseSafe(t *testing.T) {
	res, err := testnet.Reserve(t.Context(), 2)
	require.NoError(t, err)

	require.NoError(t, res.Release())
	require.NoError(t, res.Release())
	require.NoError(t, res.Release())

	addrs := res.Addresses()
	require.Len(t, addrs, 2, "Addresses() should continue returning the reserved addresses after release")
}

func TestReservation_ConcurrentRelease(t *testing.T) {
	res, err := testnet.Reserve(t.Context(), 4)
	require.NoError(t, err)

	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = res.Release()
			_ = res.Addresses()
		}()
	}
	wg.Wait()
}

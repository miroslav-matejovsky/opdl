package testnet

import (
	"math/rand/v2"
	"net"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTake_RejectInvalidInputs(t *testing.T) {
	tests := []struct {
		name  string
		host  string
		count int
	}{
		{name: "zero count", host: "127.0.0.1", count: 0},
		{name: "negative count", host: "127.0.0.1", count: -1},
		{name: "empty host", host: "", count: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ports, err := Take(tc.host, tc.count)
			require.Error(t, err)
			require.Nil(t, ports)
		})
	}
}

func TestTake_RequestExceedsBandSize(t *testing.T) {
	ports, err := Take("127.0.0.1", bandSize+1)
	require.Error(t, err)
	require.Nil(t, ports)
	require.Contains(t, err.Error(), "requested count exceeds band size")
}

func TestTake_DistinctPortsAcrossConcurrentCalls(t *testing.T) {
	const goroutines = 20
	const portsPerCall = 3

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		all  = make([]int, 0, goroutines*portsPerCall)
		errs []error
	)

	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			ports, err := Take("127.0.0.1", portsPerCall)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			all = append(all, ports...)
		}()
	}
	wg.Wait()

	require.Empty(t, errs)
	require.Len(t, all, goroutines*portsPerCall)

	seen := make(map[int]struct{}, len(all))
	for _, p := range all {
		require.GreaterOrEqual(t, p, bandStart)
		require.LessOrEqual(t, p, bandEnd)
		seen[p] = struct{}{}
	}
	require.Len(t, seen, len(all), "all returned ports must be distinct")
}

func TestTake_ReturnedPortsAreInsideBandAndBindable(t *testing.T) {
	ports, err := Take("127.0.0.1", 5)
	require.NoError(t, err)
	require.Len(t, ports, 5)

	for _, p := range ports {
		require.GreaterOrEqual(t, p, bandStart)
		require.LessOrEqual(t, p, bandEnd)

		addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(p))
		var lc net.ListenConfig
		l, err := lc.Listen(t.Context(), "tcp", addr)
		require.NoError(t, err, "expected to successfully bind returned port %d", p)
		require.NoError(t, l.Close())
	}
}

func TestTake_SkipForeignListener(t *testing.T) {
	poolMu.Lock()
	if !poolInit {
		poolOffset = rand.IntN(bandSize)
		poolInit = true
	}
	nextOffset := (poolOffset + poolCursor) % bandSize
	heldPort := bandStart + nextOffset
	poolMu.Unlock()

	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(heldPort))
	var lc net.ListenConfig
	l, err := lc.Listen(t.Context(), "tcp", addr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })

	ports, err := Take("127.0.0.1", 1)
	require.NoError(t, err)
	require.Len(t, ports, 1)
	require.NotEqual(t, heldPort, ports[0], "Take must skip port %d which is held by a listener", heldPort)
}

func TestTake_Exhaustion(t *testing.T) {
	poolMu.Lock()
	oldInit := poolInit
	oldCursor := poolCursor
	oldOffset := poolOffset
	poolInit = true
	poolCursor = bandSize - 1
	poolOffset = 0
	poolMu.Unlock()

	t.Cleanup(func() {
		poolMu.Lock()
		poolInit = oldInit
		poolCursor = oldCursor
		poolOffset = oldOffset
		poolMu.Unlock()
	})

	ports, err := Take("127.0.0.1", 2)
	require.Error(t, err)
	require.Nil(t, ports)
	require.Contains(t, err.Error(), "band exhausted")
}

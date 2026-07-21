package testnet

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"strconv"
	"sync"
)

const (
	// bandStart and bandEnd define the process-wide pool of port numbers.
	//
	// Why this band:
	// Port 0 draws from the operating system ephemeral range. Windows uses 49152
	// to 65535, Linux uses 32768 to 60999. That range is shared with outbound
	// connections (NATS clients, HTTP clients, dotnet test). Between release and
	// bind in a reserve-then-release lifecycle, an ephemeral port can be taken by
	// an unrelated outbound socket.
	//
	// Choosing ports in 20000 to 32767 stays below the Linux ephemeral start of
	// 32768 and well below the Windows start of 49152, so the operating system
	// never assigns these on its own. 12768 ports provides ample headroom against
	// test suites that need roughly 40 ports per run.
	bandStart = 20000
	bandEnd   = 32767
	bandSize  = bandEnd - bandStart + 1
)

var (
	poolMu     sync.Mutex
	poolInit   bool
	poolCursor int // number of candidate ports checked (0 to bandSize) across all Take calls
	poolOffset int // random starting offset inside [0, bandSize)
)

// Take returns count ports on host that no other Take in this process has
// returned, and that nothing on the host is currently listening on.
//
// Take uses a monotonic cursor over the 20000..32767 band, starting at a random
// offset. Once a candidate port is checked, it is never checked or returned again
// inside this process, preventing concurrent tests from receiving duplicate ports.
// Before returning a port, Take probes it by briefly listening on host:port and
// immediately closing the listener to ensure a foreign process does not hold it.
func Take(host string, count int) ([]int, error) {
	if count <= 0 {
		return nil, errors.New("count must be positive")
	}
	if host == "" {
		return nil, errors.New("host must not be empty")
	}
	if count > bandSize {
		return nil, fmt.Errorf("take %d ports on %s: requested count exceeds band size %d", count, host, bandSize)
	}

	poolMu.Lock()
	defer poolMu.Unlock()

	if !poolInit {
		poolOffset = rand.IntN(bandSize)
		poolInit = true
	}

	var (
		ports        = make([]int, 0, count)
		probesFailed int
	)

	for len(ports) < count {
		if poolCursor >= bandSize {
			return nil, fmt.Errorf("take %d ports on %s: band exhausted (%d probes failed)", count, host, probesFailed)
		}

		offset := (poolOffset + poolCursor) % bandSize
		port := bandStart + offset
		poolCursor++

		var lc net.ListenConfig
		l, err := lc.Listen(context.Background(), "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		if err != nil {
			probesFailed++
			continue
		}
		_ = l.Close()
		ports = append(ports, port)
	}

	return ports, nil
}

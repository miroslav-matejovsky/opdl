package semaphore

import (
	"context"
	"sync"
)

type waiter struct {
	n     int
	ready chan struct{}
}

// Semaphore is a bounded weighted semaphore that grants capacity in FIFO order.
type Semaphore struct {
	mu      sync.Mutex
	max     int
	cur     int
	waiters []*waiter
}

// New creates a new weighted semaphore with the specified maximum weight capacity.
func New(capacity int) *Semaphore {
	if capacity <= 0 {
		capacity = 1
	}
	return &Semaphore{max: capacity}
}

// Acquire acquires n units of weight, blocking until resources are available or ctx is canceled.
//
// A request larger than the capacity is clamped to the capacity rather than
// blocking forever. A caller whose weight is derived from its workload (a
// four-machine scenario on a two-unit budget) would otherwise deadlock against a
// limit it cannot satisfy. Release clamps identically, so the two stay balanced.
func (s *Semaphore) Acquire(ctx context.Context, n int) error {
	if n <= 0 {
		return nil
	}
	n = min(n, s.max)

	s.mu.Lock()
	if len(s.waiters) == 0 && s.cur+n <= s.max {
		s.cur += n
		s.mu.Unlock()
		return nil
	}

	w := &waiter{n: n, ready: make(chan struct{})}
	s.waiters = append(s.waiters, w)
	s.mu.Unlock()

	select {
	case <-w.ready:
		return nil
	case <-ctx.Done():
		s.mu.Lock()
		select {
		case <-w.ready:
			s.mu.Unlock()
			return nil
		default:
			for i, cand := range s.waiters {
				if cand == w {
					s.waiters = append(s.waiters[:i], s.waiters[i+1:]...)
					break
				}
			}
			s.notify()
			s.mu.Unlock()
			return ctx.Err()
		}
	}
}

// Release releases n units of weight, notifying waiting acquisitions in FIFO order.
//
// n is clamped to the capacity to match Acquire. Passing back the same n that
// was acquired therefore always returns exactly what was taken.
func (s *Semaphore) Release(n int) {
	if n <= 0 {
		return
	}
	n = min(n, s.max)

	s.mu.Lock()
	s.cur -= n
	if s.cur < 0 {
		s.cur = 0
	}
	s.notify()
	s.mu.Unlock()
}

func (s *Semaphore) notify() {
	for len(s.waiters) > 0 {
		w := s.waiters[0]
		if s.cur+w.n > s.max {
			break
		}
		s.cur += w.n
		s.waiters = s.waiters[1:]
		close(w.ready)
	}
}

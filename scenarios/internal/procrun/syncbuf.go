package procrun

import (
	"bytes"
	"sync"
)

// syncBuf is a process output buffer that is safe to read while the process
// is still writing to it.
//
// exec copies a child's stdout and stderr on goroutines of its own, so every
// diagnostic that reads a running process's output races that copier. A plain
// bytes.Buffer makes that a genuine data race, which under -race fails the
// caller for a reason that has nothing to do with the platform.
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

package winmutex

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

// namespacePrefix is the only kernel namespace this package creates objects in.
// See the package documentation for why there is no Local\ fallback.
const namespacePrefix = `Global\`

// maxObjectName bounds a kernel object name, including the namespace prefix.
const maxObjectName = 260

// Outcome reports how an acquisition attempt ended.
type Outcome int

const (
	// NotAcquired means another process holds the mutex, or the wait was canceled.
	NotAcquired Outcome = iota
	// Acquired means this process now owns the mutex and the previous owner, if
	// any, released it cleanly.
	Acquired
	// AcquiredAbandoned means this process now owns the mutex and the previous
	// owner died without releasing it. Ownership is valid; the distinction exists
	// so a caller can tell a crash from a planned handover.
	//
	// It is only reported to a process that already had the object open when the
	// owner died. A named object exists only while a handle to it is open, so a
	// process that opens the name after every previous holder is gone creates a
	// new object and acquires it cleanly. That is the intended boundary: with no
	// peer left running there is nothing the signal would warn about.
	AcquiredAbandoned
)

// Held reports whether the outcome granted ownership. Both Acquired and
// AcquiredAbandoned do.
func (o Outcome) Held() bool { return o == Acquired || o == AcquiredAbandoned }

// Mutex is one exclusive, machine-wide ownership object.
//
// A Mutex is safe for concurrent use. Its operations are serialized onto one
// dedicated OS thread, which is what keeps Windows thread-affine ownership valid
// for the whole process rather than for whichever goroutine happened to call.
type Mutex struct {
	object  string
	existed bool

	// mu serializes the exported API. The cancellation path deliberately does not
	// take it: it signals an event the owner thread is already waiting on.
	mu     sync.Mutex
	closed bool

	// held is atomic rather than guarded by mu because Acquire holds mu for the
	// whole of a blocking kernel wait. A caller that polls Held while another
	// goroutine waits for the lock, which is exactly what a standby does, would
	// otherwise block until the wait it is asking about had already finished.
	held atomic.Bool

	requests chan request
	stopped  chan struct{}

	// handle and cancel are created by the owner thread before Open returns and
	// closed by it after the request channel is drained. Only SetEvent on cancel
	// is called from another goroutine, which is safe for the handle's lifetime.
	handle windows.Handle
	cancel windows.Handle
}

type opKind int

const (
	opTryAcquire opKind = iota
	opAcquire
	opRelease
)

type request struct {
	kind opKind
	resp chan result
}

type result struct {
	outcome Outcome
	err     error
}

// Open prepares a contender for the machine-wide mutex called name.
//
// name is a bare object name; Open places it in the Global\ namespace itself, so
// it must not contain a backslash. Open creates the object if it does not exist,
// with an explicit DACL, and opens it otherwise. It does not acquire the mutex:
// call TryAcquire or Acquire to contend for it.
//
// Open fails rather than proceeding when the Global\ namespace is unavailable, or
// when an object of that name exists but this process may not open it.
func Open(name string) (*Mutex, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}
	m := &Mutex{
		object:   namespacePrefix + name,
		requests: make(chan request),
		stopped:  make(chan struct{}),
	}
	started := make(chan error, 1)
	go m.serve(started)
	if err := <-started; err != nil {
		<-m.stopped
		return nil, err
	}
	return m, nil
}

func validateName(name string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return errors.New("winmutex: object name is required")
	case strings.Contains(name, `\`):
		return fmt.Errorf("winmutex: object name %q must not contain a backslash; the Global namespace is applied by this package", name)
	case len(namespacePrefix)+len(name) > maxObjectName:
		return fmt.Errorf("winmutex: object name %q is longer than the %d character limit", name, maxObjectName-len(namespacePrefix))
	}
	return nil
}

// serve runs the owner thread. Every mutex operation happens here, on one OS
// thread that is pinned for the life of the Mutex, because Windows ties mutex
// ownership to a thread rather than to a process.
func (m *Mutex) serve(started chan<- error) {
	// Deliberately never unlocked. When this goroutine returns, Go terminates the
	// thread, which is correct only because Close guarantees the mutex is no longer
	// held by then.
	runtime.LockOSThread()
	defer close(m.stopped)

	handle, existed, err := openObject(m.object)
	if err != nil {
		started <- err
		return
	}
	cancelEvent, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		_ = windows.CloseHandle(handle)
		started <- fmt.Errorf("winmutex: create cancellation event for %s: %w", m.object, err)
		return
	}
	m.handle, m.cancel, m.existed = handle, cancelEvent, existed
	started <- nil

	for req := range m.requests {
		switch req.kind {
		case opTryAcquire:
			req.resp <- m.wait(0)
		case opAcquire:
			req.resp <- m.waitCancellable()
		case opRelease:
			req.resp <- result{err: m.release()}
		}
	}
	_ = windows.CloseHandle(m.cancel)
	_ = windows.CloseHandle(m.handle)
}

// openObject creates or opens the named mutex and reports whether it already
// existed.
//
// CreateMutex is create-or-open in one atomic call, which is what makes a racing
// peer harmless: both processes end up with a handle to the same object. It
// reports ERROR_ALREADY_EXISTS while still returning a valid handle, so the same
// call also says which of the two happened. There is deliberately no separate
// existence probe: one would be both redundant and racy.
//
// A name held by a process whose security descriptor excludes this one fails here
// with an access error rather than being retried or worked around.
func openObject(object string) (windows.Handle, bool, error) {
	name, err := windows.UTF16PtrFromString(object)
	if err != nil {
		return 0, false, fmt.Errorf("winmutex: encode object name %s: %w", object, err)
	}
	attributes, err := securityAttributes()
	if err != nil {
		return 0, false, err
	}
	handle, err := windows.CreateMutex(attributes, false, name)
	if handle == 0 {
		return 0, false, fmt.Errorf("winmutex: create %s (the Global namespace needs SeCreateGlobalPrivilege): %w", object, err)
	}
	return handle, errors.Is(err, windows.ERROR_ALREADY_EXISTS), nil
}

// securityAttributes builds the DACL a created object is given: full control for
// LocalSystem and Administrators so the object is operable, and for the account
// this process runs as so the machine's other platform process can open it. Both
// processes of a machine are launched by the same service manager under the same
// identity, which is what makes one entry sufficient.
//
// The DACL is protected so it inherits nothing.
func securityAttributes() (*windows.SecurityAttributes, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("winmutex: read process token user: %w", err)
	}
	sddl := fmt.Sprintf("D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;%s)", user.User.Sid.String())
	descriptor, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return nil, fmt.Errorf("winmutex: build security descriptor %q: %w", sddl, err)
	}
	attributes := &windows.SecurityAttributes{SecurityDescriptor: descriptor}
	attributes.Length = uint32(unsafe.Sizeof(*attributes))
	return attributes, nil
}

// wait blocks the owner thread on the mutex for at most milliseconds.
func (m *Mutex) wait(milliseconds uint32) result {
	event, err := windows.WaitForSingleObject(m.handle, milliseconds)
	return m.interpret(event, err)
}

// waitCancellable blocks the owner thread until the mutex is acquired or the
// cancellation event is signalled. The event is reset first so a cancellation
// that arrived after the previous wait returned cannot end this one.
func (m *Mutex) waitCancellable() result {
	if err := windows.ResetEvent(m.cancel); err != nil {
		return result{err: fmt.Errorf("winmutex: reset cancellation event for %s: %w", m.object, err)}
	}
	event, err := windows.WaitForMultipleObjects([]windows.Handle{m.handle, m.cancel}, false, windows.INFINITE)
	return m.interpret(event, err)
}

// interpret maps a Windows wait result onto an Outcome.
//
// WAIT_ABANDONED is not a failure: ownership is granted, and the value reports
// that the previous owner died without releasing.
func (m *Mutex) interpret(event uint32, err error) result {
	switch event {
	case windows.WAIT_OBJECT_0:
		return result{outcome: Acquired}
	case windows.WAIT_ABANDONED:
		return result{outcome: AcquiredAbandoned}
	case windows.WAIT_OBJECT_0 + 1:
		// The cancellation event, which only waitCancellable passes.
		return result{outcome: NotAcquired}
	case uint32(windows.WAIT_TIMEOUT):
		return result{outcome: NotAcquired}
	case windows.WAIT_FAILED:
		return result{err: fmt.Errorf("winmutex: wait on %s: %w", m.object, err)}
	default:
		return result{err: fmt.Errorf("winmutex: wait on %s returned unexpected result %d", m.object, event)}
	}
}

func (m *Mutex) release() error {
	if err := windows.ReleaseMutex(m.handle); err != nil {
		return fmt.Errorf("winmutex: release %s: %w", m.object, err)
	}
	return nil
}

// call sends one request to the owner thread and waits for its answer.
func (m *Mutex) call(kind opKind) result {
	resp := make(chan result, 1)
	select {
	case m.requests <- request{kind: kind, resp: resp}:
		return <-resp
	case <-m.stopped:
		return result{err: fmt.Errorf("winmutex: %s is closed", m.object)}
	}
}

// TryAcquire takes ownership without blocking. It reports the outcome, and an
// error only for an unexpected failure, never for a mutex another process
// legitimately holds.
func (m *Mutex) TryAcquire() (Outcome, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return NotAcquired, fmt.Errorf("winmutex: %s is closed", m.object)
	}
	if m.held.Load() {
		return Acquired, nil
	}
	res := m.call(opTryAcquire)
	if res.err != nil {
		return NotAcquired, res.err
	}
	m.held.Store(res.outcome.Held())
	return res.outcome, nil
}

// Acquire blocks until this process owns the mutex or ctx is canceled.
//
// The wait happens in the kernel: a waiter is woken when the holder releases or
// dies, with no polling interval between the two. Cancellation returns without
// acquiring and without promotion, and never releases ownership already held.
func (m *Mutex) Acquire(ctx context.Context) (Outcome, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return NotAcquired, fmt.Errorf("winmutex: %s is closed", m.object)
	}
	if m.held.Load() {
		return Acquired, nil
	}
	if err := ctx.Err(); err != nil {
		return NotAcquired, err
	}

	// Signal the owner thread's cancellation event when ctx ends. The watcher stops
	// as soon as the wait returns, so a later cancellation cannot leak into the
	// next Acquire; waitCancellable also resets the event before each wait.
	watching := make(chan struct{})
	defer close(watching)
	go func() {
		select {
		case <-ctx.Done():
			_ = windows.SetEvent(m.cancel)
		case <-watching:
		}
	}()

	res := m.call(opAcquire)
	if res.err != nil {
		return NotAcquired, res.err
	}
	if !res.outcome.Held() {
		if err := ctx.Err(); err != nil {
			return NotAcquired, err
		}
	}
	m.held.Store(res.outcome.Held())
	return res.outcome, nil
}

// Release drops ownership so another process may take it. Release is idempotent
// and must be called only after whatever the mutex protects has been shut down:
// releasing early lets a waiter start while this process still holds it.
func (m *Mutex) Release() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.held.Load() {
		return nil
	}
	res := m.call(opRelease)
	if res.err != nil {
		return res.err
	}
	m.held.Store(false)
	return nil
}

// Close releases the object's handles and stops the owner thread. It is
// idempotent. Close releases ownership first if it is still held, so a caller
// that closes without releasing does not leave the mutex abandoned.
func (m *Mutex) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	var releaseErr error
	if m.held.Load() {
		if res := m.call(opRelease); res.err != nil {
			releaseErr = res.err
		} else {
			m.held.Store(false)
		}
	}
	m.closed = true
	close(m.requests)
	<-m.stopped
	return releaseErr
}

// Held reports whether this process currently owns the mutex.
func (m *Mutex) Held() bool { return m.held.Load() }

// Name returns the fully qualified object name, including its namespace.
func (m *Mutex) Name() string { return m.object }

// Existed reports whether the object already existed when this process opened
// it. On a machine running a primary and a standby, the second process to start
// legitimately sees true. It is a diagnostic, not an error condition, and it is
// best effort: a peer creating the object concurrently may not be observed.
func (m *Mutex) Existed() bool { return m.existed }

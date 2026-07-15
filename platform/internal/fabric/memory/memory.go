package memory

import (
	"context"
	"maps"
	"slices"
	"sync"

	"github.com/miroslav-matejovsky/opdl/platform/deployment"
	"github.com/miroslav-matejovsky/opdl/platform/internal/fabric"
)

// Name is this adapter's implementation name, reported as operational metadata.
const Name = "memory"

// Fabric is an in-process fabric. Its expected membership still comes from the
// deployment descriptor, so a test sees the same site the production adapter
// would; only self is ever reachable.
type Fabric struct {
	members []fabric.Member

	mu          sync.RWMutex
	closed      bool
	collections map[string]*collection
}

// Open builds an in-process fabric for a machine's resolved deployment
// descriptor.
func Open(descriptor deployment.Descriptor) *Fabric {
	return &Fabric{
		members:     fabric.MembersFromDescriptor(descriptor),
		collections: map[string]*collection{},
	}
}

// Name returns this adapter's implementation name.
func (f *Fabric) Name() string { return Name }

// Collection opens the named collection, creating it on first use.
func (f *Fabric) Collection(name string) (fabric.Collection, error) {
	if err := fabric.ValidateName(name); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil, fabric.ErrClosed
	}
	existing, ok := f.collections[name]
	if !ok {
		existing = &collection{name: name, entries: map[string][]byte{}, fabric: f}
		f.collections[name] = existing
	}
	return existing, nil
}

// Members returns the expected site membership from the descriptor.
func (f *Fabric) Members() []fabric.Member {
	return slices.Clone(f.members)
}

// State reports what this adapter can honestly see: itself, and no peer. A site
// of one is therefore connected, and any larger site is disconnected. It would
// be easy to claim connected here and awkward to explain later; a site this
// adapter cannot carry should say so.
func (f *Fabric) State(ctx context.Context) (fabric.State, error) {
	if err := f.check(ctx); err != nil {
		return "", err
	}
	return fabric.StateFor(len(f.members), 1), nil
}

// Close releases the fabric. It is idempotent.
func (f *Fabric) Close(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	f.collections = map[string]*collection{}
	return nil
}

// check reports whether a call may proceed: a live fabric and a live context.
func (f *Fabric) check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.closed {
		return fabric.ErrClosed
	}
	return nil
}

// collection is one named map. Its own mutex makes each key operation atomic.
type collection struct {
	name   string
	fabric *Fabric

	mu      sync.RWMutex
	entries map[string][]byte
}

// Create stores value under key only if key is absent.
func (c *collection) Create(ctx context.Context, key string, value []byte) (bool, error) {
	if err := c.check(ctx, key); err != nil {
		return false, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[key]; exists {
		return false, nil
	}
	c.entries[key] = slices.Clone(value)
	return true, nil
}

// Swap stores value under key and returns the value it replaced.
func (c *collection) Swap(ctx context.Context, key string, value []byte) (previous []byte, existed bool, err error) {
	if err := c.check(ctx, key); err != nil {
		return nil, false, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	previous, existed = c.entries[key]
	c.entries[key] = slices.Clone(value)
	// The replaced slice is cloned rather than handed over. It is unreachable
	// from the map now, so passing it would work, but the rule that stored bytes
	// never leave this package is worth more than one avoided copy.
	return slices.Clone(previous), existed, nil
}

// Get returns the value stored under key.
func (c *collection) Get(ctx context.Context, key string) (value []byte, found bool, err error) {
	if err := c.check(ctx, key); err != nil {
		return nil, false, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	stored, found := c.entries[key]
	if !found {
		return nil, false, nil
	}
	return slices.Clone(stored), true, nil
}

// Entries returns the collection's entries ordered by key.
func (c *collection) Entries(ctx context.Context) ([]fabric.Entry, error) {
	if err := c.fabric.check(ctx); err != nil {
		return nil, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	entries := make([]fabric.Entry, 0, len(c.entries))
	for _, key := range slices.Sorted(maps.Keys(c.entries)) {
		entries = append(entries, fabric.Entry{Key: key, Value: slices.Clone(c.entries[key])})
	}
	return entries, nil
}

// check validates the key and reports whether the call may proceed.
func (c *collection) check(ctx context.Context, key string) error {
	if err := fabric.ValidateKey(c.name, key); err != nil {
		return err
	}
	return c.fabric.check(ctx)
}

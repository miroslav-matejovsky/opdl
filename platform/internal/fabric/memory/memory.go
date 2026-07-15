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

// Site is the shared state of one in-process site: the collections its members
// carry between them.
//
// It exists so a test can run several machines of one site in one process and
// have them actually share state, which is what the fabric is for. Each machine
// still opens its own Fabric, with its own descriptor, its own identity, and its
// own lifecycle; the Site is only what they share.
//
// It is not a production construct. Real members are in different processes on
// different machines, and what they share is a backend.
type Site struct {
	mu          sync.Mutex
	collections map[string]*collection
}

// NewSite creates an empty in-process site.
func NewSite() *Site {
	return &Site{collections: map[string]*collection{}}
}

// Open builds one member's fabric on the site, from that machine's resolved
// deployment descriptor.
func (s *Site) Open(descriptor deployment.Descriptor) *Fabric {
	return &Fabric{members: fabric.MembersFromDescriptor(descriptor), site: s}
}

// collection returns the site's collection of that name, creating it on first
// use. The members of a site share one collection per name, so what one member
// writes is what another reads.
func (s *Site) collection(name string) *collection {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.collections[name]
	if !ok {
		existing = &collection{entries: map[string][]byte{}}
		s.collections[name] = existing
	}
	return existing
}

// Fabric is one member's in-process fabric. Its expected membership comes from
// the deployment descriptor, so a test sees the same site the production adapter
// would.
type Fabric struct {
	members []fabric.Member
	site    *Site

	mu     sync.RWMutex
	closed bool
}

// Open builds a standalone in-process fabric for a machine's resolved deployment
// descriptor. It is a member of a site of its own, which is what a test wants
// unless it is specifically testing members sharing state; for that, see Site.
func Open(descriptor deployment.Descriptor) *Fabric {
	return NewSite().Open(descriptor)
}

// Name returns this adapter's implementation name.
func (f *Fabric) Name() string { return Name }

// Collection opens the named collection, creating it on first use.
func (f *Fabric) Collection(name string) (fabric.Collection, error) {
	if err := fabric.ValidateName(name); err != nil {
		return nil, err
	}
	if err := f.check(context.Background()); err != nil {
		return nil, err
	}
	return &handle{name: name, collection: f.site.collection(name), fabric: f}, nil
}

// Members returns the expected site membership from the descriptor.
func (f *Fabric) Members() []fabric.Member {
	return slices.Clone(f.members)
}

// State reports what this adapter can honestly see: itself, and no peer. A site
// of one is therefore connected, and any larger site is disconnected, even when
// a Site is sharing state between its members. That is not an oversight: the
// members of an in-process site never opened a connection to each other, and
// claiming they did would make the one honest thing about this adapter's state a
// lie.
//
// Nothing in registration reads State, because acceptance is decided from the
// expected membership rather than from who is reachable.
func (f *Fabric) State(ctx context.Context) (fabric.State, error) {
	if err := f.check(ctx); err != nil {
		return "", err
	}
	return fabric.StateFor(len(f.members), 1), nil
}

// Close releases this member's fabric. It is idempotent. It does not discard the
// site's collections: this member is done with them, but another member may
// still be running, exactly as closing a real member does not empty the site.
func (f *Fabric) Close(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
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

// collection is one named map shared by the members of a site. Its own mutex
// makes each key operation atomic.
type collection struct {
	mu      sync.RWMutex
	entries map[string][]byte
}

// handle is one member's view of a shared collection. The data is the site's;
// the lifecycle is the member's, so a closed member's handle stops working while
// the collection carries on for everyone else.
type handle struct {
	name       string
	collection *collection
	fabric     *Fabric
}

// Create stores value under key only if key is absent.
func (h *handle) Create(ctx context.Context, key string, value []byte) (bool, error) {
	if err := h.check(ctx, key); err != nil {
		return false, err
	}
	c := h.collection
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[key]; exists {
		return false, nil
	}
	c.entries[key] = slices.Clone(value)
	return true, nil
}

// Swap stores value under key and returns the value it replaced.
func (h *handle) Swap(ctx context.Context, key string, value []byte) (previous []byte, existed bool, err error) {
	if err := h.check(ctx, key); err != nil {
		return nil, false, err
	}
	c := h.collection
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
func (h *handle) Get(ctx context.Context, key string) (value []byte, found bool, err error) {
	if err := h.check(ctx, key); err != nil {
		return nil, false, err
	}
	c := h.collection
	c.mu.RLock()
	defer c.mu.RUnlock()
	stored, found := c.entries[key]
	if !found {
		return nil, false, nil
	}
	return slices.Clone(stored), true, nil
}

// Entries returns the collection's entries ordered by key.
func (h *handle) Entries(ctx context.Context) ([]fabric.Entry, error) {
	if err := h.fabric.check(ctx); err != nil {
		return nil, err
	}
	c := h.collection
	c.mu.RLock()
	defer c.mu.RUnlock()
	entries := make([]fabric.Entry, 0, len(c.entries))
	for _, key := range slices.Sorted(maps.Keys(c.entries)) {
		entries = append(entries, fabric.Entry{Key: key, Value: slices.Clone(c.entries[key])})
	}
	return entries, nil
}

// check validates the key and reports whether the call may proceed.
func (h *handle) check(ctx context.Context, key string) error {
	if err := fabric.ValidateKey(h.name, key); err != nil {
		return err
	}
	return h.fabric.check(ctx)
}

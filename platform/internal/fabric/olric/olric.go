package olric

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"slices"
	"sync"
	"time"

	"github.com/olric-data/olric"
	olricconfig "github.com/olric-data/olric/config"

	"github.com/miroslav-matejovsky/opdl/platform/deployment"
	"github.com/miroslav-matejovsky/opdl/platform/internal/fabric"
)

// Fabric is a fabric backed by an embedded Olric member.
type Fabric struct {
	cfg     Config
	members []fabric.Member
	// clientAddresses maps an expected member's client endpoint to its canonical
	// machine/instance ID, which is how a live Olric member is recognized as a
	// descriptor identity. An Olric member names itself by its client address, so
	// a primary and secondary on one machine map to distinct IDs and never
	// overwrite each other.
	clientAddresses map[string]string

	db     *olric.Olric
	client olric.Client
	serve  chan error

	mu     sync.RWMutex
	closed bool
}

// Open starts an embedded Olric member for a machine's resolved deployment
// descriptor, running as selfInstance, and returns once it is ready. It
// validates the composed configuration before opening any listener. Failure
// leaves nothing running.
func Open(ctx context.Context, descriptor deployment.Descriptor, selfInstance string, cfg Config) (*Fabric, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	members := fabric.MembersFromDescriptor(descriptor, selfInstance)
	addresses, err := clientAddresses(descriptor, members, cfg.ClientAddress)
	if err != nil {
		return nil, err
	}

	// Nothing is created for a caller that has already given up. Starting a
	// member only to tear it down immediately races Olric's own startup, and a
	// member torn down mid-start does not reliably stop.
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("olric: start member on %s: %w", cfg.ClientAddress, err)
	}

	olricCfg, err := cfg.olricConfig()
	if err != nil {
		return nil, err
	}
	ready := make(chan struct{})
	olricCfg.Started = func() { close(ready) }

	db, err := olric.New(olricCfg)
	if err != nil {
		return nil, fmt.Errorf("olric: create member: %w", err)
	}
	f := &Fabric{cfg: cfg, members: members, clientAddresses: addresses, db: db, serve: make(chan error, 1)}
	go func() { f.serve <- db.Start() }()

	fail := func(cause error) error {
		return fmt.Errorf("olric: start member on %s: %w", cfg.ClientAddress,
			errors.Join(cause, f.abandon(ctx)))
	}
	select {
	case <-ready:
		f.client = db.NewEmbeddedClient()
		return f, nil
	case err := <-f.serve:
		return nil, fail(err)
	case <-ctx.Done():
		return nil, fail(ctx.Err())
	case <-time.After(cfg.StartTimeout):
		return nil, fail(fmt.Errorf("not ready after %s", cfg.StartTimeout))
	}
}

// abandon tears down a member that never became ready. It gets its own bounded
// context because the caller's may already be canceled, and a failed startup
// still has to leave nothing running: a leaked member keeps its sockets and
// breaks whatever starts next.
func (f *Fabric) abandon(ctx context.Context) error {
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), f.cfg.ShutdownGrace)
	defer cancel()
	return f.shutdown(stopCtx)
}

// Name returns this adapter's implementation name.
func (f *Fabric) Name() string { return Name }

// Collection opens the named collection. An Olric DMap is created on first use
// and is the same map on every member, so opening is idempotent site-wide.
func (f *Fabric) Collection(name string) (fabric.Collection, error) {
	if err := fabric.ValidateName(name); err != nil {
		return nil, err
	}
	if err := f.check(context.Background()); err != nil {
		return nil, err
	}
	dmap, err := f.client.NewDMap(name)
	if err != nil {
		return nil, fmt.Errorf("olric: open collection %q: %w", name, err)
	}
	return &collection{name: name, dmap: dmap, fabric: f}, nil
}

// Members returns the expected site membership from the descriptor. It does not
// consult Olric: who belongs to the site is a deployment fact, and a member that
// is currently unreachable still belongs.
func (f *Fabric) Members() []fabric.Member {
	return slices.Clone(f.members)
}

// State reports how much of the expected membership Olric currently reaches.
func (f *Fabric) State(ctx context.Context) (fabric.State, error) {
	if err := f.check(ctx); err != nil {
		return "", err
	}
	reachable, err := f.Reachable(ctx)
	if err != nil {
		return "", err
	}
	return fabric.StateFor(len(f.members), len(reachable)), nil
}

// Reachable returns the expected members Olric currently sees, ordered by
// machine and then primary before secondary. It maps live members back to
// descriptor identities by client endpoint and ignores anything it cannot place,
// so a stray member never appears as part of this site, and a primary and
// secondary on one machine are reported as the two distinct members they are. It
// is exported for diagnostics; membership decisions use Members.
func (f *Fabric) Reachable(ctx context.Context) ([]fabric.Member, error) {
	if err := f.check(ctx); err != nil {
		return nil, err
	}
	live, err := f.client.Members(ctx)
	if err != nil {
		return nil, fmt.Errorf("olric: list members: %w", err)
	}
	ids := make(map[string]bool, len(live))
	for _, member := range live {
		if id, ok := f.clientAddresses[member.Name]; ok {
			ids[id] = true
		}
	}
	reachable := make([]fabric.Member, 0, len(ids))
	for _, member := range f.members {
		if ids[member.ID()] {
			reachable = append(reachable, member)
		}
	}
	return reachable, nil
}

// Close drains and shuts the member down within ctx. It is idempotent.
func (f *Fabric) Close(ctx context.Context) error {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return nil
	}
	f.closed = true
	f.mu.Unlock()
	return f.shutdown(ctx)
}

// shutdown closes the client and the member, preserving every failure. It is
// also the failure path of Open, so it tolerates a partly built fabric.
//
// It waits for the serving goroutine to return rather than peeking at it, so
// that a returning shutdown means the member is really gone and its sockets are
// really free. Not waiting leaves the next member to start racing this one's
// teardown.
func (f *Fabric) shutdown(ctx context.Context) error {
	var errs []error
	if f.client != nil {
		if err := f.client.Close(ctx); err != nil {
			errs = append(errs, fmt.Errorf("olric: close client: %w", err))
		}
	}
	if f.db != nil {
		if err := f.db.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("olric: shutdown member: %w", err))
		}
	}
	select {
	case err := <-f.serve:
		if err != nil {
			errs = append(errs, fmt.Errorf("olric: member: %w", err))
		}
	case <-ctx.Done():
		errs = append(errs, fmt.Errorf("olric: member did not stop: %w", ctx.Err()))
	}
	return errors.Join(errs...)
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

// olricConfig translates the adapter's configuration into Olric's. It pins the
// first-use-case constraints: memory storage, no expiry, and one member per
// machine. Olric's logs are discarded here and the platform reports fabric
// lifecycle through its own events instead.
func (c Config) olricConfig() (*olricconfig.Config, error) {
	cfg := olricconfig.New("local")
	cfg.LogOutput = io.Discard
	cfg.Logger = log.New(io.Discard, "", 0)
	cfg.LogLevel = "ERROR"
	cfg.Peers = slices.Clone(c.Join)

	var err error
	if cfg.BindAddr, cfg.BindPort, err = splitAddress(c.ClientAddress); err != nil {
		return nil, err
	}
	if cfg.MemberlistConfig.BindAddr, cfg.MemberlistConfig.BindPort, err = splitAddress(c.MemberlistAddress); err != nil {
		return nil, err
	}
	// Advertise exactly what was bound. A peer seeds from the address the
	// descriptor gave it, so the member must be reachable at that address and
	// not at whatever Olric would infer about the host.
	cfg.MemberlistConfig.AdvertiseAddr = cfg.MemberlistConfig.BindAddr
	cfg.MemberlistConfig.AdvertisePort = cfg.MemberlistConfig.BindPort

	if err := cfg.Sanitize(); err != nil {
		return nil, fmt.Errorf("olric: sanitize configuration: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("olric: invalid configuration: %w", err)
	}
	return cfg, nil
}

// clientAddresses maps each expected member's client endpoint to its canonical
// machine/instance ID, so a live Olric member can be recognized as a descriptor
// identity. A primary and secondary on one machine have distinct client
// endpoints, so they never collapse into one entry.
//
// Self is keyed by the address this member actually listens on, because that is
// known exactly and may have been overridden. Every other member is keyed by its
// explicit descriptor client endpoint: a sibling instance by its entry in this
// machine's platform instances, a peer instance by its peer record.
func clientAddresses(descriptor deployment.Descriptor, members []fabric.Member, selfAddress string) (map[string]string, error) {
	addresses := make(map[string]string, len(members))
	for _, member := range members {
		if member.Self {
			addresses[selfAddress] = member.ID()
			continue
		}
		endpoint, err := memberClientEndpoint(descriptor, member)
		if err != nil {
			return nil, err
		}
		addresses[endpoint] = member.ID()
	}
	return addresses, nil
}

// memberClientEndpoint returns the explicit client endpoint a non-self member
// listens on, from the descriptor. A sibling instance is found among this
// machine's platform instances; a peer instance among the fabric peers.
func memberClientEndpoint(descriptor deployment.Descriptor, member fabric.Member) (string, error) {
	if member.Machine == descriptor.Machine {
		instance, found := instanceByName(descriptor.PlatformInstances, member.Instance)
		if !found || instance.FabricClientAddress == "" {
			return "", fmt.Errorf("olric: local instance %q has no client endpoint in deployment descriptor", member.ID())
		}
		return instance.FabricClientAddress, nil
	}
	for _, peer := range descriptor.Fabric.Peers {
		if peer.Machine == member.Machine && peer.Instance == member.Instance {
			if peer.FabricClientAddress == "" {
				return "", fmt.Errorf("olric: member %q has no client endpoint in deployment descriptor", member.ID())
			}
			return peer.FabricClientAddress, nil
		}
	}
	return "", fmt.Errorf("olric: member %q has no client endpoint in deployment descriptor", member.ID())
}

// collection is one Olric DMap behind the fabric's collection contract.
type collection struct {
	name   string
	dmap   olric.DMap
	fabric *Fabric
}

// Create stores value under key only if key is absent.
func (c *collection) Create(ctx context.Context, key string, value []byte) (bool, error) {
	if err := c.check(ctx, key); err != nil {
		return false, err
	}
	// Olric copies the value before Put returns, so the caller's slice is not
	// retained and needs no defensive copy here.
	err := c.dmap.Put(ctx, key, value, olric.NX())
	if errors.Is(err, olric.ErrKeyFound) {
		return false, nil
	}
	if err != nil {
		return false, c.wrap("create", key, err)
	}
	return true, nil
}

// Swap stores value under key and returns the value it replaced.
func (c *collection) Swap(ctx context.Context, key string, value []byte) (previous []byte, existed bool, err error) {
	if err := c.check(ctx, key); err != nil {
		return nil, false, err
	}
	response, err := c.dmap.GetPut(ctx, key, value)
	if err != nil {
		return nil, false, c.wrap("swap", key, err)
	}
	previous, existed, err = decode(response)
	if err != nil {
		return nil, false, c.wrap("swap", key, err)
	}
	return previous, existed, nil
}

// Get returns the value stored under key.
func (c *collection) Get(ctx context.Context, key string) (value []byte, found bool, err error) {
	if err := c.check(ctx, key); err != nil {
		return nil, false, err
	}
	response, err := c.dmap.Get(ctx, key)
	if errors.Is(err, olric.ErrKeyNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, c.wrap("get", key, err)
	}
	value, found, err = decode(response)
	if err != nil {
		return nil, false, c.wrap("get", key, err)
	}
	return value, found, nil
}

// Entries returns the collection's entries ordered by key. Olric's iterator
// yields keys, so each value is read separately: the enumeration is therefore
// weakly consistent, and a key deleted between the scan and its read is simply
// left out rather than reported as an error.
func (c *collection) Entries(ctx context.Context) ([]fabric.Entry, error) {
	if err := c.fabric.check(ctx); err != nil {
		return nil, err
	}
	iterator, err := c.dmap.Scan(ctx)
	if err != nil {
		return nil, c.wrap("enumerate", "", err)
	}
	defer iterator.Close()

	var keys []string
	for iterator.Next() {
		keys = append(keys, iterator.Key())
	}
	slices.Sort(keys)

	entries := make([]fabric.Entry, 0, len(keys))
	for _, key := range keys {
		value, found, err := c.Get(ctx, key)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		entries = append(entries, fabric.Entry{Key: key, Value: value})
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

// wrap reports a backend failure with the collection and key it happened on.
func (c *collection) wrap(op, key string, err error) error {
	if key == "" {
		return fmt.Errorf("olric: %s collection %q: %w", op, c.name, err)
	}
	return fmt.Errorf("olric: %s collection %q key %q: %w", op, c.name, key, err)
}

// decode reads the bytes out of an Olric response.
//
// A response for a key that holds nothing is not nil, despite what the DMap
// documentation says: it is a response whose read reports ErrNilResponse. That
// is how GetPut reports "there was no previous value", so it is translated to
// absence here rather than treated as a failure. An empty stored value is
// distinct: it reads back as zero bytes with no error.
func decode(response *olric.GetResponse) (value []byte, found bool, err error) {
	if response == nil {
		return nil, false, nil
	}
	value, err = response.Byte()
	if errors.Is(err, olric.ErrNilResponse) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read value: %w", err)
	}
	// Olric hands back its own buffer; the fabric promises the caller owns what
	// it receives.
	return slices.Clone(value), true, nil
}

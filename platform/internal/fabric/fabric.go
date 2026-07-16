package fabric

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/miroslav-matejovsky/opdl/platform/deployment"
)

// ErrClosed reports a call on a fabric that has been closed. Adapters return it
// rather than panicking or blocking, so a late caller during shutdown fails
// predictably.
var ErrClosed = errors.New("fabric: closed")

// State is the reachability of the expected site members. It says nothing about
// whether any particular value is present: it answers who can be reached, not
// what they hold.
type State string

const (
	// StateConnected reports every expected member is reachable. A site of one
	// machine is connected on its own.
	StateConnected State = "connected"
	// StateDegraded reports some but not all expected members are reachable.
	// The fabric still serves calls; it is carrying less than the site.
	StateDegraded State = "degraded"
	// StateDisconnected reports no peer is reachable.
	StateDisconnected State = "disconnected"
)

// Member is one expected fabric member of a site, as the deployment descriptor
// names it. It is a deployment identity and carries no transport detail: a
// consumer correlates a member with the machine it runs on, not with a socket.
type Member struct {
	// Site is the member's site. Every member of one fabric shares it.
	Site string
	// Machine is the member's machine identity, unique within the site.
	Machine string
	// IP is the address the member is reached on.
	IP string
	// Self reports whether this member is the local machine.
	Self bool
}

// Entry is one key and its value from a collection.
type Entry struct {
	// Key is the entry's key within its collection.
	Key string
	// Value is a copy of the stored bytes, owned by the caller.
	Value []byte
}

// Fabric is the platform's distribution boundary. Runtime composition builds one
// and hands it to the code that needs to share state; that code depends on this
// interface and never on the adapter behind it.
type Fabric interface {
	// Name is the adapter implementation's name, e.g. "olric". It is
	// operational metadata for logs and lifecycle events: nothing decides
	// behavior on it, and no capability is implied by it. It is here so the
	// runtime can report which backend it composed without knowing what that
	// backend is.
	Name() string
	// Collection opens the named collection. Opening is idempotent: the same
	// name is the same shared collection on every member of the site, and
	// opening it twice on one member is not an error. A blank name is an error.
	Collection(name string) (Collection, error)
	// Members returns the expected site membership, including self, ordered by
	// machine name. It is fixed for the process's life and does not reflect who
	// is reachable now; see State for that.
	Members() []Member
	// State reports whether the expected members are reachable.
	State(ctx context.Context) (State, error)
	// Close drains in-flight work and releases the fabric within ctx. It is
	// idempotent, and it closes the collections opened from this fabric.
	Close(ctx context.Context) error
}

// Collection is a named map of string keys to byte values shared by the members
// of one site. See the package documentation for the ownership, atomicity, and
// consistency each method promises.
type Collection interface {
	// Create stores value under key only if key is absent, and reports whether
	// this call was the one that stored it. While membership is stable, it is
	// atomic for key: given concurrent Creates of one key, exactly one reports
	// true. A member join can temporarily violate that guarantee; callers that
	// require site-wide uniqueness must retain and reconcile contenders. It copies
	// value.
	Create(ctx context.Context, key string, value []byte) (bool, error)
	// Swap stores value under key unconditionally and returns the value it
	// replaced, reporting false when key was absent. It is atomic for key, and
	// it copies value. The returned bytes are the caller's.
	Swap(ctx context.Context, key string, value []byte) (previous []byte, existed bool, err error)
	// Get returns the value stored under key, reporting false when key is
	// absent. The returned bytes are the caller's.
	Get(ctx context.Context, key string) (value []byte, found bool, err error)
	// Entries returns the collection's entries, ordered by key. It is weakly
	// consistent: it is not a snapshot, and a concurrent write may or may not
	// appear. The returned bytes are the caller's.
	Entries(ctx context.Context) ([]Entry, error)
}

// MembersFromDescriptor derives the expected site membership from a machine's
// resolved deployment descriptor. Both adapters build their membership with it,
// so "who belongs to this fabric" has exactly one definition and comes from the
// descriptor rather than from anything observed at runtime.
//
// Stage 1 descriptors expose both platform instances, but the runtime still
// runs one primary fabric member per machine until Stage 2 makes membership
// instance-aware. Secondary peer records are therefore ignored here. The
// result is ordered by machine name and includes self, so every member of a
// site derives an identical list.
func MembersFromDescriptor(descriptor deployment.Descriptor) []Member {
	members := make([]Member, 0, len(descriptor.Fabric.Peers)+1)
	members = append(members, Member{
		Site:    descriptor.Site,
		Machine: descriptor.Machine,
		IP:      descriptor.IP,
		Self:    true,
	})
	for _, peer := range descriptor.Fabric.Peers {
		if peer.Instance == deployment.PlatformInstanceSecondary {
			continue
		}
		members = append(members, Member{
			Site:    peer.Site,
			Machine: peer.Machine,
			IP:      peer.IP,
		})
	}
	slices.SortFunc(members, func(a, b Member) int { return strings.Compare(a.Machine, b.Machine) })
	return members
}

// Self returns the local member of members.
func Self(members []Member) (Member, bool) {
	for _, member := range members {
		if member.Self {
			return member, true
		}
	}
	return Member{}, false
}

// StateFor derives the fabric state from how many of the expected members are
// currently reachable. reachable counts members an adapter can see, including
// self. It is shared so both adapters answer the same way, including for the
// one-member site that must read as connected rather than disconnected.
func StateFor(expected, reachable int) State {
	switch {
	case reachable >= expected:
		return StateConnected
	case reachable <= 1:
		return StateDisconnected
	default:
		return StateDegraded
	}
}

// ValidateName checks a collection name is usable. Names are part of the
// platform's shared vocabulary across machines, so they must be explicit rather
// than derived from whitespace or empty defaults.
func ValidateName(name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("fabric: collection name is required")
	}
	if name != strings.TrimSpace(name) {
		return fmt.Errorf("fabric: collection name %q has surrounding whitespace", name)
	}
	return nil
}

// ValidateKey checks a key is usable within a collection.
func ValidateKey(name, key string) error {
	if key == "" {
		return fmt.Errorf("fabric: collection %q: key is required", name)
	}
	return nil
}

package fabric_test

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/deployment"
	"github.com/miroslav-matejovsky/opdl/platform/internal/fabric"
	"github.com/miroslav-matejovsky/opdl/platform/internal/fabric/memory"
	fabricolric "github.com/miroslav-matejovsky/opdl/platform/internal/fabric/olric"
)

// This is the fabric's contract suite: one set of tests, run against every
// adapter. The fabric's promises are only real if more than one backend keeps
// them, so an adapter is not finished until it passes this unchanged. It tests
// through the fabric.Fabric interface only and knows nothing about any backend.

// testSite is the site every adapter under test is built for: this machine plus
// one peer it never has to reach, so expected membership is testable
// independently of reachability.
const testSite = "local"

func testDescriptor() deployment.Descriptor {
	return deployment.Descriptor{
		Site:    testSite,
		Machine: "node-a",
		IP:      "127.0.0.1",
		PlatformInstances: []deployment.PlatformInstance{
			{Name: "primary", APIAddress: "127.0.0.1:8080", FabricClientAddress: "127.0.0.1:3320", FabricMemberlistAddress: "127.0.0.1:3322"},
		},
		Fabric: deployment.Fabric{
			Peers: []deployment.FabricPeer{
				{Site: testSite, Machine: "node-b", Instance: "primary", IP: "127.0.0.2", FabricClientAddress: "127.0.0.2:3320", FabricMemberlistAddress: "127.0.0.2:3322"},
				{Site: testSite, Machine: "node-b", Instance: "secondary", IP: "127.0.0.2", FabricClientAddress: "127.0.0.2:3321", FabricMemberlistAddress: "127.0.0.2:3323"},
			},
		},
	}
}

// soloTopology is a single-machine site, which must form a fabric of one rather
// than a fabric that is missing everybody.
func soloDescriptor() deployment.Descriptor {
	return deployment.Descriptor{
		Site: testSite, Machine: "node-a", IP: "127.0.0.1",
		PlatformInstances: []deployment.PlatformInstance{
			{Name: "primary", APIAddress: "127.0.0.1:8080", FabricClientAddress: "127.0.0.1:3320", FabricMemberlistAddress: "127.0.0.1:3322"},
		},
	}
}

// adapter is one implementation under test. Every adapter answers identically:
// where two backends may legitimately differ is in what they can reach, and the
// suite pins that by choosing topologies whose reachability is unambiguous.
type adapter struct {
	name string
	// open builds a fabric for a topology, ready to use.
	open func(t *testing.T, descriptor deployment.Descriptor) fabric.Fabric
}

// adapters returns every implementation the contract applies to.
func adapters() []adapter {
	return []adapter{
		{
			name: "memory",
			open: func(t *testing.T, descriptor deployment.Descriptor) fabric.Fabric {
				t.Helper()
				f := memory.Open(descriptor, "primary")
				t.Cleanup(func() { _ = f.Close(context.Background()) })
				return f
			},
		},
		{
			name: "olric",
			open: func(t *testing.T, descriptor deployment.Descriptor) fabric.Fabric {
				t.Helper()
				requireIntegration(t)
				return openOlric(t, descriptor)
			},
		},
	}
}

// requireIntegration skips tests that bind sockets and start real members.
func requireIntegration(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping fabric integration test in -short mode")
	}
}

// openOlric starts a real member on dynamic ports, so tests never collide with
// a developer's machine or with each other. Its peer is left unreachable on
// purpose: the fabric must be usable before its site is whole.
func openOlric(t *testing.T, descriptor deployment.Descriptor) fabric.Fabric {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	f, err := fabricolric.Open(ctx, descriptor, "primary", fabricolric.Config{
		ClientAddress:     freeAddress(t),
		MemberlistAddress: freeAddress(t),
		StartTimeout:      60 * time.Second,
		ShutdownGrace:     10 * time.Second,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer closeCancel()
		require.NoError(t, f.Close(closeCtx))
	})
	return f
}

// freeAddress reserves a loopback port, then releases it for the member.
func freeAddress(t *testing.T) string {
	t.Helper()
	var listen net.ListenConfig
	listener, err := listen.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close())
	return address
}

// run executes one contract test against every adapter.
func run(t *testing.T, name string, test func(t *testing.T, a adapter)) {
	t.Helper()
	for _, a := range adapters() {
		t.Run(name+"/"+a.name, func(t *testing.T) { test(t, a) })
	}
}

// collection opens a uniquely named collection, so tests never share state.
func collection(t *testing.T, f fabric.Fabric) fabric.Collection {
	t.Helper()
	c, err := f.Collection("units-" + t.Name())
	require.NoError(t, err)
	return c
}

func TestContract(t *testing.T) {
	run(t, "on stable membership, create stores a value only when the key is absent", func(t *testing.T, a adapter) {
		ctx := t.Context()
		c := collection(t, a.open(t, testDescriptor()))

		created, err := c.Create(ctx, "k", []byte("first"))
		require.NoError(t, err)
		require.True(t, created)

		created, err = c.Create(ctx, "k", []byte("second"))
		require.NoError(t, err)
		require.False(t, created, "a second create must not win")

		value, found, err := c.Get(ctx, "k")
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, []byte("first"), value, "the loser must not overwrite the winner")
	})

	run(t, "get reports a missing key as absent, not as an error", func(t *testing.T, a adapter) {
		value, found, err := collection(t, a.open(t, testDescriptor())).Get(t.Context(), "missing")
		require.NoError(t, err)
		require.False(t, found)
		require.Nil(t, value)
	})

	run(t, "swap replaces a value and reports the previous one", func(t *testing.T, a adapter) {
		ctx := t.Context()
		c := collection(t, a.open(t, testDescriptor()))

		previous, existed, err := c.Swap(ctx, "k", []byte("first"))
		require.NoError(t, err)
		require.False(t, existed, "swapping an absent key reports no previous value")
		require.Nil(t, previous)

		previous, existed, err = c.Swap(ctx, "k", []byte("second"))
		require.NoError(t, err)
		require.True(t, existed)
		require.Equal(t, []byte("first"), previous)

		value, _, err := c.Get(ctx, "k")
		require.NoError(t, err)
		require.Equal(t, []byte("second"), value)
	})

	run(t, "an empty value is stored, not treated as absent", func(t *testing.T, a adapter) {
		ctx := t.Context()
		c := collection(t, a.open(t, testDescriptor()))

		created, err := c.Create(ctx, "k", []byte{})
		require.NoError(t, err)
		require.True(t, created)

		value, found, err := c.Get(ctx, "k")
		require.NoError(t, err)
		require.True(t, found, "a key holding zero bytes is present")
		require.Empty(t, value)

		created, err = c.Create(ctx, "k", []byte("late"))
		require.NoError(t, err)
		require.False(t, created, "a key holding zero bytes still occupies the key")
	})

	run(t, "entries enumerates the collection ordered by key", func(t *testing.T, a adapter) {
		ctx := t.Context()
		f := a.open(t, testDescriptor())
		c := collection(t, f)

		require.Empty(t, mustEntries(ctx, t, c), "a fresh collection enumerates as empty")

		for _, key := range []string{"c", "a", "b"} {
			_, err := c.Create(ctx, key, []byte("v-"+key))
			require.NoError(t, err)
		}
		require.Equal(t, []fabric.Entry{
			{Key: "a", Value: []byte("v-a")},
			{Key: "b", Value: []byte("v-b")},
			{Key: "c", Value: []byte("v-c")},
		}, mustEntries(ctx, t, c))
	})

	run(t, "collections are isolated from each other", func(t *testing.T, a adapter) {
		ctx := t.Context()
		f := a.open(t, testDescriptor())
		first, err := f.Collection("first-" + t.Name())
		require.NoError(t, err)
		second, err := f.Collection("second-" + t.Name())
		require.NoError(t, err)

		_, err = first.Create(ctx, "k", []byte("v"))
		require.NoError(t, err)

		_, found, err := second.Get(ctx, "k")
		require.NoError(t, err)
		require.False(t, found, "a key in one collection is not in another")
	})

	run(t, "opening the same collection twice is the same collection", func(t *testing.T, a adapter) {
		ctx := t.Context()
		f := a.open(t, testDescriptor())
		name := "shared-" + t.Name()
		first, err := f.Collection(name)
		require.NoError(t, err)
		second, err := f.Collection(name)
		require.NoError(t, err)

		_, err = first.Create(ctx, "k", []byte("v"))
		require.NoError(t, err)
		value, found, err := second.Get(ctx, "k")
		require.NoError(t, err)
		require.True(t, found, "opening by name is idempotent, not a new collection")
		require.Equal(t, []byte("v"), value)
	})

	run(t, "a blank collection name is rejected", func(t *testing.T, a adapter) {
		_, err := a.open(t, testDescriptor()).Collection("  ")
		require.ErrorContains(t, err, "collection name is required")
	})

	run(t, "a blank key is rejected", func(t *testing.T, a adapter) {
		ctx := t.Context()
		c := collection(t, a.open(t, testDescriptor()))
		_, err := c.Create(ctx, "", []byte("v"))
		require.ErrorContains(t, err, "key is required")
		_, _, err = c.Get(ctx, "")
		require.ErrorContains(t, err, "key is required")
	})

	run(t, "the caller owns the bytes it writes and reads", func(t *testing.T, a adapter) {
		ctx := t.Context()
		c := collection(t, a.open(t, testDescriptor()))

		written := []byte("original")
		_, err := c.Create(ctx, "k", written)
		require.NoError(t, err)
		// Mutating the written slice must not reach into the fabric.
		copy(written, "MUTATED!")

		value, _, err := c.Get(ctx, "k")
		require.NoError(t, err)
		require.Equal(t, []byte("original"), value, "the fabric must copy what it is given")

		// Mutating a returned slice must not reach into the fabric either.
		copy(value, "MUTATED!")
		again, _, err := c.Get(ctx, "k")
		require.NoError(t, err)
		require.Equal(t, []byte("original"), again, "the fabric must return copies")
	})

	run(t, "on stable membership, concurrent creates of one key produce exactly one winner", func(t *testing.T, a adapter) {
		ctx := t.Context()
		c := collection(t, a.open(t, testDescriptor()))

		const writers = 16
		results := make(chan bool, writers)
		failures := make(chan error, writers)
		var group sync.WaitGroup
		for i := range writers {
			group.Go(func() {
				created, err := c.Create(ctx, "contested", fmt.Appendf(nil, "writer-%d", i))
				results <- created
				failures <- err
			})
		}
		group.Wait()
		close(results)
		close(failures)

		for err := range failures {
			require.NoError(t, err)
		}
		winners := 0
		for created := range results {
			if created {
				winners++
			}
		}
		require.Equal(t, 1, winners, "exactly one concurrent create must win")

		// The surviving value is one writer's, whole: no interleaving.
		value, found, err := c.Get(ctx, "contested")
		require.NoError(t, err)
		require.True(t, found)
		require.True(t, bytes.HasPrefix(value, []byte("writer-")), "stored %q", value)
	})

	run(t, "expected members come from the descriptor, not from who is reachable", func(t *testing.T, a adapter) {
		members := a.open(t, testDescriptor()).Members()
		require.Equal(t, []fabric.Member{
			{Site: testSite, Machine: "node-a", Instance: "primary", IP: "127.0.0.1", Self: true},
			{Site: testSite, Machine: "node-b", Instance: "primary", IP: "127.0.0.2"},
			{Site: testSite, Machine: "node-b", Instance: "secondary", IP: "127.0.0.2"},
		}, members, "both instances of the redundant peer are members, ordered primary before secondary")

		self, ok := fabric.Self(members)
		require.True(t, ok)
		require.Equal(t, "node-a/primary", self.ID())
	})

	run(t, "members are a copy the caller cannot use to mutate the fabric", func(t *testing.T, a adapter) {
		f := a.open(t, testDescriptor())
		members := f.Members()
		members[0].Machine = "tampered"
		require.Equal(t, "node-a", f.Members()[0].Machine)
	})

	run(t, "a site whose peers are all unreachable is disconnected", func(t *testing.T, a adapter) {
		// Only this member is up, so no peer is reachable. The fabric still
		// works: state describes the site, not whether calls succeed.
		f := a.open(t, testDescriptor())
		state, err := f.State(t.Context())
		require.NoError(t, err)
		require.Equal(t, fabric.StateDisconnected, state)

		_, err = collection(t, f).Create(t.Context(), "k", []byte("v"))
		require.NoError(t, err, "a disconnected fabric still serves its own member")
	})

	run(t, "a one-member site is connected on its own", func(t *testing.T, a adapter) {
		// A standalone deployment is whole, not permanently disconnected.
		f := a.open(t, soloDescriptor())
		state, err := f.State(t.Context())
		require.NoError(t, err)
		require.Equal(t, fabric.StateConnected, state)
		require.Len(t, f.Members(), 1)
	})

	run(t, "a canceled context fails a call", func(t *testing.T, a adapter) {
		c := collection(t, a.open(t, testDescriptor()))
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err := c.Create(ctx, "k", []byte("v"))
		require.ErrorIs(t, err, context.Canceled)
		_, _, err = c.Get(ctx, "k")
		require.ErrorIs(t, err, context.Canceled)
		_, _, err = c.Swap(ctx, "k", []byte("v"))
		require.ErrorIs(t, err, context.Canceled)
		_, err = c.Entries(ctx)
		require.ErrorIs(t, err, context.Canceled)
	})

	run(t, "calls after close report the fabric is closed", func(t *testing.T, a adapter) {
		ctx := t.Context()
		f := a.open(t, testDescriptor())
		c := collection(t, f)
		require.NoError(t, f.Close(ctx))

		_, err := c.Create(ctx, "k", []byte("v"))
		require.ErrorIs(t, err, fabric.ErrClosed)
		_, _, err = c.Get(ctx, "k")
		require.ErrorIs(t, err, fabric.ErrClosed)
		_, _, err = c.Swap(ctx, "k", []byte("v"))
		require.ErrorIs(t, err, fabric.ErrClosed)
		_, err = c.Entries(ctx)
		require.ErrorIs(t, err, fabric.ErrClosed)
		_, err = f.Collection("late")
		require.ErrorIs(t, err, fabric.ErrClosed)
		_, err = f.State(ctx)
		require.ErrorIs(t, err, fabric.ErrClosed)
	})

	run(t, "close is idempotent", func(t *testing.T, a adapter) {
		ctx := t.Context()
		f := a.open(t, testDescriptor())
		require.NoError(t, f.Close(ctx))
		require.NoError(t, f.Close(ctx), "a second close is not an error")
	})

	run(t, "members remain readable after close", func(t *testing.T, a adapter) {
		// Membership is a deployment fact, not a live query, so shutdown code
		// can still report which site it was part of.
		f := a.open(t, testDescriptor())
		require.NoError(t, f.Close(t.Context()))
		require.Len(t, f.Members(), 3)
	})
}

// mustEntries enumerates a collection.
func mustEntries(ctx context.Context, t *testing.T, c fabric.Collection) []fabric.Entry {
	t.Helper()
	entries, err := c.Entries(ctx)
	require.NoError(t, err)
	return entries
}

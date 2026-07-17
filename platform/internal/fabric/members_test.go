package fabric_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/deployment"
	"github.com/miroslav-matejovsky/opdl/platform/internal/fabric"
)

// These tests pin how the descriptor becomes the site's expected membership: one
// member per configured platform instance, one Self per process, and an ordering
// every member of the site derives identically.

// primaryOnly is a machine that runs only its primary instance.
func primaryOnly(ip string) []deployment.PlatformInstance {
	return []deployment.PlatformInstance{
		{Name: "primary", APIAddress: ip + ":8080", FabricClientAddress: ip + ":3320", FabricMemberlistAddress: ip + ":3322"},
	}
}

// redundant is a machine that runs its primary and secondary.
func redundant(ip string) []deployment.PlatformInstance {
	return []deployment.PlatformInstance{
		{Name: "primary", APIAddress: ip + ":8080", FabricClientAddress: ip + ":3320", FabricMemberlistAddress: ip + ":3322"},
		{Name: "secondary", APIAddress: ip + ":8081", FabricClientAddress: ip + ":3321", FabricMemberlistAddress: ip + ":3323"},
	}
}

// TestPrimaryOnlyOneMachineSiteHasOneMember checks a machine that runs only its
// primary, alone, is a member of one: itself.
func TestPrimaryOnlyOneMachineSiteHasOneMember(t *testing.T) {
	d := deployment.Descriptor{
		Site: "north", Machine: "node-a", IP: "10.0.0.1",
		PlatformInstances: primaryOnly("10.0.0.1"),
	}
	members := fabric.MembersFromDescriptor(d, "primary")
	require.Equal(t, []fabric.Member{
		{Site: "north", Machine: "node-a", Instance: "primary", IP: "10.0.0.1", Self: true},
	}, members)

	self, ok := fabric.Self(members)
	require.True(t, ok)
	require.Equal(t, "node-a/primary", self.ID())
	require.Equal(t, fabric.StateConnected, fabric.StateFor(len(members), 1),
		"a one-instance site is connected by itself")
}

// TestDefaultOneMachineSiteHasTwoInstances checks a redundant machine, alone, is
// a site of its own two instances, with unique IDs and exactly one Self per
// process.
func TestDefaultOneMachineSiteHasTwoInstances(t *testing.T) {
	d := deployment.Descriptor{
		Site: "north", Machine: "node-a", IP: "10.0.0.1",
		PlatformInstances: redundant("10.0.0.1"),
	}

	fromPrimary := fabric.MembersFromDescriptor(d, "primary")
	require.Equal(t, []fabric.Member{
		{Site: "north", Machine: "node-a", Instance: "primary", IP: "10.0.0.1", Self: true},
		{Site: "north", Machine: "node-a", Instance: "secondary", IP: "10.0.0.1"},
	}, fromPrimary, "both instances are members, the primary is Self, and their IDs are unique")

	// The same machine seen by its secondary process derives the same members,
	// but now the secondary is the one Self.
	fromSecondary := fabric.MembersFromDescriptor(d, "secondary")
	require.Equal(t, []fabric.Member{
		{Site: "north", Machine: "node-a", Instance: "primary", IP: "10.0.0.1"},
		{Site: "north", Machine: "node-a", Instance: "secondary", IP: "10.0.0.1", Self: true},
	}, fromSecondary, "primary before secondary regardless of which one is Self")

	require.Equal(t, 1, selfCount(fromPrimary))
	require.Equal(t, 1, selfCount(fromSecondary))
}

// TestMixedSiteDerivesIdenticalOrderedMembers checks a site with one redundant
// and one primary-only machine derives the same ordered three members from every
// descriptor, differing only in which one is Self.
func TestMixedSiteDerivesIdenticalOrderedMembers(t *testing.T) {
	const site = "north"
	alphaIP, bravoIP := "10.0.0.1", "10.0.0.2"

	// alpha is redundant; bravo runs only its primary.
	alphaPeers := []deployment.FabricPeer{
		{Site: site, Machine: "bravo", Instance: "primary", IP: bravoIP, FabricClientAddress: bravoIP + ":3320", FabricMemberlistAddress: bravoIP + ":3322"},
	}
	bravoPeers := []deployment.FabricPeer{
		{Site: site, Machine: "alpha", Instance: "primary", IP: alphaIP, FabricClientAddress: alphaIP + ":3320", FabricMemberlistAddress: alphaIP + ":3322"},
		{Site: site, Machine: "alpha", Instance: "secondary", IP: alphaIP, FabricClientAddress: alphaIP + ":3321", FabricMemberlistAddress: alphaIP + ":3323"},
	}
	alpha := deployment.Descriptor{Site: site, Machine: "alpha", IP: alphaIP, PlatformInstances: redundant(alphaIP), Fabric: deployment.Fabric{Peers: alphaPeers}}
	bravo := deployment.Descriptor{Site: site, Machine: "bravo", IP: bravoIP, PlatformInstances: primaryOnly(bravoIP), Fabric: deployment.Fabric{Peers: bravoPeers}}

	fromAlpha := fabric.MembersFromDescriptor(alpha, "primary")
	fromBravo := fabric.MembersFromDescriptor(bravo, "primary")

	// The ordered identities are the same from either machine: machine order,
	// primary before secondary.
	wantIDs := []string{"alpha/primary", "alpha/secondary", "bravo/primary"}
	require.Equal(t, wantIDs, ids(fromAlpha))
	require.Equal(t, wantIDs, ids(fromBravo))

	// The members are identical apart from which one each process calls Self.
	require.Equal(t, clearSelf(fromAlpha), clearSelf(fromBravo),
		"every descriptor derives the same membership")
	self, _ := fabric.Self(fromAlpha)
	require.Equal(t, "alpha/primary", self.ID())
	self, _ = fabric.Self(fromBravo)
	require.Equal(t, "bravo/primary", self.ID())
}

// selfCount reports how many members are marked Self.
func selfCount(members []fabric.Member) int {
	count := 0
	for _, member := range members {
		if member.Self {
			count++
		}
	}
	return count
}

// ids returns the members' canonical IDs in order.
func ids(members []fabric.Member) []string {
	out := make([]string, 0, len(members))
	for _, member := range members {
		out = append(out, member.ID())
	}
	return out
}

// clearSelf returns members with Self zeroed, so two viewpoints of one site can
// be compared for the identity they share.
func clearSelf(members []fabric.Member) []fabric.Member {
	out := make([]fabric.Member, len(members))
	for i, member := range members {
		member.Self = false
		out[i] = member
	}
	return out
}

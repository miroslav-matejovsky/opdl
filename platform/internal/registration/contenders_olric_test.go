package registration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/api"
	"github.com/miroslav-matejovsky/opdl/platform/deployment"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
	fabricolric "github.com/miroslav-matejovsky/opdl/platform/internal/fabric/olric"
)

func TestJoinConvergesRetainedContenders(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Olric contender regression test in -short mode")
	}
	ctx := context.Background()
	nodeA, nodeB := contenderDescriptors()
	aConfig, err := fabricolric.DefaultConfig(nodeA)
	require.NoError(t, err)
	bConfig, err := fabricolric.DefaultConfig(nodeB)
	require.NoError(t, err)

	fabricA := openContenderFabric(t, nodeA, aConfig)
	fabricB := openContenderFabric(t, nodeB, bConfig)
	serviceA, reconcilerA, err := Open(fabricA, events.NopRecorder{})
	require.NoError(t, err)
	_, reconcilerB, err := Open(fabricB, events.NopRecorder{})
	require.NoError(t, err)

	_, err = serviceA.Create(ctx, api.RegistrationRequest{UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "First"})
	require.NoError(t, err)
	first := onlyContender(t, serviceA, unitKey)
	var marker acceptanceRecord
	require.Eventually(t, func() bool {
		if reconcilerA.Reconcile(ctx) != nil || reconcilerB.Reconcile(ctx) != nil {
			return false
		}
		var found bool
		var acceptanceErr error
		marker, found, acceptanceErr = serviceA.store.acceptance(ctx, first)
		return acceptanceErr == nil && found
	}, 30*time.Second, 100*time.Millisecond, "the initial contender was not accepted before rejoin")

	require.NoError(t, fabricB.Close(ctx))
	rejoinedFabricB := openContenderFabric(t, nodeB, bConfig)
	rejoinedServiceB, rejoinedReconcilerB, err := Open(rejoinedFabricB, events.NopRecorder{})
	require.NoError(t, err)
	// This is the state Olric's false Create win can leave behind: the current
	// request and accepted projections name the later contender. The immutable
	// contender and acceptance marker are left intact, which is what this stage
	// relies on to repair the site.
	second := requestRecord{
		Version:                recordVersion,
		UnitType:               unitKey.UnitType,
		UnitID:                 unitKey.UnitID,
		UnitTypeNameAdvertised: "Second",
		OriginMachine:          nodeB.Machine,
		OriginIP:               nodeB.IP,
		ObservedAt:             marker.AcceptedAt.Add(time.Millisecond),
	}
	second.Fingerprint = fingerprintOf(second)
	value, err := encode(second)
	require.NoError(t, err)
	created, err := rejoinedServiceB.store.contenderRecords.Create(ctx, contenderKey(unitKey, second.Fingerprint), value)
	require.NoError(t, err)
	require.True(t, created)
	require.NoError(t, rejoinedServiceB.store.setRequest(ctx, second))
	require.NoError(t, rejoinedServiceB.store.setAccepted(ctx, acceptedFrom(second)))

	require.Eventually(t, func() bool {
		if err := reconcilerA.Reconcile(ctx); err != nil {
			return false
		}
		if err := rejoinedReconcilerB.Reconcile(ctx); err != nil {
			return false
		}
		projection, found, err := serviceA.store.accepted(ctx, unitKey)
		return err == nil && found && projection.Fingerprint == first.Fingerprint
	}, 30*time.Second, 100*time.Millisecond, "reconciliation did not restore the incumbent projection")

	winner, found, err := serviceA.Get(ctx, unitKey)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, api.RegistrationStatusAccepted, winner.Status)
	require.Equal(t, "First", winner.UnitTypeNameAdvertised)
	loser, found, err := rejoinedServiceB.Get(ctx, unitKey)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, api.RegistrationStatusRejected, loser.Status)
	require.Equal(t, ReasonKeyConflict, *loser.Reason)
}

func contenderDescriptors() (nodeA, nodeB deployment.Descriptor) {
	nodeA = deployment.Descriptor{
		Site: "contender-regression", Machine: "node-a", IP: "127.0.0.11",
		Fabric: deployment.Fabric{Peers: []deployment.FabricPeer{{Site: "contender-regression", Machine: "node-b", IP: "127.0.0.12"}}},
	}
	nodeB = deployment.Descriptor{
		Site: "contender-regression", Machine: "node-b", IP: "127.0.0.12",
		Fabric: deployment.Fabric{Peers: []deployment.FabricPeer{{Site: "contender-regression", Machine: "node-a", IP: "127.0.0.11"}}},
	}
	return nodeA, nodeB
}

func openContenderFabric(t *testing.T, descriptor deployment.Descriptor, config fabricolric.Config) *fabricolric.Fabric {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	opened, err := fabricolric.Open(ctx, descriptor, config)
	require.NoError(t, err)
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer closeCancel()
		require.NoError(t, opened.Close(closeCtx))
	})
	return opened
}

func onlyContender(t *testing.T, service *Service, key Key) requestRecord {
	t.Helper()
	contenders, err := service.store.contenders(context.Background(), key)
	require.NoError(t, err)
	require.Len(t, contenders, 1)
	return contenders[0]
}

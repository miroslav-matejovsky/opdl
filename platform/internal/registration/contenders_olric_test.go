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
	ctx := t.Context()
	nodeA, nodeB := contenderDescriptors()
	aConfig, err := fabricolric.DefaultConfig(nodeA)
	require.NoError(t, err)
	bConfig, err := fabricolric.DefaultConfig(nodeB)
	require.NoError(t, err)

	fabricA := openContenderFabric(t, nodeA, aConfig)
	fabricB := openContenderFabric(t, nodeB, bConfig)
	serviceA, reconcilerA, err := Open(fabricA, events.NopRecorder{})
	require.NoError(t, err)
	serviceB, reconcilerB, err := Open(fabricB, events.NopRecorder{})
	require.NoError(t, err)

	firstRequest := api.RegistrationRequest{UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "First"}
	secondRequest := api.RegistrationRequest{UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "Second"}
	_, err = serviceA.Create(ctx, firstRequest)
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

	_, err = serviceB.Create(ctx, secondRequest)
	require.ErrorIs(t, err, ErrConflict,
		"a conflict visible at request time must be refused immediately")
	require.Len(t, contendersFor(t, serviceA, unitKey), 1,
		"a refused conflict must not be retained")

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

	wantList := []api.Registration{winner, loser}
	listedA, err := serviceA.List(ctx)
	require.NoError(t, err)
	require.Equal(t, wantList, listedA)
	listedB, err := rejoinedServiceB.List(ctx)
	require.NoError(t, err)
	require.Equal(t, wantList, listedB)

	wantConflicts, err := serviceA.Conflicts(ctx)
	require.NoError(t, err)
	require.Len(t, wantConflicts, 1)
	require.Equal(t, winner, wantConflicts[0].Winner)
	require.Equal(t, []api.Registration{loser}, wantConflicts[0].Losers)
	gotConflicts, err := rejoinedServiceB.Conflicts(ctx)
	require.NoError(t, err)
	require.Equal(t, wantConflicts, gotConflicts)

	// Reopen registration on the joined member and run another pass. This is a
	// reconciler restart, not a fabric-member restart: the in-memory backend does
	// not promise that partitions survive losing their owner.
	restartedServiceB, restartedReconcilerB, err := Open(rejoinedFabricB, events.NopRecorder{})
	require.NoError(t, err)
	require.NoError(t, reconcilerA.Reconcile(ctx))
	require.NoError(t, restartedReconcilerB.Reconcile(ctx))
	restartedList, err := restartedServiceB.List(ctx)
	require.NoError(t, err)
	require.Equal(t, wantList, restartedList)
	restartedConflicts, err := restartedServiceB.Conflicts(ctx)
	require.NoError(t, err)
	require.Equal(t, wantConflicts, restartedConflicts)
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
	if config.ShutdownGrace == 0 {
		config.ShutdownGrace = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
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
	contenders := contendersFor(t, service, key)
	require.Len(t, contenders, 1)
	return contenders[0]
}

func contendersFor(t *testing.T, service *Service, key Key) []requestRecord {
	t.Helper()
	contenders, err := service.store.contenders(t.Context(), key)
	require.NoError(t, err)
	return contenders
}

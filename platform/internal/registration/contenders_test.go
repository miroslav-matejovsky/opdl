package registration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/api"
)

func TestReconcileKeepsAcceptedIncumbent(t *testing.T) {
	site := newSite(t, "node-a", "node-b")
	nodeA := site.start("node-a")
	nodeB := site.start("node-b")
	first := writeContender(t, nodeA, api.RegistrationRequest{UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "First"}, nodeA.service.location, testTime(10))
	second := writeContender(t, nodeA, api.RegistrationRequest{UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "Second"}, nodeB.service.location, testTime(20))
	writeAcceptance(t, nodeA, first, testTime(15))

	site.reconcile()

	accepted := nodeA.get(t, unitKey)
	require.Equal(t, "First", accepted.UnitTypeNameAdvertised)
	require.Equal(t, api.RegistrationStatusAccepted, accepted.Status)
	loser := nodeB.get(t, unitKey)
	require.Equal(t, "Second", loser.UnitTypeNameAdvertised)
	require.Equal(t, api.RegistrationStatusRejected, loser.Status)
	require.Equal(t, ReasonKeyConflict, *loser.Reason)

	projection, found := site.accepted(nodeA, unitKey)
	require.True(t, found)
	require.Equal(t, first.Fingerprint, projection.Fingerprint)
	require.NotEqual(t, second.Fingerprint, projection.Fingerprint)
}

func TestReconcileChoosesEarliestPendingContender(t *testing.T) {
	site := newSite(t, "node-a", "node-b")
	nodeA := site.start("node-a")
	nodeB := site.start("node-b")
	first := writeContender(t, nodeA, api.RegistrationRequest{UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "First"}, nodeA.service.location, testTime(10))
	writeContender(t, nodeA, api.RegistrationRequest{UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "Second"}, nodeB.service.location, testTime(20))

	site.reconcile()

	view := nodeA.get(t, unitKey)
	require.Equal(t, "First", view.UnitTypeNameAdvertised)
	require.Equal(t, api.RegistrationStatusAccepted, view.Status)
	projection, found := site.accepted(nodeA, unitKey)
	require.True(t, found)
	require.Equal(t, first.Fingerprint, projection.Fingerprint)
}

func TestReconcileBreaksEqualObservedTimesByFingerprint(t *testing.T) {
	site := newSite(t, "node-a", "node-b")
	nodeA := site.start("node-a")
	nodeB := site.start("node-b")
	observedAt := testTime(10)
	left := writeContender(t, nodeA, api.RegistrationRequest{UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "Left"}, nodeA.service.location, observedAt)
	right := writeContender(t, nodeA, api.RegistrationRequest{UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "Right"}, nodeB.service.location, observedAt)

	site.reconcile()

	want := left
	if right.Fingerprint < left.Fingerprint {
		want = right
	}
	projection, found := site.accepted(nodeA, unitKey)
	require.True(t, found)
	require.Equal(t, want.Fingerprint, projection.Fingerprint)
	for _, node := range []*instance{nodeA, nodeB} {
		contenders, err := node.service.store.contenders(context.Background(), unitKey)
		require.NoError(t, err)
		winner, _, err := node.service.store.winner(context.Background(), contenders)
		require.NoError(t, err)
		require.Equal(t, want.Fingerprint, winner.Fingerprint)
	}
}

func TestReconcileRejectsTemporarilyAcceptedLoser(t *testing.T) {
	site := newSite(t, "node-a", "node-b")
	nodeA := site.start("node-a")
	nodeB := site.start("node-b")
	first := writeContender(t, nodeA, api.RegistrationRequest{UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "First"}, nodeA.service.location, testTime(10))
	second := writeContender(t, nodeA, api.RegistrationRequest{UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "Second"}, nodeB.service.location, testTime(20))
	writeAcceptance(t, nodeA, second, testTime(21))
	_ = first

	site.reconcile()

	loser := nodeB.get(t, unitKey)
	require.Equal(t, api.RegistrationStatusRejected, loser.Status)
	require.Equal(t, ReasonKeyConflict, *loser.Reason)
	winner := nodeA.get(t, unitKey)
	require.Equal(t, api.RegistrationStatusAccepted, winner.Status)
	require.Equal(t, "First", winner.UnitTypeNameAdvertised)
}

func TestReconciliationOfContendersIsIdempotentAfterRestart(t *testing.T) {
	site := newSite(t, "node-a", "node-b")
	nodeA := site.start("node-a")
	nodeB := site.start("node-b")
	first := writeContender(t, nodeA, api.RegistrationRequest{UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "First"}, nodeA.service.location, testTime(10))
	writeContender(t, nodeA, api.RegistrationRequest{UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "Second"}, nodeB.service.location, testTime(20))
	writeAcceptance(t, nodeA, first, testTime(15))
	site.reconcile()
	before := nodeA.recorder.types()

	restarted := site.restart("node-a")
	site.reconcile()

	projection, found := site.accepted(restarted, unitKey)
	require.True(t, found)
	require.Equal(t, first.Fingerprint, projection.Fingerprint)
	view := restarted.get(t, unitKey)
	require.Equal(t, api.RegistrationStatusAccepted, view.Status)
	require.Equal(t, before, restarted.recorder.types(), "reconciliation did not restate an existing transition")
}

func TestConflictsExposeDeterministicWinnerAndLoserViews(t *testing.T) {
	site := newSite(t, "node-a", "node-b")
	nodeA := site.start("node-a")
	nodeB := site.start("node-b")
	first := writeContender(t, nodeA, api.RegistrationRequest{UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "First"}, nodeA.service.location, testTime(10))
	writeContender(t, nodeA, api.RegistrationRequest{UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "Second"}, nodeB.service.location, testTime(20))
	writeAcceptance(t, nodeA, first, testTime(15))
	site.reconcile()

	conflicts, err := nodeA.service.Conflicts(context.Background())
	require.NoError(t, err)
	require.Len(t, conflicts, 1)
	conflict := conflicts[0]
	require.Equal(t, unitKey.UnitType, conflict.UnitType)
	require.Equal(t, unitKey.UnitID, conflict.UnitID)
	require.Equal(t, api.RegistrationConflictResolutionResolved, conflict.ResolutionStatus)
	require.Equal(t, "First", conflict.Winner.UnitTypeNameAdvertised)
	require.Equal(t, api.RegistrationStatusAccepted, conflict.Winner.Status)
	require.Len(t, conflict.Losers, 1)
	require.Equal(t, "Second", conflict.Losers[0].UnitTypeNameAdvertised)
	require.Equal(t, api.RegistrationStatusRejected, conflict.Losers[0].Status)
	require.Equal(t, ReasonKeyConflict, *conflict.Losers[0].Reason)

	listed := nodeB.list(t)
	require.Len(t, listed, 2)
	require.Equal(t, "First", listed[0].UnitTypeNameAdvertised)
	require.Equal(t, api.RegistrationStatusAccepted, listed[0].Status)
	require.Equal(t, "Second", listed[1].UnitTypeNameAdvertised)
	require.Equal(t, api.RegistrationStatusRejected, listed[1].Status)
}

func TestConflictsAreEmptyWithoutCompetingContenders(t *testing.T) {
	site := newSite(t, "node-a")
	nodeA := site.start("node-a")

	conflicts, err := nodeA.service.Conflicts(context.Background())
	require.NoError(t, err)
	require.Empty(t, conflicts)
}

func writeContender(t *testing.T, node *instance, request api.RegistrationRequest, origin Location, observedAt time.Time) requestRecord {
	t.Helper()
	record := requestRecord{
		Version:                recordVersion,
		UnitType:               request.UnitType,
		UnitID:                 request.UnitID,
		UnitTypeNameAdvertised: request.UnitTypeNameAdvertised,
		Role:                   copyString(request.Role),
		OriginMachine:          origin.Machine,
		OriginIP:               origin.IP,
		ObservedAt:             observedAt.UTC(),
	}
	record.Fingerprint = fingerprintOf(record)
	value, err := encode(record)
	require.NoError(t, err)
	created, err := node.service.store.contenderRecords.Create(context.Background(), contenderKey(record.key(), record.Fingerprint), value)
	require.NoError(t, err)
	require.True(t, created)
	return record
}

func writeAcceptance(t *testing.T, node *instance, request requestRecord, acceptedAt time.Time) {
	t.Helper()
	created, err := node.service.store.createAcceptance(context.Background(), acceptanceRecord{
		Version:     recordVersion,
		UnitType:    request.UnitType,
		UnitID:      request.UnitID,
		Fingerprint: request.Fingerprint,
		AcceptedAt:  acceptedAt.UTC(),
	})
	require.NoError(t, err)
	require.True(t, created)
}

func testTime(second int) time.Time {
	return time.Date(2026, time.July, 15, 12, 0, second, 0, time.UTC)
}

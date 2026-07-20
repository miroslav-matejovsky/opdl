package scenarios

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTwoMachineRegistration is the registration use case as a customer meets
// it: two real platform processes, built from one blueprint, coordinating only
// through their site journal, driven only through REST.
//
// It is the scenario the acceptance barrier exists for. A proposal taken by one
// machine is not registered until every machine the deployment declares has
// confirmed it, so the interesting part is what the platform does while one of
// them is not running: it waits, it says it is waiting, and it says which
// machine it is waiting for. When that machine finally starts, nothing hands it
// the backlog — it finds the proposal in the retained journal and answers.
func TestTwoMachineRegistration(t *testing.T) {
	ctx := t.Context()
	outDir := t.TempDir()
	deployment := deploySite(ctx, t, outDir, t.TempDir(), "two-machine")
	first, second := deployment.machine(t, "node-a"), deployment.machine(t, "node-b")
	const request = `{"unit_type":7,"unit_id":42,"unit_type_name_advertised":"Billing","role":"Master"}`

	// Start node-a alone. node-b is in its topology and is not running.
	first.start(ctx, t)
	waitForAPI(ctx, t, first)

	accepted := propose(ctx, t, first, request)

	// node-a confirms for itself as soon as it handles its own proposal. That is
	// one expected machine of two, so it is not acceptance: the proposal is
	// pending before node-a answers and still pending after, which is why the wait
	// is for node-a's own answer rather than for the overall status.
	pending := waitForRegistration(ctx, t, first, accepted.ProposalID,
		confirmedBy("node-a"), "node-a confirming its own proposal")
	require.Equal(t, "pending", pending.Status,
		"the site cannot accept a proposal node-b has not seen, however long node-a waits")
	require.Equal(t, "pending", pending.instance(t, "node-b").Status,
		"node-b is expected and offline, which is visible rather than silent")
	require.Equal(t, "127.0.0.2", pending.instance(t, "node-b").IP)
	require.Equal(t, "node-a", pending.Machine)
	require.Equal(t, "127.0.0.1", pending.IP)

	require.Equal(t, []registration{pending}, listRegistrations(ctx, t, first),
		"a pending proposal is listed, with the same state the status endpoint reports")

	// Start the machine the site was waiting for. Nothing tells it about the
	// proposal: it replays the retained journal at startup and answers on its own
	// schedule. This is delivery to a node that was not there.
	second.start(ctx, t)
	waitForAPI(ctx, t, second)

	// The client polls the proposal it was handed, which is its only confirmation
	// mechanism.
	done := waitForRegistrationStatus(ctx, t, first, accepted.ProposalID, "accepted")
	require.Equal(t, "accepted", done.instance(t, "node-a").Status)
	require.Equal(t, "accepted", done.instance(t, "node-b").Status)
	require.Equal(t, "node-a", done.Machine, "the origin is a fact of the proposal, and does not move")
	require.Equal(t, "127.0.0.1", done.IP)
	require.Equal(t, "Billing", done.UnitTypeNameAdvertised)
	require.Equal(t, "Master", *done.Role)

	// Both machines report the same registration: the list is the site's state,
	// folded from one journal by each machine independently.
	require.Equal(t, []registration{done}, listRegistrations(ctx, t, first))
	require.Equal(t, []registration{done}, listRegistrations(ctx, t, second))

	// A proposal's status is the same answer wherever it is asked. The client is
	// no longer tied to the machine it posted to.
	fromSecond := waitForRegistrationStatus(ctx, t, second, accepted.ProposalID, "accepted")
	require.Equal(t, done, fromSecond)

	// An exact retry is the same claim, answered the same way, changing nothing.
	retry := propose(ctx, t, first, request)
	require.Equal(t, accepted.ProposalID, retry.ProposalID)
	require.Equal(t, []registration{done}, listRegistrations(ctx, t, first),
		"an exact retry is not a second registration")

	// The same key with a different advertised name is a different claim, from the
	// very machine that holds the key. The POST cannot refuse it: at the moment
	// the journal takes it, nothing has decided anything.
	contender := propose(ctx, t, first, `{"unit_type":7,"unit_id":42,"unit_type_name_advertised":"Payments","role":"Master"}`)
	require.NotEqual(t, accepted.ProposalID, contender.ProposalID)
	require.Greater(t, contender.Sequence, accepted.Sequence, "the journal ordered the claims")

	refused := waitForRegistrationStatus(ctx, t, first, contender.ProposalID, "rejected")
	require.Equal(t, "registration_key_conflict", *refused.Reason,
		"the first claim in journal order keeps the key")

	// The same key from the other machine is a different claim too: the key is the
	// site's, not a machine's.
	crossMachine := propose(ctx, t, second, request)
	require.NotEqual(t, accepted.ProposalID, crossMachine.ProposalID,
		"a different origin is a different proposal, even for an identical request")
	refusedOnB := waitForRegistrationStatus(ctx, t, second, crossMachine.ProposalID, "rejected")
	require.Equal(t, "registration_key_conflict", *refusedOnB.Reason)

	// Every losing claim left the accepted registration exactly as it was.
	after, code := getRegistration(ctx, t, first, accepted.ProposalID)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, done, after, "a losing claim preserves the accepted registration")

	// Both machines resolve the conflict the same way, because journal order is
	// the site's order rather than each machine's own view of when things arrived.
	for _, m := range []*machine{first, second} {
		conflicts := listConflicts(ctx, t, m)
		require.Len(t, conflicts, 1, "%s reported the wrong number of conflicts", m.name)
		require.Equal(t, "resolved", conflicts[0].ResolutionStatus)
		require.Equal(t, uint8(7), conflicts[0].UnitType)
		require.Equal(t, uint16(42), conflicts[0].UnitID)
		require.Equal(t, accepted.ProposalID, conflicts[0].Winner.ProposalID,
			"%s named a different winner", m.name)

		losers := make([]string, 0, len(conflicts[0].Losers))
		for _, loser := range conflicts[0].Losers {
			losers = append(losers, loser.ProposalID)
		}
		require.ElementsMatch(t, []string{contender.ProposalID, crossMachine.ProposalID}, losers,
			"%s did not report both losing claims", m.name)
	}
}

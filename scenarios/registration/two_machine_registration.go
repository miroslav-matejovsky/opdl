package registration

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/scenarios/internal/harness"
)

// TwoMachineRegistration is the registration use case as a customer meets
// it: two real platform processes, built from one blueprint, coordinating only
// through their site journal, driven only through REST.
//
// It is the scenario the acceptance barrier exists for. A proposal taken by one
// machine is not registered until every machine the deployment declares has
// confirmed it, so the interesting part is what the platform does while one of
// them is not running: it waits, it says it is waiting, and it says which
// machine it is waiting for. When that machine finally starts, nothing hands it
// the backlog — it finds the proposal in the retained journal and answers.
func TwoMachineRegistration(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	outDir := filepath.Join(harness.ScenarioDir(t), "out")
	deployment := harness.DeploySite(ctx, t, outDir, filepath.Join(harness.ScenarioDir(t), "work"), "two-machine")
	first, second := deployment.Machine(t, "node-a"), deployment.Machine(t, "node-c")
	const request = `{"unit_type":7,"unit_id":42,"unit_type_name_advertised":"Billing","role":"Master"}`

	// Start the site's storage instances. node-c is in the topology and is not
	// running: it stores nothing, so the journal is complete without it while the
	// site's membership is not.
	deployment.StartSite(ctx, t, "node-c")

	accepted := harness.Propose(ctx, t, first, request)

	// node-a confirms for itself as soon as it handles its own proposal. That is
	// one expected machine of two, so it is not acceptance: the proposal is
	// pending before node-a answers and still pending after, which is why the wait
	// is for node-a's own answer rather than for the overall status.
	pending := harness.WaitForRegistration(ctx, t, first, accepted.ProposalID,
		harness.ConfirmedBy("node-a"), "node-a confirming its own proposal")
	require.Equal(t, "pending", pending.Status,
		"the site cannot accept a proposal node-c has not seen, however long node-a waits")
	require.Equal(t, "pending", pending.Instance(t, "node-c").Status,
		"node-c is expected and offline, which is visible rather than silent")
	require.Equal(t, "127.0.0.3", pending.Instance(t, "node-c").IP)
	require.Equal(t, "node-a", pending.Machine)
	require.Equal(t, "127.0.0.1", pending.IP)

	require.Equal(t, []harness.Registration{pending}, harness.ListRegistrations(ctx, t, first),
		"a pending proposal is listed, with the same state the status endpoint reports")

	// Start the machine the site was waiting for. Nothing tells it about the
	// proposal: it replays the retained journal at startup and answers on its own
	// schedule. This is delivery to a node that was not there.
	second.Start(ctx, t)
	harness.WaitForAPI(ctx, t, second)

	// The client polls the proposal it was handed, which is its only confirmation
	// mechanism.
	done := harness.WaitForRegistrationStatus(ctx, t, first, accepted.ProposalID, "accepted")
	require.Equal(t, "accepted", done.Instance(t, "node-a").Status)
	require.Equal(t, "accepted", done.Instance(t, "node-b").Status)
	require.Equal(t, "node-a", done.Machine, "the origin is a fact of the proposal, and does not move")
	require.Equal(t, "127.0.0.1", done.IP)
	require.Equal(t, "Billing", done.UnitTypeNameAdvertised)
	require.Equal(t, "Master", *done.Role)

	// Both machines report the same registration: the list is the site's state,
	// folded from one journal by each machine independently.
	require.Equal(t, []harness.Registration{done}, harness.ListRegistrations(ctx, t, first))
	require.Equal(t, []harness.Registration{done}, harness.ListRegistrations(ctx, t, second))

	// A proposal's status is the same answer wherever it is asked. The client is
	// no longer tied to the machine it posted to.
	fromSecond := harness.WaitForRegistrationStatus(ctx, t, second, accepted.ProposalID, "accepted")
	require.Equal(t, done, fromSecond)

	// An exact retry is the same claim, answered the same way, changing nothing.
	retry := harness.Propose(ctx, t, first, request)
	require.Equal(t, accepted.ProposalID, retry.ProposalID)
	require.Equal(t, []harness.Registration{done}, harness.ListRegistrations(ctx, t, first),
		"an exact retry is not a second registration")

	// The same key with a different advertised name is a different claim, from the
	// very machine that holds the key. The POST cannot refuse it: at the moment
	// the journal takes it, nothing has decided anything.
	contender := harness.Propose(ctx, t, first, `{"unit_type":7,"unit_id":42,"unit_type_name_advertised":"Payments","role":"Master"}`)
	require.NotEqual(t, accepted.ProposalID, contender.ProposalID)
	require.Greater(t, contender.Sequence, accepted.Sequence, "the journal ordered the claims")

	refused := harness.WaitForRegistrationStatus(ctx, t, first, contender.ProposalID, "rejected")
	require.Equal(t, "registration_key_conflict", *refused.Reason,
		"the first claim in journal order keeps the key")

	// The same key from the other machine is a different claim too: the key is the
	// site's, not a machine's.
	crossMachine := harness.Propose(ctx, t, second, request)
	require.NotEqual(t, accepted.ProposalID, crossMachine.ProposalID,
		"a different origin is a different proposal, even for an identical request")
	refusedOnB := harness.WaitForRegistrationStatus(ctx, t, second, crossMachine.ProposalID, "rejected")
	require.Equal(t, "registration_key_conflict", *refusedOnB.Reason)

	// Every losing claim left the accepted registration exactly as it was.
	after, code := harness.GetRegistration(ctx, t, first, accepted.ProposalID)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, done, after, "a losing claim preserves the accepted registration")

	// Both machines resolve the conflict the same way, because journal order is
	// the site's order rather than each machine's own view of when things arrived.
	for _, m := range []*harness.Machine{first, second} {
		conflicts := harness.ListConflicts(ctx, t, m)
		require.Len(t, conflicts, 1, "%s reported the wrong number of conflicts", m.Name)
		require.Equal(t, "resolved", conflicts[0].ResolutionStatus)
		require.Equal(t, uint8(7), conflicts[0].UnitType)
		require.Equal(t, uint16(42), conflicts[0].UnitID)
		require.Equal(t, accepted.ProposalID, conflicts[0].Winner.ProposalID,
			"%s named a different winner", m.Name)

		losers := make([]string, 0, len(conflicts[0].Losers))
		for _, loser := range conflicts[0].Losers {
			losers = append(losers, loser.ProposalID)
		}
		require.ElementsMatch(t, []string{contender.ProposalID, crossMachine.ProposalID}, losers,
			"%s did not report both losing claims", m.Name)
	}
}

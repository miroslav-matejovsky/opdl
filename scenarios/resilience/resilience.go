package resilience

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/scenarios/internal/harness"
)

// This file holds the scenarios about a platform that is interrupted or cannot
// start. They are the reason the site journal is on disk and the reason startup
// is ordered, so they are worth driving from outside the process, where a
// promise about restart and refusal is the only thing a customer can actually
// rely on.

// RestartRebuildsStateFromTheJournal is the promise durable storage exists
// for: a platform's state is not in the process. Kill it, start it again, and it
// answers the same questions the same way, because it rebuilt its projection by
// replaying the journal it had already written.
//
// The kill is deliberate. An orderly shutdown proves less: what has to survive
// is a machine that stopped without warning, which is the case a customer meets.
func RestartRebuildsStateFromTheJournal(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	outDir := filepath.Join(harness.ScenarioDir(t), "out")
	deployment := harness.DeploySite(ctx, t, outDir, filepath.Join(harness.ScenarioDir(t), "work"), "scenario")
	node := deployment.Machine(t, "node-a")
	// Both machines: a proposal needs a confirmation from every machine of the
	// site, and the journal's metadata group needs a quorum before either can
	// finish starting.
	deployment.StartSite(ctx, t)

	accepted := harness.Propose(ctx, t, node, `{"unit_type":7,"unit_id":42,"unit_type_name_advertised":"Billing","role":"Master"}`)
	before := harness.WaitForRegistrationStatus(ctx, t, node, accepted.ProposalID, "accepted")
	listedBefore := harness.ListRegistrations(ctx, t, node)

	// Nothing is handed to the new process but the same configuration and the same
	// directory on disk.
	node.Restart(ctx, t)
	harness.WaitForAPI(ctx, t, node)

	after, code := harness.GetRegistration(ctx, t, node, accepted.ProposalID)
	require.Equal(t, http.StatusOK, code,
		"the restarted platform does not know a registration it had already accepted")
	require.Equal(t, before, after, "a restarted platform answers the same question the same way")
	require.Equal(t, listedBefore, harness.ListRegistrations(ctx, t, node))

	// The state came back by replay, not by a second decision. An accepted
	// proposal that was re-decided would be a new fact in the journal; the
	// identity is the same, so the site sees the same registration it already had.
	retry := harness.Propose(ctx, t, node, `{"unit_type":7,"unit_id":42,"unit_type_name_advertised":"Billing","role":"Master"}`)
	require.Equal(t, accepted.ProposalID, retry.ProposalID,
		"the same request is the same claim across a restart: identity is derived, not remembered")
	require.Len(t, harness.ListRegistrations(ctx, t, node), 1)
	require.Empty(t, harness.ListConflicts(ctx, t, node),
		"a restart must not make a platform contend with its own history")
}

// PlatformRefusesToStartWithoutItsJournalStorage checks a node fails loudly
// and early rather than serving without the thing it answers from.
//
// The journal is the site's history. A platform that could not store it and
// started anyway would answer every query with an empty projection and take
// proposals it could never retain, which is worse than not starting: it would
// look healthy while losing facts.
func PlatformRefusesToStartWithoutItsJournalStorage(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	outDir := filepath.Join(harness.ScenarioDir(t), "out")
	workDir := filepath.Join(harness.ScenarioDir(t), "work")
	deployment := harness.DeploySite(ctx, t, outDir, workDir, "scenario")
	node := deployment.Machine(t, "node-a")

	// Put a file where the journal's directory has to be, so creating it cannot
	// succeed. This is a stand-in for the real cases: no permission, or a full or
	// unmounted disk.
	require.NoError(t, os.MkdirAll(filepath.Dir(node.Sockets.JetStreamStoreDir), 0o755))
	require.NoError(t, os.WriteFile(node.Sockets.JetStreamStoreDir, []byte("not a directory"), 0o644))

	node.Start(ctx, t)
	output := node.AwaitExit(t)

	require.Contains(t, output, "data directory",
		"a platform that cannot store its journal must say so:\n%s", output)
	// Listening is no longer the same fact as serving. Every instance binds its
	// API for its whole lifetime and answers about itself while Passive, so this
	// process does print "listening on" before it ever tries to open the journal.
	// What it must never do is activate: becoming Active is what would let it
	// answer domain operations from a journal it could not store.
	require.NotContains(t, output, "active, serving on",
		"a platform that cannot store its journal must not serve:\n%s", output)
	require.NotContains(t, output, "platform.app.api_active",
		"a platform that cannot store its journal must not activate its API:\n%s", output)

	// The API never came up, so there is nothing to answer a client that tries.
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, node.URL+"/registrations", http.NoBody)
	require.NoError(t, err)
	_, err = http.DefaultClient.Do(request)
	require.Error(t, err, "the platform served its API despite failing to start")
}

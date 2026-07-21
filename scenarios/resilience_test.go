package scenarios

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// This file holds the scenarios about a platform that is interrupted or cannot
// start. They are the reason the site journal is on disk and the reason startup
// is ordered, so they are worth driving from outside the process, where a
// promise about restart and refusal is the only thing a customer can actually
// rely on.

// TestRestartRebuildsStateFromTheJournal is the promise durable storage exists
// for: a platform's state is not in the process. Kill it, start it again, and it
// answers the same questions the same way, because it rebuilt its projection by
// replaying the journal it had already written.
//
// The kill is deliberate. An orderly shutdown proves less: what has to survive
// is a machine that stopped without warning, which is the case a customer meets.
func TestRestartRebuildsStateFromTheJournal(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	outDir := filepath.Join(scenarioDir(t), "out")
	deployment := deploySite(ctx, t, outDir, filepath.Join(scenarioDir(t), "work"), "scenario")
	node := deployment.machine(t, "node")
	node.start(ctx, t)
	waitForAPI(ctx, t, node)

	accepted := propose(ctx, t, node, `{"unit_type":7,"unit_id":42,"unit_type_name_advertised":"Billing","role":"Master"}`)
	before := waitForRegistrationStatus(ctx, t, node, accepted.ProposalID, "accepted")
	listedBefore := listRegistrations(ctx, t, node)

	// Nothing is handed to the new process but the same configuration and the same
	// directory on disk.
	node.restart(ctx, t)
	waitForAPI(ctx, t, node)

	after, code := getRegistration(ctx, t, node, accepted.ProposalID)
	require.Equal(t, http.StatusOK, code,
		"the restarted platform does not know a registration it had already accepted")
	require.Equal(t, before, after, "a restarted platform answers the same question the same way")
	require.Equal(t, listedBefore, listRegistrations(ctx, t, node))

	// The state came back by replay, not by a second decision. An accepted
	// proposal that was re-decided would be a new fact in the journal; the
	// identity is the same, so the site sees the same registration it already had.
	retry := propose(ctx, t, node, `{"unit_type":7,"unit_id":42,"unit_type_name_advertised":"Billing","role":"Master"}`)
	require.Equal(t, accepted.ProposalID, retry.ProposalID,
		"the same request is the same claim across a restart: identity is derived, not remembered")
	require.Len(t, listRegistrations(ctx, t, node), 1)
	require.Empty(t, listConflicts(ctx, t, node),
		"a restart must not make a platform contend with its own history")
}

// TestPlatformRefusesToStartWithoutItsJournalStorage checks a node fails loudly
// and early rather than serving without the thing it answers from.
//
// The journal is the site's history. A platform that could not store it and
// started anyway would answer every query with an empty projection and take
// proposals it could never retain, which is worse than not starting: it would
// look healthy while losing facts.
func TestPlatformRefusesToStartWithoutItsJournalStorage(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	outDir := filepath.Join(scenarioDir(t), "out")
	workDir := filepath.Join(scenarioDir(t), "work")
	deployment := deploySite(ctx, t, outDir, workDir, "scenario")
	node := deployment.machine(t, "node")

	// Put a file where the journal's directory has to be, so creating it cannot
	// succeed. This is a stand-in for the real cases: no permission, or a full or
	// unmounted disk.
	require.NoError(t, os.MkdirAll(filepath.Dir(node.sockets.dataDir), 0o755))
	require.NoError(t, os.WriteFile(node.sockets.dataDir, []byte("not a directory"), 0o644))

	node.start(ctx, t)
	output := node.wait(t)

	require.Contains(t, output, "data directory",
		"a platform that cannot store its journal must say so:\n%s", output)
	// Listening is no longer the same fact as serving. Every instance binds its
	// API for its whole lifetime and answers about itself while Passive, so this
	// process does print "listening on" before it ever tries to open the journal.
	// What it must never do is activate: becoming Active is what would let it
	// answer domain operations from a journal it could not store.
	require.NotContains(t, output, "active, serving on",
		"a platform that cannot store its journal must not serve:\n%s", output)
	require.NotContains(t, output, "platform.api_active",
		"a platform that cannot store its journal must not activate its API:\n%s", output)

	// The API never came up, so there is nothing to answer a client that tries.
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, node.url+"/registrations", http.NoBody)
	require.NoError(t, err)
	_, err = http.DefaultClient.Do(request)
	require.Error(t, err, "the platform served its API despite failing to start")
}

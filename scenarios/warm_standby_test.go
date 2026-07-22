package scenarios

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/scenarios/internal/harness"
	"github.com/miroslav-matejovsky/opdl/scenarios/internal/processinfo"
)

// TestWarmStandbyFailoverAndPreferredPrimary drives the complete redundant
// process lifecycle from a built package. Status files are operational evidence;
// registration assertions stay on the public API.
func TestWarmStandbyFailoverAndPreferredPrimary(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	outDir := filepath.Join(harness.ScenarioDir(t), "out")
	deployment := harness.DeploySite(ctx, t, outDir, filepath.Join(harness.ScenarioDir(t), "work"), "manifest-contract")
	node := deployment.Machine(t, "node-b")
	// node-a carries the site's third storage instance. This scenario starts
	// node-b's two instances itself, from the manifest, so the rest of the site is
	// brought up here: without it the journal has no quorum to be created in.
	deployment.StartSite(ctx, t, "node-b")
	manifest := harness.ReadManifest(t, node.BinaryPath)
	require.NotNil(t, manifest.Standby, "default policy must package a standby launch")

	primary := node.StartManaged(ctx, t, "primary", manifest.Primary.Args)
	node.WaitStatus(t, primary, "active", false)
	waitForManagedAPI(ctx, t, node, primary)

	stable := harness.Propose(ctx, t, node,
		`{"unit_type":7,"unit_id":41,"unit_type_name_advertised":"Before failover","role":"Master"}`)
	stableView := harness.WaitForRegistrationStatus(ctx, t, node, stable.ProposalID, "accepted")

	standbyStarted := time.Now()
	standby := node.StartManaged(ctx, t, "standby", manifest.Standby.Args)
	standbyStatus := node.WaitStatus(t, standby, "passive", true)
	catchUpTime := standbyStatus.UpdatedAt.Sub(standbyStarted)
	standbyMemory, err := processinfo.ResidentBytes(ctx, standby.PID())
	require.NoError(t, err)

	aroundFailover := harness.Propose(ctx, t, node,
		`{"unit_type":7,"unit_id":42,"unit_type_name_advertised":"During failover","role":"Master"}`)
	gapReady, listenerGap := observeListenerGap(ctx, node.URL)
	select {
	case <-gapReady:
	case <-time.After(harness.APIWaitTimeout):
		t.Fatal("listener observer did not see the active API")
	}
	failoverStarted := time.Now()
	_ = primary.Kill()
	failedOverStatus := node.WaitStatus(t, standby, "active", false)
	failoverTime := failedOverStatus.UpdatedAt.Sub(failoverStarted)
	waitForManagedAPI(ctx, t, node, standby)
	var gap time.Duration
	select {
	case gap = <-listenerGap:
	case <-time.After(harness.APIWaitTimeout):
		t.Fatal("listener observer did not see the API recover")
	}

	require.Equal(t, stableView,
		harness.WaitForRegistrationStatus(ctx, t, node, stable.ProposalID, "accepted"),
		"failover changed a registration retained before it")
	failoverView := harness.WaitForRegistrationStatus(ctx, t, node, aroundFailover.ProposalID, "accepted")
	require.Len(t, failoverView.PlatformInstances, 1,
		"primary and standby must remain one machine decision")
	require.Len(t, harness.ListRegistrations(ctx, t, node), 2,
		"failover must not duplicate a logical proposal")
	retry := harness.Propose(ctx, t, node,
		`{"unit_type":7,"unit_id":42,"unit_type_name_advertised":"During failover","role":"Master"}`)
	require.Equal(t, aroundFailover.ProposalID, retry.ProposalID)
	require.Len(t, harness.ListRegistrations(ctx, t, node), 2)

	primaryReturned := node.StartManaged(ctx, t, "primary", manifest.Primary.Args)
	node.WaitStatus(t, primaryReturned, "passive", true)
	failbackStarted := time.Now()
	standby.StopGracefully(t)
	node.WaitStatus(t, primaryReturned, "active", false)
	waitForManagedAPI(ctx, t, node, primaryReturned)
	failbackTime := time.Since(failbackStarted)

	// Repeat failover and failback to expose stale status, ownership, listener, or
	// storage ownership left behind by the first transfer.
	standbySecond := node.StartManaged(ctx, t, "standby", manifest.Standby.Args)
	node.WaitStatus(t, standbySecond, "passive", true)
	_ = primaryReturned.Kill()
	node.WaitStatus(t, standbySecond, "active", false)
	waitForManagedAPI(ctx, t, node, standbySecond)

	primarySecond := node.StartManaged(ctx, t, "primary", manifest.Primary.Args)
	node.WaitStatus(t, primarySecond, "passive", true)
	standbySecond.StopGracefully(t)
	node.WaitStatus(t, primarySecond, "active", false)
	waitForManagedAPI(ctx, t, node, primarySecond)

	// Terminating a caught-up lock waiter must stop only that process. Context
	// cancellation of Lock.Acquire is covered by the platform contract tests.
	cancelledStandby := node.StartManaged(ctx, t, "standby", manifest.Standby.Args)
	node.WaitStatus(t, cancelledStandby, "passive", true)
	_ = cancelledStandby.Kill()
	require.True(t, primarySecond.Running())
	waitForManagedAPI(ctx, t, node, primarySecond)

	// Full machine shutdown is primary-service stop followed by standby-service
	// stop. The standby may become Active in the bounded interval and must still stop.
	shutdownStandby := node.StartManaged(ctx, t, "standby", manifest.Standby.Args)
	node.WaitStatus(t, shutdownStandby, "passive", true)
	primarySecond.StopGracefully(t)
	node.WaitStatus(t, shutdownStandby, "active", false)
	shutdownStandby.StopGracefully(t)
	require.False(t, primarySecond.Running())
	require.False(t, shutdownStandby.Running())

	// Every process of this machine has now exited, so its output is complete and
	// safe to read.
	assertSharedEndpoints(t, primary, standby)

	t.Logf("warm standby baseline: catch-up=%s failover=%s listener-unavailable=%s failback=%s standby-working-set=%d bytes",
		catchUpTime, failoverTime, gap, failbackTime, standbyMemory)
}

// assertSharedEndpoints is the regression for the warm standby connection
// failure.
//
// The defect was that the standby derived its own NATS configuration and got a
// client address the active process was not serving on. Nothing was listening
// there, so the standby retried forever and never became failover-ready, while every
// log line about it looked ordinary. These assertions pin the property that makes
// that impossible: the machine has one endpoint set, the standby connects to the
// one the active process is serving on, and promotion rebinds that same address
// rather than moving the site onto a second one.
func assertSharedEndpoints(t *testing.T, primary, standby *harness.ManagedProcess) {
	t.Helper()

	primaryFabrics := harness.ParseFabricConfigs(t, primary.Role, primary.Logs())
	standbyFabrics := harness.ParseFabricConfigs(t, standby.Role, standby.Logs())
	require.NotEmpty(t, primaryFabrics)
	require.GreaterOrEqual(t, len(standbyFabrics), 2,
		"the standby should report a client-only composition and then an active one after promotion:\n%s",
		standby.Logs())

	active := primaryFabrics[0]
	require.True(t, active.Storage, "the primary owns the machine's storage:\n%s", primary.Logs())
	require.True(t, active.Binds, "the primary binds the machine's NATS listener:\n%s", primary.Logs())
	require.NotEmpty(t, active.Endpoint)

	// The standby binds nothing while the primary holds Primary Ownership.
	warm := standbyFabrics[0]
	require.False(t, warm.Storage, "the standby must not open the journal store:\n%s", standby.Logs())
	require.False(t, warm.Binds, "the standby must bind no NATS listener:\n%s", standby.Logs())
	require.Equal(t, "none", warm.Cluster, "the standby must bind no cluster listener:\n%s", standby.Logs())

	// It is nonetheless talking about the same machine endpoint, and reaches the
	// journal through exactly the server list the active process is serving. This
	// is the assertion the original defect would fail: the standby had its own
	// address, and nothing was listening on it.
	require.Equal(t, active.Endpoint, warm.Endpoint,
		"the standby composed a different machine endpoint from the active process")
	require.Equal(t, active.Servers, warm.Servers,
		"the standby used a different server list from the active process")
	require.Contains(t, warm.Servers, warm.Endpoint,
		"a local standby follows the journal through its own machine's server")

	// After promotion it owns the same endpoints the primary had, so nothing the
	// rest of the site was told about this machine has changed.
	promoted := standbyFabrics[len(standbyFabrics)-1]
	require.True(t, promoted.Storage, "the promoted process must own storage:\n%s", standby.Logs())
	require.True(t, promoted.Binds, "the promoted process must bind the listener:\n%s", standby.Logs())
	require.Equal(t, active.Endpoint, promoted.Endpoint,
		"promotion moved the machine's client endpoint")
	require.Equal(t, active.Cluster, promoted.Cluster,
		"promotion moved the machine's cluster endpoint")
	require.Equal(t, active.Servers, promoted.Servers,
		"promotion changed the machine's server list")
}

func waitForManagedAPI(ctx context.Context, t *testing.T, machine *harness.Machine, process *harness.ManagedProcess) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	cond := func() bool {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, machine.URL+"/registrations", http.NoBody)
		if err != nil {
			return false
		}
		response, err := client.Do(request)
		if err != nil {
			return false
		}
		_ = response.Body.Close()
		return response.StatusCode == http.StatusOK
	}
	abort := func() (bool, string) {
		if !process.Running() {
			out, _ := process.Wait()
			return true, fmt.Sprintf("%s process exited before restoring the API:\n%s", process.Role, out)
		}
		return false, ""
	}
	diag := harness.DiagStringer(func() string {
		return fmt.Sprintf("%s process did not restore the API:\n%s", process.Role, process.Logs())
	})
	harness.WaitFor(t, process.Role+" restoring the API", harness.APIWaitTimeout, harness.APIPollInterval, cond, abort, diag)
}

// observeListenerGap returns the last-success to first-recovery interval around
// a listener outage. ready closes after the initial listener has answered.
func observeListenerGap(ctx context.Context, url string) (ready <-chan struct{}, gap <-chan time.Duration) {
	readyChannel := make(chan struct{})
	done := make(chan time.Duration, 1)
	go func() {
		client := &http.Client{Timeout: time.Second}
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		var lastAvailable time.Time
		unavailable := false
		readyClosed := false
		for {
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"/registrations", http.NoBody)
			available := false
			if err == nil {
				response, requestErr := client.Do(request)
				if requestErr == nil {
					available = response.StatusCode == http.StatusOK
					_ = response.Body.Close()
				}
			}
			now := time.Now()
			if available {
				if unavailable {
					done <- now.Sub(lastAvailable)
					return
				}
				lastAvailable = now
				if !readyClosed {
					close(readyChannel)
					readyClosed = true
				}
			} else if readyClosed {
				unavailable = true
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return readyChannel, done
}

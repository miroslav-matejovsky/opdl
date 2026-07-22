package scenarios

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/miroslav-matejovsky/opdl/utils/logscan"
	"github.com/miroslav-matejovsky/opdl/utils/processinfo"
	"github.com/stretchr/testify/require"
)

// TestWarmStandbyFailoverAndPreferredPrimary drives the complete redundant
// process lifecycle from a built package. Status files are operational evidence;
// registration assertions stay on the public API.
func TestWarmStandbyFailoverAndPreferredPrimary(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	outDir := filepath.Join(scenarioDir(t), "out")
	deployment := deploySite(ctx, t, outDir, filepath.Join(scenarioDir(t), "work"), "manifest-contract")
	node := deployment.machine(t, "node-b")
	// node-a carries the site's third storage instance. This scenario starts
	// node-b's two instances itself, from the manifest, so the rest of the site is
	// brought up here: without it the journal has no quorum to be created in.
	deployment.startSite(ctx, t, "node-b")
	manifest := readManifest(t, node.binaryPath)
	require.NotNil(t, manifest.Standby, "default policy must package a standby launch")

	primary := node.startManaged(ctx, t, "primary", manifest.Primary.Args)
	node.waitStatus(t, primary, "active", false)
	waitForManagedAPI(ctx, t, node, primary)

	stable := propose(ctx, t, node,
		`{"unit_type":7,"unit_id":41,"unit_type_name_advertised":"Before failover","role":"Master"}`)
	stableView := waitForRegistrationStatus(ctx, t, node, stable.ProposalID, "accepted")

	standbyStarted := time.Now()
	standby := node.startManaged(ctx, t, "standby", manifest.Standby.Args)
	standbyStatus := node.waitStatus(t, standby, "passive", true)
	catchUpTime := standbyStatus.UpdatedAt.Sub(standbyStarted)
	standbyMemory, err := processinfo.ResidentBytes(ctx, standby.PID())
	require.NoError(t, err)

	aroundFailover := propose(ctx, t, node,
		`{"unit_type":7,"unit_id":42,"unit_type_name_advertised":"During failover","role":"Master"}`)
	gapReady, listenerGap := observeListenerGap(ctx, node.url)
	select {
	case <-gapReady:
	case <-time.After(apiWaitTimeout):
		t.Fatal("listener observer did not see the active API")
	}
	failoverStarted := time.Now()
	_ = primary.Kill()
	failedOverStatus := node.waitStatus(t, standby, "active", false)
	failoverTime := failedOverStatus.UpdatedAt.Sub(failoverStarted)
	waitForManagedAPI(ctx, t, node, standby)
	var gap time.Duration
	select {
	case gap = <-listenerGap:
	case <-time.After(apiWaitTimeout):
		t.Fatal("listener observer did not see the API recover")
	}

	require.Equal(t, stableView,
		waitForRegistrationStatus(ctx, t, node, stable.ProposalID, "accepted"),
		"failover changed a registration retained before it")
	failoverView := waitForRegistrationStatus(ctx, t, node, aroundFailover.ProposalID, "accepted")
	require.Len(t, failoverView.PlatformInstances, 1,
		"primary and standby must remain one machine decision")
	require.Len(t, listRegistrations(ctx, t, node), 2,
		"failover must not duplicate a logical proposal")
	retry := propose(ctx, t, node,
		`{"unit_type":7,"unit_id":42,"unit_type_name_advertised":"During failover","role":"Master"}`)
	require.Equal(t, aroundFailover.ProposalID, retry.ProposalID)
	require.Len(t, listRegistrations(ctx, t, node), 2)

	primaryReturned := node.startManaged(ctx, t, "primary", manifest.Primary.Args)
	node.waitStatus(t, primaryReturned, "passive", true)
	failbackStarted := time.Now()
	standby.stopGracefully(t)
	node.waitStatus(t, primaryReturned, "active", false)
	waitForManagedAPI(ctx, t, node, primaryReturned)
	failbackTime := time.Since(failbackStarted)

	// Repeat failover and failback to expose stale status, ownership, listener, or
	// storage ownership left behind by the first transfer.
	standbySecond := node.startManaged(ctx, t, "standby", manifest.Standby.Args)
	node.waitStatus(t, standbySecond, "passive", true)
	_ = primaryReturned.Kill()
	node.waitStatus(t, standbySecond, "active", false)
	waitForManagedAPI(ctx, t, node, standbySecond)

	primarySecond := node.startManaged(ctx, t, "primary", manifest.Primary.Args)
	node.waitStatus(t, primarySecond, "passive", true)
	standbySecond.stopGracefully(t)
	node.waitStatus(t, primarySecond, "active", false)
	waitForManagedAPI(ctx, t, node, primarySecond)

	// Terminating a caught-up lock waiter must stop only that process. Context
	// cancellation of Lock.Acquire is covered by the platform contract tests.
	cancelledStandby := node.startManaged(ctx, t, "standby", manifest.Standby.Args)
	node.waitStatus(t, cancelledStandby, "passive", true)
	_ = cancelledStandby.Kill()
	require.True(t, primarySecond.Running())
	waitForManagedAPI(ctx, t, node, primarySecond)

	// Full machine shutdown is primary-service stop followed by standby-service
	// stop. The standby may become Active in the bounded interval and must still stop.
	shutdownStandby := node.startManaged(ctx, t, "standby", manifest.Standby.Args)
	node.waitStatus(t, shutdownStandby, "passive", true)
	primarySecond.stopGracefully(t)
	node.waitStatus(t, shutdownStandby, "active", false)
	shutdownStandby.stopGracefully(t)
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
func assertSharedEndpoints(t *testing.T, primary, standby *managedProcess) {
	t.Helper()

	primaryFabrics := parseFabricConfigs(t, primary.role, primary.Logs())
	standbyFabrics := parseFabricConfigs(t, standby.role, standby.Logs())
	require.NotEmpty(t, primaryFabrics)
	require.GreaterOrEqual(t, len(standbyFabrics), 2,
		"the standby should report a client-only composition and then an active one after promotion:\n%s",
		standby.Logs())

	active := primaryFabrics[0]
	require.True(t, active.storage, "the primary owns the machine's storage:\n%s", primary.Logs())
	require.True(t, active.binds, "the primary binds the machine's NATS listener:\n%s", primary.Logs())
	require.NotEmpty(t, active.endpoint)

	// The standby binds nothing while the primary holds Primary Ownership.
	warm := standbyFabrics[0]
	require.False(t, warm.storage, "the standby must not open the journal store:\n%s", standby.Logs())
	require.False(t, warm.binds, "the standby must bind no NATS listener:\n%s", standby.Logs())
	require.Equal(t, "none", warm.cluster, "the standby must bind no cluster listener:\n%s", standby.Logs())

	// It is nonetheless talking about the same machine endpoint, and reaches the
	// journal through exactly the server list the active process is serving. This
	// is the assertion the original defect would fail: the standby had its own
	// address, and nothing was listening on it.
	require.Equal(t, active.endpoint, warm.endpoint,
		"the standby composed a different machine endpoint from the active process")
	require.Equal(t, active.servers, warm.servers,
		"the standby used a different server list from the active process")
	require.Contains(t, warm.servers, warm.endpoint,
		"a local standby follows the journal through its own machine's server")

	// After promotion it owns the same endpoints the primary had, so nothing the
	// rest of the site was told about this machine has changed.
	promoted := standbyFabrics[len(standbyFabrics)-1]
	require.True(t, promoted.storage, "the promoted process must own storage:\n%s", standby.Logs())
	require.True(t, promoted.binds, "the promoted process must bind the listener:\n%s", standby.Logs())
	require.Equal(t, active.endpoint, promoted.endpoint,
		"promotion moved the machine's client endpoint")
	require.Equal(t, active.cluster, promoted.cluster,
		"promotion moved the machine's cluster endpoint")
	require.Equal(t, active.servers, promoted.servers,
		"promotion changed the machine's server list")
}

// fabricConfig is one effective Event Fabric configuration a process reported at
// startup, before it bound or connected anything.
type fabricConfig struct {
	// endpoint is the machine's own client address from the descriptor. It is
	// reported whether or not this process binds it, which is what lets a standby
	// and the active process it follows be compared.
	endpoint string
	// binds reports whether this process opens the machine's NATS listener.
	binds   bool
	cluster string
	servers string
	routes  string
	storage bool
}

// fabricConfigPrefix is the runtime's effective-configuration log line.
const fabricConfigPrefix = "platform: event fabric configuration "

// parseFabricConfigs extracts every effective Event Fabric configuration a
// process reported, in the order it reported them.
//
// A scenario reads this from the process output rather than from a status file
// because it is the only place the composed endpoints appear before anything is
// bound. That ordering is what distinguishes a client-only standby from an
// active storage server, and a promotion from a fresh start.
func parseFabricConfigs(t *testing.T, role, logs string) []fabricConfig {
	t.Helper()
	var configs []fabricConfig
	for _, fields := range logscan.Fields(logs, fabricConfigPrefix) {
		configs = append(configs, fabricConfig{
			endpoint: fields["endpoint"],
			binds:    fields["binds"] == "true",
			cluster:  fields["cluster"],
			servers:  fields["servers"],
			routes:   fields["routes"],
			storage:  fields["storage"] == "true",
		})
	}
	require.NotEmptyf(t, configs, "%s never reported its effective Event Fabric configuration:\n%s", role, logs)
	return configs
}

func waitForManagedAPI(ctx context.Context, t *testing.T, machine *machine, process *managedProcess) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	cond := func() bool {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, machine.url+"/registrations", http.NoBody)
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
			return true, fmt.Sprintf("%s process exited before restoring the API:\n%s", process.role, out)
		}
		return false, ""
	}
	diag := diagStringer(func() string {
		return fmt.Sprintf("%s process did not restore the API:\n%s", process.role, process.Logs())
	})
	waitFor(t, process.role+" restoring the API", apiWaitTimeout, apiPollInterval, cond, abort, diag)
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

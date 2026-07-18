package scenarios

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestWarmStandbyFailoverAndPreferredPrimary drives the complete redundant
// process lifecycle from a built package. Status files are operational evidence;
// registration assertions stay on the public API.
func TestWarmStandbyFailoverAndPreferredPrimary(t *testing.T) {
	ctx := t.Context()
	scenariosDir, err := filepath.Abs(".")
	require.NoError(t, err)
	outDir := t.TempDir()
	buildProject(ctx, t, filepath.Join(scenariosDir, "testdata"), outDir, "manifest-contract")

	deployment := prepareSite(t, outDir, t.TempDir(), "manifest-contract", "node")
	node := deployment.machine(t, "node")
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
	standbyStatus := node.waitStatus(t, standby, "standby", true)
	catchUpTime := standbyStatus.UpdatedAt.Sub(standbyStarted)
	standbyMemory, err := processMemoryBytes(standby.pid())
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
	primary.kill()
	promotedStatus := node.waitStatus(t, standby, "active", false)
	promotionTime := promotedStatus.UpdatedAt.Sub(failoverStarted)
	waitForManagedAPI(ctx, t, node, standby)
	var gap time.Duration
	select {
	case gap = <-listenerGap:
	case <-time.After(apiWaitTimeout):
		t.Fatal("listener observer did not see the API recover")
	}

	require.Equal(t, stableView,
		waitForRegistrationStatus(ctx, t, node, stable.ProposalID, "accepted"),
		"promotion changed a registration retained before failover")
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
	node.waitStatus(t, primaryReturned, "standby", true)
	handoverStarted := time.Now()
	standby.stopGracefully(t)
	node.waitStatus(t, primaryReturned, "active", false)
	waitForManagedAPI(ctx, t, node, primaryReturned)
	handoverTime := time.Since(handoverStarted)

	// Repeat failover and reclamation to expose stale status, lock, listener, or
	// storage ownership left behind by the first transfer.
	standbySecond := node.startManaged(ctx, t, "standby", manifest.Standby.Args)
	node.waitStatus(t, standbySecond, "standby", true)
	primaryReturned.kill()
	node.waitStatus(t, standbySecond, "active", false)
	waitForManagedAPI(ctx, t, node, standbySecond)

	primarySecond := node.startManaged(ctx, t, "primary", manifest.Primary.Args)
	node.waitStatus(t, primarySecond, "standby", true)
	standbySecond.stopGracefully(t)
	node.waitStatus(t, primarySecond, "active", false)
	waitForManagedAPI(ctx, t, node, primarySecond)

	// Terminating a caught-up fence waiter must stop only that process. Context
	// cancellation of Fence.Acquire is covered by the platform contract tests.
	cancelledStandby := node.startManaged(ctx, t, "standby", manifest.Standby.Args)
	node.waitStatus(t, cancelledStandby, "standby", true)
	cancelledStandby.kill()
	require.True(t, primarySecond.running())
	waitForManagedAPI(ctx, t, node, primarySecond)

	// Full machine shutdown is primary-service stop followed by standby-service
	// stop. The standby may promote in the bounded interval and must still stop.
	shutdownStandby := node.startManaged(ctx, t, "standby", manifest.Standby.Args)
	node.waitStatus(t, shutdownStandby, "standby", true)
	primarySecond.stopGracefully(t)
	node.waitStatus(t, shutdownStandby, "active", false)
	shutdownStandby.stopGracefully(t)
	require.False(t, primarySecond.running())
	require.False(t, shutdownStandby.running())

	standbyLogs := standby.logs()
	clientOnly := strings.Index(standbyLogs, "storage=false")
	activeStorage := strings.Index(standbyLogs, "storage=true")
	require.NotEqual(t, -1, clientOnly, "standby never reported client-only composition:\n%s", standbyLogs)
	require.Greater(t, activeStorage, clientOnly,
		"storage opened before promotion or did not reopen after it:\n%s", standbyLogs)

	t.Logf("warm standby baseline: catch-up=%s promotion=%s listener-unavailable=%s handover=%s standby-working-set=%d bytes",
		catchUpTime, promotionTime, gap, handoverTime, standbyMemory)
}

func waitForManagedAPI(ctx context.Context, t *testing.T, machine *machine, process *managedProcess) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	require.Eventually(t, func() bool {
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
	}, apiWaitTimeout, apiPollInterval, "%s process did not restore the API:\n%s", process.role, process.logs())
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

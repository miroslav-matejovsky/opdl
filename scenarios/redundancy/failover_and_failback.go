// Package redundancy is the scenario category for local redundancy: one
// machine, two instances, one lease. The scenarios drive real built processes
// through failover and failback and observe them only through their public
// surfaces — the HTTP API of each instance and the local JSONL event record.
package redundancy

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/scenarios/internal/harness"
	"github.com/miroslav-matejovsky/opdl/scenarios/internal/waitfor"
)

// pollInterval is how often the scenario re-asks an instance for its state.
const pollInterval = 100 * time.Millisecond

// FailoverAndFailback is the whole local redundancy story, end to end, against
// real processes:
//
//  1. Primary and Standby start; the Primary serves and the Standby waits,
//     answering health checks and refusing domain operations at its own address.
//  2. The Primary is killed hard — no release, no goodbye. The Standby takes
//     over once the lease lapses.
//  3. The Primary is started again. After the stabilization window the Standby
//     hands ownership back in place: the Primary is Active again, and the
//     Standby is Passive in the same process it has been all along.
//
// Throughout, a poller samples both instances and fails the run if both ever
// report Active — the split-brain assertion at the process level.
func FailoverAndFailback(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	outDir := filepath.Join(harness.ScenarioDir(t), "out")
	deployment := harness.DeploySite(ctx, t, outDir, filepath.Join(harness.ScenarioDir(t), "work"), "redundancy")
	node := deployment.Machine(t, "node-a")

	manifest := harness.ReadManifest(t, node.BinaryPath)
	require.NotNil(t, manifest.Standby, "a machine that deploys a standby packages both launches")

	// Both instances are started as explicitly managed processes so the scenario
	// can kill and restart the Primary without touching the Standby.
	primary := node.StartManaged(ctx, t, harness.RolePrimary, manifest.Primary.Args)
	standby := node.StartManaged(ctx, t, harness.RoleStandby, manifest.Standby.Args)
	standbyPID := standby.PID()

	diag := harness.DiagStringer(func() string {
		return fmt.Sprintf("--- primary process ---\n%s\n--- standby process ---\n%s\n%s",
			primary.Logs(), standby.Logs(), harness.Diagnose([]*harness.Machine{node}))
	})

	// waitState polls one instance's own address until it reports the wanted
	// state. mustLive aborts the wait early when a process that has to be alive
	// for the state to ever arrive has died.
	waitState := func(what, url, wantState string, mustLive *harness.ManagedProcess) {
		t.Helper()
		cond := func() bool {
			instance, code, err := harness.FetchInstanceAt(ctx, url)
			return err == nil && code == http.StatusOK && instance.State == wantState
		}
		abort := waitfor.Abort(func() (bool, string) {
			if mustLive != nil && !mustLive.Running() {
				return true, mustLive.Role + " process died"
			}
			return false, ""
		})
		harness.WaitFor(t, what, harness.APIWaitTimeout, pollInterval, cond, abort, diag)
	}

	// The split-brain poller runs for the whole scenario: at no sampled instant
	// may both instances report Active.
	pollerCtx, stopPoller := context.WithCancel(ctx)
	defer stopPoller()
	var bothActive atomic.Int32
	pollerDone := make(chan struct{})
	go func() {
		defer close(pollerDone)
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-pollerCtx.Done():
				return
			case <-ticker.C:
				p, pCode, pErr := harness.FetchInstanceAt(pollerCtx, node.URL)
				s, sCode, sErr := harness.FetchInstanceAt(pollerCtx, node.StandbyURL)
				if pErr == nil && sErr == nil && pCode == http.StatusOK && sCode == http.StatusOK &&
					p.State == harness.InstanceStateActive && s.State == harness.InstanceStateActive {
					bothActive.Add(1)
				}
			}
		}
	}()

	// Normal life converges on the Preferred Primary shape, whichever instance
	// won the empty lease first: the Primary serves, the Standby waits.
	waitState("primary reporting itself active", node.URL, harness.InstanceStateActive, primary)
	waitState("standby reporting itself passive", node.StandbyURL, harness.InstanceStatePassive, standby)

	// The Passive surface, observed end to end: the Standby answers health at
	// its own address and refuses a domain operation, naming the instance that
	// owns.
	health, code, err := harness.FetchHealthAt(ctx, node.StandbyURL)
	require.NoError(t, err, "a passive instance answers its health endpoint")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, harness.InstanceStatePassive, health.RuntimeState)
	refusal, code := harness.GetProblemAt(ctx, t, node, node.StandbyURL, "/registrations")
	require.Equal(t, http.StatusServiceUnavailable, code,
		"a passive instance serves health checks and nothing else")
	require.Equal(t, "instance_passive", refusal.Title)

	// Failover: the Primary dies without releasing anything. The Standby must
	// wait out the lease and take over.
	require.NoError(t, primary.Kill())
	waitState("standby taking over after the primary died", node.StandbyURL, harness.InstanceStateActive, standby)

	// Failback: the Primary returns, waits out the stabilization window as
	// Passive, and gets ownership back. The Standby hands over in place: same
	// process, now Passive again, still answering.
	primary = node.StartManaged(ctx, t, harness.RolePrimary, manifest.Primary.Args)
	waitState("primary reclaiming ownership", node.URL, harness.InstanceStateActive, primary)
	waitState("standby back in the passive state", node.StandbyURL, harness.InstanceStatePassive, standby)
	require.True(t, standby.Running(), "the standby survived the whole story")
	require.Equal(t, standbyPID, standby.PID(),
		"the standby handed ownership back in place, without a restart")

	stopPoller()
	<-pollerDone
	require.Zero(t, bothActive.Load(), "at no sampled instant did both instances report Active")

	// The standby's own event record tells the same story: it took over a lease
	// the dead primary never released, and it initiated the failback itself.
	record, err := os.ReadFile(node.Sockets.StandbyEventsFile)
	require.NoError(t, err)
	require.Contains(t, string(record), `"platform.redundancy.ownership_acquired"`,
		"the takeover is stated in the standby's local record")
	require.Contains(t, string(record), `"abandoned":true`,
		"a hard-killed primary abandoned its lease, and the record says so")
	require.Contains(t, string(record), `"platform.redundancy.failback_initiated"`,
		"the handover back to the primary was the standby's own decision")

	// The machine's own store tells the story once, from both sides. Each
	// instance's record holds only what that instance did; this file is the
	// machine's, so the standby's takeover and the primary's reclaim are in it
	// in the order they happened, with nothing else mixed in.
	stored := harness.MachineEvents(t, node)
	var acquisitions []string
	for _, event := range stored {
		require.Equal(t, "machine", event.Scope,
			"the machine's store holds machine-scoped events and nothing else, whichever package stated them")
		if event.Type == "platform.redundancy.ownership_acquired" {
			acquisitions = append(acquisitions, event.Origin.ProcessRole)
		}
	}
	require.Equal(t, []string{"primary", "standby", "primary"}, acquisitions,
		"one file holds the whole handover: the primary owned, the standby took over when it died, and the primary took it back")

	// Nothing an instance said about itself reached it. The process starting,
	// binding, and stopping are that process's own story and stay in its record.
	instanceRecord, err := os.ReadFile(node.Sockets.EventsFile)
	require.NoError(t, err)
	require.Contains(t, string(instanceRecord), `"platform.app.process_started"`,
		"the instance's own record holds what that process did")
	for _, event := range stored {
		require.NotContains(t, event.Type, "platform.app.",
			"a fact about one process is not a fact about the machine")
	}

	// The epochs tell the same story as a pair of counters that outlive the
	// processes. The standby never restarted, so its two incarnations are its
	// process and the failover it served through. The primary was killed and came
	// back, so its second process is a third incarnation on top of the two its
	// first process had, and reclaiming ownership makes a fourth.
	require.Equal(t, harness.InstanceEpochs{Epoch: 2, Process: 1, Activation: 1},
		harness.InstanceEpoch(t, node.Sockets.StandbyStateFile),
		"the standby started once and became Active once; stepping back down does not advance it")
	require.Equal(t, harness.InstanceEpochs{Epoch: 4, Process: 2, Activation: 2},
		harness.InstanceEpoch(t, node.Sockets.StateFile),
		"the primary started, activated, was killed, started again, and reclaimed ownership")
}

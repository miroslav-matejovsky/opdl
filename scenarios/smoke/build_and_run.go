package smoke

import (
	"net/http"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/scenarios/internal/harness"
)

// BuildAndRunSingleMachine builds the simplest deployment there is and asks the
// running platform who it is.
//
// One machine, one Primary Instance, no standby: nothing to coordinate with and
// nothing to fail over to. The whole deployment is one process binding one
// listener, which makes this the floor the rest of the suite would build up
// from. Nothing here imports platform code; the binary under test is the one the
// builder produced a moment earlier.
//
// The platform has no domain surface yet, so the scenario checks what a running
// instance does have: it is Active and healthy, and it says so in its own log.
func BuildAndRunSingleMachine(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	outDir := filepath.Join(harness.ScenarioDir(t), "out")
	deployment := harness.DeploySite(ctx, t, outDir, filepath.Join(harness.ScenarioDir(t), "work"), "simple")
	node := deployment.Machine(t, "node-a")

	// The package is the first observable result: a machine that deploys no
	// standby must be packaged with only the primary launch, because a launch
	// record in the manifest is what a service installer would act on.
	manifest := harness.ReadManifest(t, node.BinaryPath)
	require.Nil(t, manifest.Standby, "a machine with no standby packages only the primary launch")
	require.Equal(t, []string{"-instance", "primary"}, manifest.Primary.Args)

	// StartSite waits for the instance to report itself active, which is the
	// runtime's own statement that it bound its API and brought its Event Fabric
	// up.
	deployment.StartSite(ctx, t)

	instance, code := harness.GetInstance(ctx, t, node)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "node-a", instance.Machine, "the instance reports the descriptor machine it was built for")
	require.Equal(t, harness.RolePrimary, instance.Role, "the role is fixed at build time")
	require.Equal(t, harness.InstanceStateActive, instance.State,
		"the only instance of the machine holds Primary Ownership, so it serves")
	require.Equal(t, node.Sockets.API, instance.Address,
		"the instance answers at the address the builder resolved from its authored local_port")
	require.Empty(t, instance.PeerAddress, "a machine with one instance has no peer to name")

	// The machine reported the configuration it booted with, naming every local
	// file it was authored with rather than a root it composes paths under.
	logs := node.Output()
	require.Contains(t, logs, "platform configuration")
	require.Contains(t, logs, "events_file  "+filepath.ToSlash(node.Sockets.EventsFile))
	require.Contains(t, logs, "state_file   "+filepath.ToSlash(node.Sockets.StateFile))

	// The application log is the other local file the blueprint authored, and it
	// is a separate record from the events: the instance says what it was doing
	// here and states facts there. Every line names the instance that wrote it, so
	// a line lifted out of the file still says where it came from.
	records := harness.ApplicationLog(t, node.Sockets.LogFile)
	require.NotEmpty(t, records, "the process logs its startup before it serves")
	for _, record := range records {
		require.Equal(t, "node-a", record.Machine)
		require.Equal(t, harness.RolePrimary, record.Instance)
	}
	require.True(t, slices.ContainsFunc(records, func(r harness.LogRecord) bool {
		return r.Message == "platform starting"
	}), "the log opens with the startup record: %+v", records)
	require.True(t, slices.ContainsFunc(records, func(r harness.LogRecord) bool {
		return r.Message == "active"
	}), "the instance records taking Primary Ownership in its own log: %+v", records)

	// The instance recorded its incarnation durably. This one has had two: the
	// process started, and then it took Primary Ownership. A machine with no
	// standby takes ownership at once, so both happened before it served.
	require.Equal(t, harness.InstanceEpochs{Epoch: 2, Process: 1, Activation: 1},
		harness.InstanceEpoch(t, node.Sockets.StateFile),
		"the epoch advances once for the process and once for the activation, and each kind is counted on its own")
	require.Contains(t, logs, "epoch        1 (starts 1, activations 0)",
		"the startup block reports the epoch the process itself claimed, before it took ownership")
}

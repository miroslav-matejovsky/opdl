package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/deployment"
	"github.com/miroslav-matejovsky/opdl/platform/internal/config"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events/jsonl"
	"github.com/miroslav-matejovsky/opdl/platform/internal/fabric"
	"github.com/miroslav-matejovsky/opdl/platform/internal/fabric/memory"
	fabricolric "github.com/miroslav-matejovsky/opdl/platform/internal/fabric/olric"
	"github.com/miroslav-matejovsky/opdl/platform/internal/registration"
)

var testDescriptor = deployment.Descriptor{
	Platform:    "opdl",
	Project:     "scenario",
	Environment: "development",
	Site:        "local",
	Machine:     "node",
	Role:        "all-in-one",
	IP:          "127.0.0.1",
	PlatformInstances: []deployment.PlatformInstance{
		{Name: "primary", APIAddress: "127.0.0.1:8080", FabricClientAddress: "127.0.0.1:3320", FabricMemberlistAddress: "127.0.0.1:3322"},
	},
	Fabric: deployment.Fabric{},
}

// testFabric is an in-process fabric. The lifecycle these tests check is the
// runtime's own ordering, which is the same whichever backend is composed, so
// they use the memory adapter and stay in the fast gate. The real backend's
// lifecycle is covered by the olric adapter's own tests.
func testFabric() fabric.Fabric {
	return memory.Open(testDescriptor, "primary")
}

// testLoop is a reconciler loop that is not running. serve stops it like any
// other, which is all these tests need: what they check is the runtime's
// teardown ordering, and reconciliation's own behavior is the registration
// package's to prove.
func testLoop() *reconcilerLoop {
	done := make(chan struct{})
	close(done)
	return &reconcilerLoop{cancel: func() {}, done: done}
}

// probeEvent is a real domain event, so these tests exercise the recorder the
// runtime actually composes rather than a double.
func probeEvent() events.Event {
	return registration.Requested{
		UnitType: 1, UnitID: 2, UnitTypeNameAdvertised: "Worker", Machine: "node", IP: "127.0.0.1",
	}
}

// freeAddress reserves an ephemeral loopback port, then releases it so the
// server under test can bind it.
func freeAddress(t *testing.T) string {
	t.Helper()
	var listen net.ListenConfig
	listener, err := listen.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	require.NoError(t, listener.Close())
	return addr
}

// newTestServer builds a server that answers on addr, so a test can tell a
// running platform from a stopped one.
func newTestServer(addr string) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	return &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: time.Second}
}

// get reports whether addr answers an HTTP request.
func get(ctx context.Context, addr string) bool {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr, http.NoBody)
	if err != nil {
		return false
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return false
	}
	_ = response.Body.Close()
	return true
}

func TestServeStopsServerAndClosesRecorderOnSignal(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	dir := t.TempDir()
	rec, err := newRecorder(testDescriptor, dir)
	require.NoError(t, err)
	addr := freeAddress(t)

	stopped := make(chan error, 1)
	go func() { stopped <- serve(ctx, newTestServer(addr), testLoop(), testFabric(), rec, 10*time.Second) }()
	require.Eventually(t, func() bool { return get(ctx, addr) }, 10*time.Second, 20*time.Millisecond,
		"server never became reachable")

	// Canceling the context is what an interrupt does to the runtime.
	cancel()
	select {
	case err := <-stopped:
		require.NoError(t, err, "an orderly shutdown is not an error")
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not return after its context was canceled")
	}

	require.False(t, get(t.Context(), addr), "server still accepts requests after shutdown")
	// The sink is closed, so a late event is reported rather than dropped.
	err = rec.Record(t.Context(), probeEvent())
	require.ErrorContains(t, err, "append to closed events file")
}

func TestServeClosesRecorderWhenServerCannotStart(t *testing.T) {
	dir := t.TempDir()
	rec, err := newRecorder(testDescriptor, dir)
	require.NoError(t, err)

	// Hold the address so ListenAndServe fails immediately.
	var listen net.ListenConfig
	listener, err := listen.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = listener.Close() }()

	err = serve(t.Context(), newTestServer(listener.Addr().String()), testLoop(), testFabric(), rec, 10*time.Second)
	require.ErrorContains(t, err, "serve HTTP", "a server that cannot start must report why")

	// Dependencies are released even on the failure path.
	require.ErrorContains(t,
		rec.Record(t.Context(), probeEvent()),
		"append to closed events file")
}

func TestServeReportsShutdownAndCloseFailuresTogether(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	failing := failingRecorder{err: errors.New("sink is gone")}
	addr := freeAddress(t)

	stopped := make(chan error, 1)
	go func() { stopped <- serve(ctx, newTestServer(addr), testLoop(), testFabric(), failing, 10*time.Second) }()
	require.Eventually(t, func() bool { return get(ctx, addr) }, 10*time.Second, 20*time.Millisecond,
		"server never became reachable")
	cancel()

	select {
	case err := <-stopped:
		require.ErrorContains(t, err, "close event recorder")
		require.ErrorContains(t, err, "sink is gone", "the sink's own error is preserved")
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not return after its context was canceled")
	}
}

// failingRecorder accepts every event and fails to close, so a test can check
// the runtime reports a sink it could not release.
type failingRecorder struct{ err error }

func (failingRecorder) Record(context.Context, events.Event) error { return nil }

func (r failingRecorder) Close() error { return r.err }

func TestNewRecorderDisabledWithoutEventsDir(t *testing.T) {
	rec, err := newRecorder(testDescriptor, "")
	require.NoError(t, err)
	require.Equal(t, events.NopRecorder{}, rec)
	require.NoError(t, rec.Record(t.Context(), probeEvent()))
	require.NoError(t, rec.Close())
}

// TestNewRecorderNamesTheFileAfterThisMachine checks the runtime hands the sink
// its own compiled-in identity: the events land in a file named after the
// descriptor, and the records themselves carry no node.
func TestNewRecorderNamesTheFileAfterThisMachine(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "events")
	rec, err := newRecorder(testDescriptor, dir)
	require.NoError(t, err)
	defer func() { _ = rec.Close() }()

	require.NoError(t, rec.Record(t.Context(), probeEvent()))

	entries, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	require.NoError(t, err)
	require.Equal(t, []string{
		filepath.Join(dir, "events-scenario-development-local-node-all-in-one.jsonl"),
	}, entries)
	require.Equal(t,
		jsonl.FileName(events.NodeFromDescriptor(testDescriptor)),
		filepath.Base(entries[0]))

	data, err := os.ReadFile(entries[0])
	require.NoError(t, err)
	require.Contains(t, string(data), `"type":"platform.registration.requested"`)
	require.NotContains(t, string(data), `"node":`, "the node is stated by the file name, not on every record")
}

func TestNewRecorderRejectsUnusableEventsDirAtStartup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o644))

	_, err := newRecorder(testDescriptor, path)
	require.ErrorContains(t, err, "event sink")
	require.ErrorContains(t, err, path)
}

// TestOlricConfigDerivesFromDescriptorAndAppliesOverrides pins the precedence
// rule: the deployment decides, and the configuration file may move sockets.
func TestOlricConfigDerivesFromDescriptorAndAppliesOverrides(t *testing.T) {
	descriptor := deployment.Descriptor{
		Site:    "north",
		Machine: "node-a",
		IP:      "10.0.1.10",
		PlatformInstances: []deployment.PlatformInstance{
			{Name: "primary", APIAddress: "10.0.1.10:8080", FabricClientAddress: "10.0.1.10:3320", FabricMemberlistAddress: "10.0.1.10:3322"},
		},
		Fabric: deployment.Fabric{
			Peers: []deployment.FabricPeer{{Site: "north", Machine: "node-b", Instance: "primary", IP: "10.0.1.11", FabricClientAddress: "10.0.1.11:3320", FabricMemberlistAddress: "10.0.1.11:3322"}},
		},
	}

	t.Run("no overrides uses the deployment", func(t *testing.T) {
		cfg, err := olricConfig(descriptor, "primary", config.FabricOlric{})
		require.NoError(t, err)
		require.Equal(t, "10.0.1.10:3320", cfg.ClientAddress)
		require.Equal(t, "10.0.1.10:3322", cfg.MemberlistAddress)
		require.Equal(t, []string{"10.0.1.11:3322"}, cfg.Join)
		require.Equal(t, fabricolric.DefaultStartTimeout, cfg.StartTimeout)
		require.Equal(t, 10*time.Second, cfg.ShutdownGrace)
	})

	t.Run("overrides move sockets", func(t *testing.T) {
		cfg, err := olricConfig(descriptor, "primary", config.FabricOlric{
			ClientAddress:     "127.0.0.1:4001",
			MemberlistAddress: "127.0.0.1:4002",
			Join:              []string{"127.0.0.1:4102"},
			StartTimeout:      "45s",
			ShutdownGrace:     "15s",
		})
		require.NoError(t, err)
		require.Equal(t, "127.0.0.1:4001", cfg.ClientAddress)
		require.Equal(t, "127.0.0.1:4002", cfg.MemberlistAddress)
		require.Equal(t, []string{"127.0.0.1:4102"}, cfg.Join)
		require.Equal(t, 45*time.Second, cfg.StartTimeout)
		require.Equal(t, 15*time.Second, cfg.ShutdownGrace)
	})

	t.Run("a partial override keeps the rest of the deployment", func(t *testing.T) {
		cfg, err := olricConfig(descriptor, "primary", config.FabricOlric{ClientAddress: "127.0.0.1:4001"})
		require.NoError(t, err)
		require.Equal(t, "127.0.0.1:4001", cfg.ClientAddress)
		require.Equal(t, "10.0.1.10:3322", cfg.MemberlistAddress, "an absent override is not a blank")
		require.Equal(t, []string{"10.0.1.11:3322"}, cfg.Join)
	})

	t.Run("an explicit empty join list seeds from nobody", func(t *testing.T) {
		cfg, err := olricConfig(descriptor, "primary", config.FabricOlric{Join: []string{}})
		require.NoError(t, err)
		require.Empty(t, cfg.Join, "an explicit empty list is a deliberate override")
	})

	t.Run("an unparsable start timeout is reported", func(t *testing.T) {
		_, err := olricConfig(descriptor, "primary", config.FabricOlric{StartTimeout: "soon"})
		require.ErrorContains(t, err, `start timeout "soon"`)
	})

	t.Run("an unparsable shutdown grace is reported", func(t *testing.T) {
		_, err := olricConfig(descriptor, "primary", config.FabricOlric{ShutdownGrace: "soon"})
		require.ErrorContains(t, err, `shutdown grace "soon"`)
	})
}

// TestStopFabricRecordsStoppedBeforeTheSinkCloses checks the shutdown order the
// runtime promises: the fabric is closed and says so while the sink can still
// take the event.
func TestStopFabricRecordsStoppedBeforeTheSinkCloses(t *testing.T) {
	dir := t.TempDir()
	rec, err := newRecorder(testDescriptor, dir)
	require.NoError(t, err)
	member := testFabric()

	require.NoError(t, stopFabric(t.Context(), member, rec, 10*time.Second))
	require.NoError(t, rec.Close())

	data, err := os.ReadFile(filepath.Join(dir, jsonl.FileName(events.NodeFromDescriptor(testDescriptor))))
	require.NoError(t, err)
	require.Contains(t, string(data), `"type":"platform.fabric.stopped"`)
	require.Contains(t, string(data), `"adapter":"memory"`, "the event names the adapter that was composed")

	// The fabric really is closed, not just reported as closed.
	_, err = member.Collection("late")
	require.ErrorIs(t, err, fabric.ErrClosed)
}

// TestServeRecordsFabricStoppedOnShutdown checks the whole ordered teardown: the
// server stops, the fabric reports it stopped, and only then does the sink close.
func TestServeRecordsFabricStoppedOnShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	dir := t.TempDir()
	rec, err := newRecorder(testDescriptor, dir)
	require.NoError(t, err)
	addr := freeAddress(t)
	member := testFabric()

	stopped := make(chan error, 1)
	go func() { stopped <- serve(ctx, newTestServer(addr), testLoop(), member, rec, 10*time.Second) }()
	require.Eventually(t, func() bool { return get(ctx, addr) }, 10*time.Second, 20*time.Millisecond,
		"server never became reachable")
	cancel()
	require.NoError(t, <-stopped)

	data, err := os.ReadFile(filepath.Join(dir, jsonl.FileName(events.NodeFromDescriptor(testDescriptor))))
	require.NoError(t, err)
	require.Contains(t, string(data), `"type":"platform.fabric.stopped"`,
		"the stopped event must reach the sink before it closes")
	require.False(t, get(t.Context(), addr))
}

func TestRunReportsMissingConfigFlag(t *testing.T) {
	require.Error(t, Run([]string{"-unknown"}))
}

func TestRunReportsUnusableConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(path, []byte("[invalid"), 0o644))
	require.ErrorContains(t, Run([]string{"-config", path}), "invalid configuration file")
}

func TestRunReportsUnusableEventsDir(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "not-a-dir")
	require.NoError(t, os.WriteFile(blocked, []byte("x"), 0o644))
	path := filepath.Join(dir, "config.toml")
	contents := fmt.Sprintf(`events_dir = %q
read_header_timeout = "5s"
shutdown_timeout = "10s"
[registration]
reconcile_interval = "1s"
[fabric.olric]
start_timeout = "30s"
shutdown_grace = "10s"
`, blocked)
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))

	require.ErrorContains(t, Run([]string{"-config", path}), "event sink")
}

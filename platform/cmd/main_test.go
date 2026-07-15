package main

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
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events/jsonl"
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
	var config net.ListenConfig
	listener, err := config.Listen(context.Background(), "tcp", "127.0.0.1:0")
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	rec, err := newRecorder(testDescriptor, dir)
	require.NoError(t, err)
	addr := freeAddress(t)

	stopped := make(chan error, 1)
	go func() { stopped <- serve(ctx, newTestServer(addr), rec) }()
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

	require.False(t, get(context.Background(), addr), "server still accepts requests after shutdown")

	// The sink is closed, so a late event is reported rather than dropped.
	err = rec.Record(context.Background(), probeEvent())
	require.ErrorContains(t, err, "append to closed events file")
}

func TestServeClosesRecorderWhenServerCannotStart(t *testing.T) {
	dir := t.TempDir()
	rec, err := newRecorder(testDescriptor, dir)
	require.NoError(t, err)

	// Hold the address so ListenAndServe fails immediately.
	var config net.ListenConfig
	listener, err := config.Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = listener.Close() }()

	err = serve(context.Background(), newTestServer(listener.Addr().String()), rec)
	require.ErrorContains(t, err, "serve HTTP", "a server that cannot start must report why")

	// Dependencies are released even on the failure path.
	require.ErrorContains(t,
		rec.Record(context.Background(), probeEvent()),
		"append to closed events file")
}

func TestServeReportsShutdownAndCloseFailuresTogether(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	failing := failingCloser{err: errors.New("sink is gone")}
	addr := freeAddress(t)

	stopped := make(chan error, 1)
	go func() { stopped <- serve(ctx, newTestServer(addr), failing) }()
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

type failingCloser struct{ err error }

func (c failingCloser) Close() error { return c.err }

func TestNewRecorderDisabledWithoutEventsDir(t *testing.T) {
	rec, err := newRecorder(testDescriptor, "")
	require.NoError(t, err)
	require.Equal(t, events.NopRecorder{}, rec)
	require.NoError(t, rec.Record(context.Background(), probeEvent()))
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

	require.NoError(t, rec.Record(context.Background(), probeEvent()))

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

func TestRunReportsMissingConfigFlag(t *testing.T) {
	require.Error(t, run([]string{"-unknown"}))
}

func TestRunReportsUnusableConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, []byte("{"), 0o644))
	require.ErrorContains(t, run([]string{"-config", path}), "invalid configuration file")
}

func TestRunReportsUnusableEventsDir(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "not-a-dir")
	require.NoError(t, os.WriteFile(blocked, []byte("x"), 0o644))
	path := filepath.Join(dir, "config.json")
	require.NoError(t, os.WriteFile(path, fmt.Appendf(nil, `{"events_dir": %q}`, blocked), 0o644))

	require.ErrorContains(t, run([]string{"-config", path}), "event sink")
}

package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/api"
	"github.com/miroslav-matejovsky/opdl/platform/config"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events/storage"
	"github.com/miroslav-matejovsky/opdl/platform/internal/httpapi"
	"github.com/miroslav-matejovsky/opdl/platform/internal/registration"
)

// problemDetails is the subset of huma's RFC 9457 error body these tests read.
// huma serves errors as application/problem+json; the stable machine code the
// platform sets is carried in detail.
type problemDetails struct {
	Status   int           `json:"status"`
	Title    string        `json:"title"`
	Detail   string        `json:"detail"`
	Instance *api.Instance `json:"instance,omitempty"`
}

// testInstance is what an Active instance reports about itself in these tests.
func testInstance() api.Instance {
	return api.Instance{
		Machine:     "node-a",
		Role:        "primary",
		State:       api.InstanceStateActive,
		Address:     "127.0.0.1:8080",
		PeerAddress: "127.0.0.1:8081",
	}
}

// testPassiveInstance is the same machine's Standby Instance while it is Passive,
// pointing back at the Primary Instance that holds ownership.
func testPassiveInstance() api.Instance {
	return api.Instance{
		Machine:     "node-a",
		Role:        "standby",
		State:       api.InstanceStatePassive,
		Address:     "127.0.0.1:8081",
		PeerAddress: "127.0.0.1:8080",
	}
}

// TestHandlerRefusesRegistrationCreationWithNotImplemented checks that POST /registrations
// returns 501 Not Implemented as eventfabric has been removed.
func TestHandlerRefusesRegistrationCreationWithNotImplemented(t *testing.T) {
	site := newSite(t, "node-a")
	nodeA := site.start("node-a")
	srv := httptest.NewServer(httpapi.NewHandler(nodeA.commands, nodeA.queries, testInstance, false))
	defer srv.Close()

	response := do(t, http.MethodPost, srv.URL+"/registrations",
		[]byte(`{"unit_type": 7, "unit_id": 42, "unit_type_name_advertised": "Billing"}`), "application/json")
	defer func() { _ = response.Body.Close() }()

	require.Equal(t, http.StatusNotImplemented, response.StatusCode)
}

func TestHandlerServesHealthEndpoints(t *testing.T) {
	site := newSite(t, "node-a")
	nodeA := site.start("node-a")

	activeSrv := httptest.NewServer(httpapi.NewHandler(nodeA.commands, nodeA.queries, testInstance, false))
	defer activeSrv.Close()

	passiveSrv := httptest.NewServer(httpapi.NewPassiveHandler(testPassiveInstance))
	defer passiveSrv.Close()

	for _, srv := range []*httptest.Server{activeSrv, passiveSrv} {
		resp := do(t, http.MethodGet, srv.URL+"/health", nil, "")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.NoError(t, resp.Body.Close())

		resp = do(t, http.MethodGet, srv.URL+"/health/live", nil, "")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.NoError(t, resp.Body.Close())

		resp = do(t, http.MethodGet, srv.URL+"/health/ready", nil, "")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.NoError(t, resp.Body.Close())

		resp = do(t, http.MethodGet, srv.URL+"/health/ha", nil, "")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.NoError(t, resp.Body.Close())
	}
}

func TestHandlerReturnsMethodErrors(t *testing.T) {
	site := newSite(t, "node-a")
	nodeA := site.start("node-a")
	srv := httptest.NewServer(httpapi.NewHandler(nodeA.commands, nodeA.queries, testInstance, false))
	defer srv.Close()

	// Routing is Go 1.22 ServeMux under huma. It answers a wrong method with 405
	// and an Allow header, and adds HEAD for any route that serves GET.
	response := do(t, http.MethodDelete, srv.URL+"/registrations", nil, "")
	defer func() { _ = response.Body.Close() }()
	require.Equal(t, http.StatusMethodNotAllowed, response.StatusCode)
	require.Equal(t, "GET, HEAD, POST", response.Header.Get("Allow"))

	response = do(t, http.MethodPost, srv.URL+"/registrations/some-proposal", nil, "")
	defer func() { _ = response.Body.Close() }()
	require.Equal(t, http.StatusMethodNotAllowed, response.StatusCode)
	require.Equal(t, "GET, HEAD", response.Header.Get("Allow"))

	response = do(t, http.MethodPost, srv.URL+"/registrations/conflicts", nil, "")
	defer func() { _ = response.Body.Close() }()
	require.Equal(t, http.StatusMethodNotAllowed, response.StatusCode)
	require.Equal(t, "GET, HEAD", response.Header.Get("Allow"))
}

func TestHandlerReturnsNotFoundForUnknownOrInvalidStatusPath(t *testing.T) {
	site := newSite(t, "node-a")
	nodeA := site.start("node-a")
	srv := httptest.NewServer(httpapi.NewHandler(nodeA.commands, nodeA.queries, testInstance, false))
	defer srv.Close()

	for _, path := range []string{
		"/registrations/deadbeef",
		"/registrations/",
		"/registrations/7/42/status",
		"/",
	} {
		response := do(t, http.MethodGet, srv.URL+path, nil, "")
		require.Equal(t, http.StatusNotFound, response.StatusCode, path)
		require.NoError(t, response.Body.Close())
	}
}

// site is the handler tests' deployment: the machines of one site, publishing
// into one in-process journal and folding it into their own projections.
//
// The journal is the Event Fabric's contract without a transport. That is all an
// HTTP test needs: what this file checks is the boundary's own behavior, and the
// domain's and the transport's are their own packages' to prove.
type site struct {
	t        *testing.T
	journal  *journal
	machines []config.Peer
}

// journal is an ordered, in-process site journal. It stamps each published event
// with the node that stated it, gives it the next sequence, and folds it into
// every node's projection, which is what the real fabric does once JetStream has
// accepted a write.
type journal struct {
	t *testing.T

	mu       sync.Mutex
	sequence uint64
	records  []storage.Delivery
	nodes    []*node
	failure  error
}

// node is one machine's registration composition: what it publishes through,
// what it folds into, and what its HTTP boundary is handed.
type node struct {
	machine    string
	projection *registration.Projection
	handler    *registration.Handler
	commands   *registration.CommandService
	queries    *registration.QueryService
}

// publisherFor is one node's narrow publishing capability: its own envelope
// factory, stamping its trusted identity onto everything it states, over the
// shared journal.
func publisherFor(t *testing.T, j *journal, machine string) events.Publisher {
	t.Helper()
	factory, err := events.NewFactory(config.Descriptor{
		Platform: "opdl", Project: "test", Environment: "development", Site: "local",
		Machine: machine, MachineProfile: "all-in-one",
	}, "primary")
	require.NoError(t, err)
	pub, err := storage.NewPublisher(factory, j)
	require.NoError(t, err)
	return pub
}

// newSite declares a site of machines and starts none of them. Machine i is at
// 127.0.0.(i+1).
func newSite(t *testing.T, machines ...string) *site {
	t.Helper()
	s := &site{
		t:       t,
		journal: &journal{t: t},
	}
	for i, machine := range machines {
		s.machines = append(s.machines, config.Peer{
			Site: "local", Machine: machine, Role: config.RolePrimary,
			IP: fmt.Sprintf("127.0.0.%d", i+1),
		})
	}
	return s
}

// start brings one expected machine up and returns its composition. It replays
// the journal into the new node's projection first, as a node joining a running
// site does before it serves.
//
//nolint:unparam // machine parameter kept for clarity when identifying site node
func (s *site) start(machine string) *node {
	s.t.Helper()
	expected := make([]registration.Location, 0, len(s.machines))
	var self registration.Location
	for _, peer := range s.machines {
		expected = append(expected, registration.Location{Machine: peer.Machine, IP: peer.IP})
		if peer.Machine == machine {
			self = registration.Location{Machine: peer.Machine, IP: peer.IP}
		}
	}
	require.NotEmpty(s.t, self.Machine, "%s is not a machine of this site", machine)

	n := &node{machine: self.Machine, projection: registration.NewProjection()}
	pub := publisherFor(s.t, s.journal, self.Machine)

	commands, queries, err := registration.Open(pub, n.projection, self, expected)
	require.NoError(s.t, err)
	handler, _ := registration.NewHandler(pub, n.projection, self, expected)
	n.commands, n.queries, n.handler = commands, queries, handler

	s.journal.attach(s.t.Context(), n)
	return n
}

// attach registers a node and replays the journal into its projection.
func (j *journal) attach(ctx context.Context, n *node) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, delivery := range j.records {
		require.NoError(j.t, n.projection.Apply(ctx, delivery))
	}
	j.nodes = append(j.nodes, n)
}

// Store orders one already stamped envelope and folds it into every node's projection.
func (j *journal) Store(ctx context.Context, envelope events.Envelope) error {
	j.mu.Lock()
	if j.failure != nil {
		defer j.mu.Unlock()
		return j.failure
	}
	require.NoError(j.t, envelope.Validate(), "the journal only stores complete envelopes")
	j.sequence++
	delivery := storage.Delivery{Envelope: envelope, Sequence: j.sequence}
	j.records = append(j.records, delivery)
	nodes := slices.Clone(j.nodes)
	j.mu.Unlock()

	for _, n := range nodes {
		require.NoError(j.t, n.projection.Apply(ctx, delivery))
	}
	return nil
}

func (j *journal) Close(_ context.Context) error { return nil }

func do(t *testing.T, method, url string, body []byte, contentType string) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), method, url, bytes.NewReader(body))
	require.NoError(t, err)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	return response
}

// TestPassiveHandlerAnswersForItself checks the one thing a Passive instance can
// answer, and the reason it binds a listener at all: what it is, what it is
// doing, and where the other instance is.
//
// None of it comes from the journal, so it is answerable while the instance's
// projection is still catching up and while it never will.
func TestPassiveHandlerAnswersForItself(t *testing.T) {
	srv := httptest.NewServer(httpapi.NewPassiveHandler(testPassiveInstance))
	defer srv.Close()

	response := do(t, http.MethodGet, srv.URL+api.PathInstance, nil, "")
	defer func() { _ = response.Body.Close() }()
	require.Equal(t, http.StatusOK, response.StatusCode)

	var instance api.Instance
	require.NoError(t, json.NewDecoder(response.Body).Decode(&instance))
	require.Equal(t, "standby", instance.Role, "the role is fixed at build time")
	require.Equal(t, api.InstanceStatePassive, instance.State, "the state is what changes")
	require.Equal(t, "127.0.0.1:8081", instance.Address)
	require.Equal(t, "127.0.0.1:8080", instance.PeerAddress, "and where to go instead")
}

// TestPassiveHandlerRefusesEveryDomainOperation is the ownership rule made
// visible at the API.
//
// A Passive instance holds no ownership and its projection is not authoritative,
// so answering a domain query from it would make the rule meaningless. It says so
// with a 503 that names the instance holding ownership, which is a pointer the
// caller can follow rather than a dead end.
func TestPassiveHandlerRefusesEveryDomainOperation(t *testing.T) {
	srv := httptest.NewServer(httpapi.NewPassiveHandler(testPassiveInstance))
	defer srv.Close()

	requests := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/registrations"},
		{http.MethodGet, "/registrations"},
		{http.MethodGet, "/registrations/conflicts"},
		{http.MethodGet, "/registrations/some-proposal"},
	}
	for _, request := range requests {
		t.Run(request.method+" "+request.path, func(t *testing.T) {
			response := do(t, request.method, srv.URL+request.path, []byte(`{}`), "application/json")
			defer func() { _ = response.Body.Close() }()

			require.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
			require.Equal(t, "application/problem+json", response.Header.Get("Content-Type"),
				"a Passive refusal parses as the same error shape an Active instance produces")

			var problem problemDetails
			require.NoError(t, json.NewDecoder(response.Body).Decode(&problem))
			require.Equal(t, "instance_passive", problem.Title)
			require.Contains(t, problem.Detail, "127.0.0.1:8080", "the refusal names where to go instead")
			require.NotNil(t, problem.Instance, "and which instance refused")
			require.Equal(t, api.InstanceStatePassive, problem.Instance.State)
		})
	}
}

// TestPassiveHandlerStillReportsUnknownPathsAsNotFound checks the refusal is
// scoped to the operations that exist.
//
// A Passive instance that answered 503 to everything would tell a caller with a
// typo that the platform is temporarily unavailable, and they would retry
// forever. Refusing is about who serves an operation, not about whether it exists.
func TestPassiveHandlerStillReportsUnknownPathsAsNotFound(t *testing.T) {
	srv := httptest.NewServer(httpapi.NewPassiveHandler(testPassiveInstance))
	defer srv.Close()

	response := do(t, http.MethodGet, srv.URL+"/no-such-operation", nil, "")
	defer func() { _ = response.Body.Close() }()
	require.Equal(t, http.StatusNotFound, response.StatusCode)
}

// testJournallessInstance is the only instance of a machine whose deployment
// authored no event storage. It holds ownership and has no peer to point at.
func testJournallessInstance() api.Instance {
	return api.Instance{
		Machine: "node-a",
		Role:    "primary",
		State:   api.InstanceStateActive,
		Address: "127.0.0.1:8080",
	}
}

// TestJournallessHandlerIsActiveAndStillRefusesDomainOperations is the
// no-event-storage deployment made visible at the API.
//
// The two halves are the whole contract. The instance reports itself active
// because it is: it holds Primary Ownership and there is nothing wrong with it.
// It still refuses every domain operation, because each one is a fact to be
// journalled or a query answered from a projection of one, and this deployment
// has no journal. The refusal names the deployment rather than an instance to go
// to instead, since there is no such instance and retrying will not help.
func TestJournallessHandlerIsActiveAndStillRefusesDomainOperations(t *testing.T) {
	srv := httptest.NewServer(httpapi.NewJournallessHandler(testJournallessInstance))
	defer srv.Close()

	response := do(t, http.MethodGet, srv.URL+api.PathInstance, nil, "")
	defer func() { _ = response.Body.Close() }()
	require.Equal(t, http.StatusOK, response.StatusCode)

	var instance api.Instance
	require.NoError(t, json.NewDecoder(response.Body).Decode(&instance))
	require.Equal(t, api.InstanceStateActive, instance.State,
		"an instance with no journal is not passive; it owns the machine and serves what it can")
	require.Empty(t, instance.PeerAddress)

	for _, request := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/registrations"},
		{http.MethodGet, "/registrations"},
		{http.MethodGet, "/registrations/conflicts"},
		{http.MethodGet, "/registrations/some-proposal"},
	} {
		t.Run(request.method+" "+request.path, func(t *testing.T) {
			response := do(t, request.method, srv.URL+request.path, []byte(`{}`), "application/json")
			defer func() { _ = response.Body.Close() }()

			require.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
			require.Equal(t, "application/problem+json", response.Header.Get("Content-Type"))

			var problem problemDetails
			require.NoError(t, json.NewDecoder(response.Body).Decode(&problem))
			require.Equal(t, "no_event_storage", problem.Title,
				"the reason is the deployment's, not this instance's state")
			require.Contains(t, problem.Detail, "event_storage",
				"and it names what to author to get a journal")
			require.NotNil(t, problem.Instance)
			require.Equal(t, api.InstanceStateActive, problem.Instance.State)
		})
	}
}

// TestJournallessHandlerStillReportsUnknownPathsAsNotFound checks the refusal is
// scoped to the operations that exist, for the same reason the Passive one is.
func TestJournallessHandlerStillReportsUnknownPathsAsNotFound(t *testing.T) {
	srv := httptest.NewServer(httpapi.NewJournallessHandler(testJournallessInstance))
	defer srv.Close()

	response := do(t, http.MethodGet, srv.URL+"/no-such-operation", nil, "")
	defer func() { _ = response.Body.Close() }()
	require.Equal(t, http.StatusNotFound, response.StatusCode)
}

// TestActiveHandlerAnswersTheSameInstanceOperation checks both instances serve
// the identity operation on one path with one shape, so an operator asks the same
// question of either and the specification describes one endpoint.
func TestActiveHandlerAnswersTheSameInstanceOperation(t *testing.T) {
	site := newSite(t, "node-a")
	nodeA := site.start("node-a")
	srv := httptest.NewServer(httpapi.NewHandler(nodeA.commands, nodeA.queries, testInstance, false))
	defer srv.Close()

	response := do(t, http.MethodGet, srv.URL+api.PathInstance, nil, "")
	defer func() { _ = response.Body.Close() }()
	require.Equal(t, http.StatusOK, response.StatusCode)

	var instance api.Instance
	require.NoError(t, json.NewDecoder(response.Body).Decode(&instance))
	require.Equal(t, api.InstanceStateActive, instance.State)
	require.Equal(t, "primary", instance.Role)
}

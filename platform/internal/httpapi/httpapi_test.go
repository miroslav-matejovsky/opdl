package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/api"
	"github.com/miroslav-matejovsky/opdl/platform/config"
	"github.com/miroslav-matejovsky/opdl/platform/internal/eventfabric"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
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

// TestHandlerServesAProposalFromPendingToAccepted is the API's side of the
// asynchronous story: 202 hands back the proposal's identity and says only that
// the journal took it, the status says pending while an expected machine has not
// answered, and it turns to accepted once that machine does.
func TestHandlerServesAProposalFromPendingToAccepted(t *testing.T) {
	site := newSite(t, "node-a", "node-b")
	nodeA := site.start("node-a")
	srv := httptest.NewServer(httpapi.NewHandler(nodeA.commands, nodeA.queries, testInstance, false))
	defer srv.Close()

	require.Empty(t, list(t, srv))
	require.Empty(t, conflicts(t, srv))

	accepted := propose(t, srv, `{"unit_type": 7, "unit_id": 42, "unit_type_name_advertised": "Billing", "role": "Master"}`)
	require.NotEmpty(t, accepted.ProposalID, "202 hands back the handle the client polls with")
	require.Positive(t, accepted.Sequence, "the proposal has a place in the site's history")

	// node-b is expected and has not answered, so the site cannot accept this.
	site.settle()
	view, code := status(t, srv, accepted.ProposalID)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, accepted.ProposalID, view.ProposalID)
	require.Equal(t, api.RegistrationStatusPending, view.Status)
	require.Equal(t, api.RegistrationStatusAccepted, instanceStatus(t, view, "node-a").Status)
	require.Equal(t, api.RegistrationStatusPending, instanceStatus(t, view, "node-b").Status,
		"an expected machine that has not answered is visible rather than silent")

	registrations := list(t, srv)
	require.Len(t, registrations, 1, "a pending proposal is listed like any other")
	require.Equal(t, api.RegistrationStatusPending, registrations[0].Status)

	// Start the machine the site was waiting for and let it answer.
	site.start("node-b")
	site.settle()

	view, code = status(t, srv, accepted.ProposalID)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, api.RegistrationStatusAccepted, view.Status)
	require.Equal(t, "node-a", view.Machine)
	require.Equal(t, "127.0.0.1", view.IP)
	require.Equal(t, api.RoleMaster, *view.Role)
	require.Equal(t, api.RegistrationStatusAccepted, instanceStatus(t, view, "node-a").Status)
	require.Equal(t, api.RegistrationStatusAccepted, instanceStatus(t, view, "node-b").Status)
}

// TestHandlerAnswersTheSameProposalOnEveryNode checks the query is a projection
// of the site's journal rather than the origin's private record. Every node
// folds the same events, so a proposal's status is the same answer wherever it
// is asked, and the client is no longer tied to the machine it posted to.
func TestHandlerAnswersTheSameProposalOnEveryNode(t *testing.T) {
	site := newSite(t, "node-a", "node-b")
	nodeA, nodeB := site.start("node-a"), site.start("node-b")
	origin := httptest.NewServer(httpapi.NewHandler(nodeA.commands, nodeA.queries, testInstance, false))
	defer origin.Close()
	other := httptest.NewServer(httpapi.NewHandler(nodeB.commands, nodeB.queries, testInstance, false))
	defer other.Close()

	accepted := propose(t, origin, `{"unit_type": 7, "unit_id": 42, "unit_type_name_advertised": "Billing"}`)
	site.settle()

	fromOrigin, code := status(t, origin, accepted.ProposalID)
	require.Equal(t, http.StatusOK, code)
	fromOther, code := status(t, other, accepted.ProposalID)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, fromOrigin, fromOther, "both nodes folded the same journal into the same answer")
	require.Equal(t, api.RegistrationStatusAccepted, fromOther.Status)
	require.Equal(t, "node-a", fromOther.Machine, "the origin is a fact of the proposal, not of who answers")

	require.Equal(t, list(t, origin), list(t, other))
}

// TestHandlerProjectsAConflictRatherThanRefusingIt is the contract change the
// journal's order makes possible. A competing claim is taken, ordered, and then
// decided; the POST cannot refuse it, because at the moment it is written
// nothing has decided anything yet.
func TestHandlerProjectsAConflictRatherThanRefusingIt(t *testing.T) {
	site := newSite(t, "node-a")
	nodeA := site.start("node-a")
	srv := httptest.NewServer(httpapi.NewHandler(nodeA.commands, nodeA.queries, testInstance, false))
	defer srv.Close()

	winner := propose(t, srv, `{"unit_type": 1, "unit_id": 2, "unit_type_name_advertised": "Worker"}`)
	loser := propose(t, srv, `{"unit_type": 1, "unit_id": 2, "unit_type_name_advertised": "Other"}`)
	require.NotEqual(t, winner.ProposalID, loser.ProposalID, "a different claim is a different proposal")
	require.Less(t, winner.Sequence, loser.Sequence, "the journal ordered them")
	site.settle()

	first, _ := status(t, srv, winner.ProposalID)
	require.Equal(t, api.RegistrationStatusAccepted, first.Status, "the first claim in journal order keeps the key")
	second, _ := status(t, srv, loser.ProposalID)
	require.Equal(t, api.RegistrationStatusRejected, second.Status)
	require.Equal(t, "registration_key_conflict", *second.Reason)

	resolved := conflicts(t, srv)
	require.Len(t, resolved, 1)
	require.Equal(t, api.RegistrationConflictResolutionResolved, resolved[0].ResolutionStatus)
	require.Equal(t, winner.ProposalID, resolved[0].Winner.ProposalID)
	require.Len(t, resolved[0].Losers, 1)
	require.Equal(t, loser.ProposalID, resolved[0].Losers[0].ProposalID)
}

// TestHandlerAnswersAnExactRetryWithTheSameProposal checks the identity contract
// the client depends on: an identical request is the same claim, so it returns
// the same handle rather than a second contender.
func TestHandlerAnswersAnExactRetryWithTheSameProposal(t *testing.T) {
	site := newSite(t, "node-a")
	nodeA := site.start("node-a")
	srv := httptest.NewServer(httpapi.NewHandler(nodeA.commands, nodeA.queries, testInstance, false))
	defer srv.Close()

	const body = `{"unit_type": 1, "unit_id": 2, "unit_type_name_advertised": "Worker"}`
	first := propose(t, srv, body)
	site.settle()
	retry := propose(t, srv, body)
	site.settle()

	require.Equal(t, first.ProposalID, retry.ProposalID, "an exact retry is the same claim")
	require.Len(t, list(t, srv), 1, "a retry is not a second registration")
	require.Empty(t, conflicts(t, srv), "a retry does not contend with itself")
}

func TestHandlerRejectsInvalidOrSpoofedRequests(t *testing.T) {
	site := newSite(t, "node-a")
	nodeA := site.start("node-a")
	srv := httptest.NewServer(httpapi.NewHandler(nodeA.commands, nodeA.queries, testInstance, false))
	defer srv.Close()

	// huma validates the request against the generated schema before the handler
	// runs, so a value the schema forbids is 422 and never reaches the domain: an
	// out-of-range integer and an unknown (spoofed) field are refused here.
	for _, body := range []string{
		`{"unit_type": 256, "unit_id": 1, "unit_type_name_advertised": "Worker"}`,
		`{"unit_type": -1, "unit_id": 1, "unit_type_name_advertised": "Worker"}`,
		`{"unit_type": 1, "unit_id": 1, "unit_type_name_advertised": "Worker", "machine": "spoofed"}`,
	} {
		response := do(t, http.MethodPost, srv.URL+"/registrations", []byte(body), "application/json")
		require.Equal(t, http.StatusUnprocessableEntity, response.StatusCode, body)
		require.NoError(t, response.Body.Close())
	}

	// A blank advertised name and an unknown role satisfy the schema (role is a
	// free string in the contract) but the domain refuses them, so they reach the
	// handler and come back 400.
	for _, body := range []string{
		`{"unit_type": 1, "unit_id": 1, "unit_type_name_advertised": " "}`,
		`{"unit_type": 1, "unit_id": 1, "unit_type_name_advertised": "Worker", "role": "master"}`,
	} {
		response := do(t, http.MethodPost, srv.URL+"/registrations", []byte(body), "application/json")
		require.Equal(t, http.StatusBadRequest, response.StatusCode, body)
		require.NoError(t, response.Body.Close())
	}

	// A body that is not JSON is refused for its media type before any decoding.
	response := do(t, http.MethodPost, srv.URL+"/registrations", []byte(`{}`), "text/plain")
	require.Equal(t, http.StatusUnsupportedMediaType, response.StatusCode)
	require.NoError(t, response.Body.Close())

	require.Empty(t, list(t, srv), "no invalid request is published")
}

// TestHandlerReportsAnUnavailableJournal checks the one failure a client can act
// on. Nothing was recorded, so the answer says "not now" rather than "no": the
// same request will succeed once the site journal takes writes again.
func TestHandlerReportsAnUnavailableJournal(t *testing.T) {
	site := newSite(t, "node-a")
	nodeA := site.start("node-a")
	srv := httptest.NewServer(httpapi.NewHandler(nodeA.commands, nodeA.queries, testInstance, false))
	defer srv.Close()

	site.journal.breakWith(errors.New("no quorum"))
	response := do(t, http.MethodPost, srv.URL+"/registrations",
		[]byte(`{"unit_type": 1, "unit_id": 2, "unit_type_name_advertised": "Worker"}`), "application/json")
	defer func() { _ = response.Body.Close() }()

	require.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
	var failure problemDetails
	require.NoError(t, json.NewDecoder(response.Body).Decode(&failure))
	require.Equal(t, "journal_unavailable", failure.Detail)

	// Queries are answered from the local projection, so they are unaffected by a
	// journal that will not take writes.
	require.Empty(t, list(t, srv))
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
	t     *testing.T
	scope eventfabric.SiteScope

	mu       sync.Mutex
	sequence uint64
	records  []eventfabric.Delivery
	nodes    []*node
	failure  error
}

// node is one machine's registration composition: what it publishes through,
// what it folds into, and what its HTTP boundary is handed.
type node struct {
	identity   events.Node
	projection *registration.Projection
	handler    *registration.Handler
	commands   *registration.CommandService
	queries    *registration.QueryService
	// consumed is the highest journal sequence this node's handler has taken,
	// standing in for a durable consumer's acknowledgements: without it a
	// redriven handler would restate every decision forever.
	consumed uint64
}

// publisher is one node's narrow publishing capability, stamping its own trusted
// identity onto everything it states.
type publisher struct {
	journal  *journal
	identity events.Node
}

func (p publisher) Publish(ctx context.Context, event events.Event) (eventfabric.Receipt, error) {
	return p.journal.append(ctx, p.identity, event)
}

// newSite declares a site of machines and starts none of them. Machine i is at
// 127.0.0.(i+1).
func newSite(t *testing.T, machines ...string) *site {
	t.Helper()
	s := &site{
		t:       t,
		journal: &journal{t: t, scope: eventfabric.NewSiteScope("test", "development", "local")},
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

	n := &node{
		identity: events.Node{
			Project: "test", Environment: "development", Site: "local",
			Machine: self.Machine, MachineProfile: "all-in-one",
		},
		projection: registration.NewProjection(),
	}
	pub := publisher{journal: s.journal, identity: n.identity}

	commands, queries, err := registration.Open(pub, n.projection, self, expected)
	require.NoError(s.t, err)
	handler, err := registration.NewHandler(pub, n.projection, self, expected, s.journal.scope)
	require.NoError(s.t, err)
	n.commands, n.queries, n.handler = commands, queries, handler

	s.journal.attach(s.t.Context(), n)
	return n
}

// settle lets the site reach its conclusion, which its durable handlers do on
// their own schedule. It drives every running handler over the events it has not
// taken until nothing new happens.
func (s *site) settle() {
	s.t.Helper()
	require.True(s.t, s.journal.settle(s.t.Context()), "the site never settled")
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

// append orders one event and folds it into every node's projection.
func (j *journal) append(ctx context.Context, identity events.Node, event events.Event) (eventfabric.Receipt, error) {
	j.mu.Lock()
	if j.failure != nil {
		defer j.mu.Unlock()
		return eventfabric.Receipt{}, j.failure
	}
	record, err := events.StampRecord(identity, events.NewID(), time.Now(), event)
	require.NoError(j.t, err)
	j.sequence++
	delivery := eventfabric.Delivery{Record: record, Sequence: j.sequence}
	j.records = append(j.records, delivery)
	nodes := slices.Clone(j.nodes)
	j.mu.Unlock()

	for _, n := range nodes {
		require.NoError(j.t, n.projection.Apply(ctx, delivery))
	}
	return eventfabric.Receipt{ID: record.ID, Sequence: delivery.Sequence}, nil
}

// breakWith makes the journal refuse writes, as an unreachable or full one does.
func (j *journal) breakWith(err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.failure = fmt.Errorf("%w: %w", api.ErrJournalUnavailable, err)
}

// settle drives every handler over what it has not taken, until a pass changes
// nothing. It reports whether the site reached that point.
func (j *journal) settle(ctx context.Context) bool {
	const passes = 10
	for range passes {
		progressed := false
		for _, n := range j.nodes {
			for _, delivery := range j.deliveries() {
				if delivery.Sequence <= n.consumed {
					continue
				}
				n.consumed = delivery.Sequence
				progressed = true
				if !j.routed(n.handler, delivery) {
					continue
				}
				require.NoError(j.t, n.handler.Handle(ctx, delivery))
			}
		}
		if !progressed {
			return true
		}
	}
	return false
}

func (j *journal) deliveries() []eventfabric.Delivery {
	j.mu.Lock()
	defer j.mu.Unlock()
	return slices.Clone(j.records)
}

// routed reports whether a handler declared the delivery's route. It asks the
// handler what it consumes rather than repeating the answer, so a handler that
// changed its routes changes what these tests deliver to it.
func (j *journal) routed(handler *registration.Handler, delivery eventfabric.Delivery) bool {
	route, err := eventfabric.NewRoute(j.scope, delivery.Record.Type)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(handler.Routes(), func(declared eventfabric.Route) bool {
		return declared.Subject() == route.Subject()
	})
}

// instanceStatus returns one platform instance's entry in a registration view.
func instanceStatus(t *testing.T, view api.Registration, machine string) api.PlatformInstanceRegistrationStatus {
	t.Helper()
	for _, instance := range view.PlatformInstances {
		if instance.Machine == machine {
			return instance
		}
	}
	require.FailNow(t, "no platform instance entry", "machine %s not in %v", machine, view.PlatformInstances)
	return api.PlatformInstanceRegistrationStatus{}
}

// propose posts a registration request and requires it to be taken.
func propose(t *testing.T, srv *httptest.Server, body string) api.ProposalAccepted {
	t.Helper()
	response := do(t, http.MethodPost, srv.URL+"/registrations", []byte(body), "application/json")
	defer func() { _ = response.Body.Close() }()
	require.Equal(t, http.StatusAccepted, response.StatusCode, body)

	var accepted api.ProposalAccepted
	require.NoError(t, json.NewDecoder(response.Body).Decode(&accepted))
	return accepted
}

// status reads one proposal's projected view and the code it was answered with.
func status(t *testing.T, srv *httptest.Server, proposalID string) (view api.Registration, code int) {
	t.Helper()
	response := do(t, http.MethodGet, srv.URL+"/registrations/"+proposalID, nil, "")
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return api.Registration{}, response.StatusCode
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&view))
	return view, response.StatusCode
}

func list(t *testing.T, srv *httptest.Server) []api.Registration {
	t.Helper()
	response := do(t, http.MethodGet, srv.URL+"/registrations", nil, "")
	defer func() { _ = response.Body.Close() }()
	require.Equal(t, http.StatusOK, response.StatusCode)

	var registrations []api.Registration
	require.NoError(t, json.NewDecoder(response.Body).Decode(&registrations))
	return registrations
}

func conflicts(t *testing.T, srv *httptest.Server) []api.RegistrationConflict {
	t.Helper()
	response := do(t, http.MethodGet, srv.URL+"/registrations/conflicts", nil, "")
	defer func() { _ = response.Body.Close() }()
	require.Equal(t, http.StatusOK, response.StatusCode)

	var resolved []api.RegistrationConflict
	require.NoError(t, json.NewDecoder(response.Body).Decode(&resolved))
	return resolved
}

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

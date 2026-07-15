package registration

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/api"
	"github.com/miroslav-matejovsky/opdl/platform/deployment"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
	"github.com/miroslav-matejovsky/opdl/platform/internal/fabric/memory"
)

// This file is the registration tests' site: the machines of one deployment,
// sharing one fabric in one process, each with the service and reconciler a real
// platform process runs.
//
// It exists because registration is site-wide behavior. A test that cannot leave
// one expected machine down, bring it up later, or restart it cannot test what
// this package actually promises. Machines are therefore declared and started
// separately: a machine that is expected but was never started is exactly a
// machine that has not booted yet, which is the case the whole two-phase design
// exists for.
//
// Nothing here schedules anything. A test reconciles when it decides to, so what
// the site does is the test's statement rather than a race with a ticker.

// testSite is the site every test deployment is in.
const testSite = "local"

// site is a test's deployment: an immutable expected membership, and whichever
// of those machines are running right now.
type site struct {
	t        *testing.T
	shared   *memory.Site
	expected []deployment.FabricPeer
	running  map[string]*instance
}

// instance is one running platform process's registration, as its runtime would
// compose it.
type instance struct {
	// service is what its HTTP API calls.
	service *Service
	// reconciler is the pass its runtime schedules.
	reconciler *Reconciler
	// recorder is its event log.
	recorder *recordingRecorder

	fabric *memory.Fabric
}

// newSite declares a site of machines and starts none of them. Machine i is at
// 127.0.0.(i+1), so each has the distinct IP a real deployment requires.
func newSite(t *testing.T, machines ...string) *site {
	t.Helper()
	require.NotEmpty(t, machines, "a site has at least one machine")
	s := &site{t: t, shared: memory.NewSite(), running: map[string]*instance{}}
	for i, machine := range machines {
		s.expected = append(s.expected, deployment.FabricPeer{
			Site:    testSite,
			Machine: machine,
			IP:      fmt.Sprintf("127.0.0.%d", i+1),
		})
	}
	return s
}

// start brings one expected machine up on the site's shared fabric.
func (s *site) start(machine string) *instance {
	s.t.Helper()
	require.NotContains(s.t, s.running, machine, "%s is already running", machine)
	return s.launch(machine, &recordingRecorder{})
}

// restart stops a running machine and starts it again on the same site, keeping
// its event log. A restarted process appends to the log it was already writing,
// so this is how a test sees whether coming back restates facts it had already
// stated.
func (s *site) restart(machine string) *instance {
	s.t.Helper()
	running, ok := s.running[machine]
	require.True(s.t, ok, "%s is not running", machine)
	require.NoError(s.t, running.fabric.Close(context.Background()))
	delete(s.running, machine)
	return s.launch(machine, running.recorder)
}

// launch starts one machine's registration on the site.
func (s *site) launch(machine string, recorder *recordingRecorder) *instance {
	s.t.Helper()
	f := s.shared.Open(s.descriptorFor(machine))
	service, reconciler, err := Open(f, recorder)
	require.NoError(s.t, err)
	s.t.Cleanup(func() { _ = f.Close(context.Background()) })
	started := &instance{service: service, reconciler: reconciler, recorder: recorder, fabric: f}
	s.running[machine] = started
	return started
}

// descriptorFor builds one machine's resolved deployment descriptor: itself,
// plus every other expected machine of the site as a peer.
func (s *site) descriptorFor(machine string) deployment.Descriptor {
	descriptor := deployment.Descriptor{Site: testSite}
	for _, peer := range s.expected {
		if peer.Machine == machine {
			descriptor.Machine, descriptor.IP = peer.Machine, peer.IP
			continue
		}
		descriptor.Fabric.Peers = append(descriptor.Fabric.Peers, peer)
	}
	require.NotEmpty(s.t, descriptor.Machine, "%s is not a machine of this site", machine)
	return descriptor
}

// reconcile lets the site settle: every running machine reconciles, twice, in
// machine order. It is explicit so a test says exactly when the site is allowed
// to make progress, and it is what the site's schedulers do on their own.
//
// Two rounds is this site's fixed point rather than a guess. A machine's own
// confirmation depends on nothing but the stored request, so one round records
// every confirmation the running machines will ever make; a commit depends on
// those confirmations, so the round after can see them. Anything still pending
// after that is pending because an expected machine is not running, which no
// number of rounds fixes. A real site converges the same way, on its own
// schedule and in whatever order its machines happen to wake up.
func (s *site) reconcile() {
	s.t.Helper()
	for range 2 {
		for _, expected := range s.expected {
			running, ok := s.running[expected.Machine]
			if !ok {
				continue
			}
			require.NoError(s.t, running.reconciler.Reconcile(context.Background()))
		}
	}
}

// reject records machine's refusal of a request directly, which is what that
// machine's own reconciler would write if it refused.
//
// A test has to do this itself because validation is a function of the stored
// record and the site's static deployment, so every instance of a healthy site
// reaches the same verdict: one machine refusing while the others accept is not
// a state this phase's instances can reach on their own. The aggregation still
// has to handle it, because a rejection is per-instance by design, and this is
// how a test presents one.
func (s *site) reject(from *instance, machine string, key Key, reason string) {
	s.t.Helper()
	request := s.storedRequest(from, key)
	created, err := from.service.store.createConfirmation(context.Background(), confirmationRecord{
		Version:     recordVersion,
		UnitType:    key.UnitType,
		UnitID:      key.UnitID,
		Fingerprint: request.Fingerprint,
		Machine:     machine,
		Status:      api.RegistrationStatusRejected,
		Reason:      &reason,
	})
	require.NoError(s.t, err)
	require.True(s.t, created, "%s had already decided about %s", machine, key)
}

// storedRequest returns the proposal the site holds under key.
func (s *site) storedRequest(from *instance, key Key) requestRecord {
	s.t.Helper()
	request, found, err := from.service.store.request(context.Background(), key)
	require.NoError(s.t, err)
	require.True(s.t, found, "the site holds no request for %s", key)
	return request
}

// decisions returns what the expected machines have decided about a request,
// keyed by machine. A machine that has not decided is absent.
func (s *site) decisions(from *instance, key Key) map[string]confirmationRecord {
	s.t.Helper()
	decisions, err := from.service.store.decisions(context.Background(), s.storedRequest(from, key), from.service.members)
	require.NoError(s.t, err)
	return decisions
}

// accepted returns the committed registration under key, and whether the site
// has committed one at all.
func (s *site) accepted(from *instance, key Key) (acceptedRecord, bool) {
	s.t.Helper()
	record, found, err := from.service.store.accepted(context.Background(), key)
	require.NoError(s.t, err)
	return record, found
}

// create submits a request to this machine and requires it to be taken.
func (i *instance) create(t *testing.T, request api.RegistrationRequest) CreateResult {
	t.Helper()
	result, err := i.service.Create(context.Background(), request)
	require.NoError(t, err)
	return result
}

// get returns this machine's view of a request and requires it to be found.
// Like the status endpoint it backs, it reconciles the key before answering.
func (i *instance) get(t *testing.T, key Key) api.Registration {
	t.Helper()
	view, found, err := i.service.Get(context.Background(), key)
	require.NoError(t, err)
	require.True(t, found, "%s does not hold a request for %s", i.service.location.Machine, key)
	return view
}

// list returns this machine's view of every request in the site.
func (i *instance) list(t *testing.T) []api.Registration {
	t.Helper()
	registrations, err := i.service.List(context.Background())
	require.NoError(t, err)
	return registrations
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

// recordingRecorder captures what a machine states, in order, so a test can
// assert behavior by the facts reported rather than by the calls made.
type recordingRecorder struct {
	mu        sync.Mutex
	recorded  []events.Event
	failAfter int
	err       error
}

func (r *recordingRecorder) Record(_ context.Context, event events.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil && len(r.recorded) >= r.failAfter {
		return r.err
	}
	r.recorded = append(r.recorded, event)
	return nil
}

func (r *recordingRecorder) events() []events.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]events.Event(nil), r.recorded...)
}

// types returns the recorded event types in order.
func (r *recordingRecorder) types() []events.Type {
	recorded := r.events()
	types := make([]events.Type, 0, len(recorded))
	for _, event := range recorded {
		types = append(types, event.EventType())
	}
	return types
}

// only returns the one event of a type the machine stated, failing when it
// stated none or more than one. Stating a fact exactly once is usually the point
// of the assertion, so it is checked here rather than assumed.
func (r *recordingRecorder) only(t *testing.T, eventType events.Type) events.Event {
	t.Helper()
	var matched []events.Event
	for _, event := range r.events() {
		if event.EventType() == eventType {
			matched = append(matched, event)
		}
	}
	require.Len(t, matched, 1, "expected exactly one %s, stated %v", eventType, r.types())
	return matched[0]
}

package registration

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/api"
	"github.com/miroslav-matejovsky/opdl/platform/deployment"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
	"github.com/miroslav-matejovsky/opdl/platform/internal/fabric"
	"github.com/miroslav-matejovsky/opdl/platform/internal/fabric/memory"
)

// unitKey is the registration key most tests use.
var unitKey = Key{UnitType: 7, UnitID: 42}

// unitRequest is the request most tests submit.
func unitRequest() api.RegistrationRequest {
	return api.RegistrationRequest{UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "Billing"}
}

// TestRequestStaysPendingWhileAnExpectedMachineIsOffline is the promise the
// whole two-phase design exists to keep: the acceptance boundary is every
// machine the deployment declares, so a request waits for a machine that is not
// running rather than being accepted without it.
func TestRequestStaysPendingWhileAnExpectedMachineIsOffline(t *testing.T) {
	site := newSite(t, "node-a", "node-b")
	nodeA := site.start("node-a")

	require.Equal(t, CreateResultNew, nodeA.create(t, unitRequest()))

	// Reconciling as hard as the site can does not help: node-b is expected and
	// has not answered, and no amount of the origin trying changes that.
	site.reconcile()
	site.reconcile()

	view := nodeA.get(t, unitKey)
	require.Equal(t, api.RegistrationStatusPending, view.Status)
	require.Equal(t, api.RegistrationStatusAccepted, instanceStatus(t, view, "node-a").Status,
		"the origin answers for itself like any other expected instance")
	require.Equal(t, api.RegistrationStatusPending, instanceStatus(t, view, "node-b").Status,
		"an expected machine that has not answered is pending, not absent")

	_, committed := site.accepted(nodeA, unitKey)
	require.False(t, committed, "nothing may be committed while an expected instance has not accepted")
}

// TestStartingTheMissingMachineAllowsAcceptance is the other half: the site is
// not deadlocked, it is waiting, and the machine it waits for is all it needs.
func TestStartingTheMissingMachineAllowsAcceptance(t *testing.T) {
	site := newSite(t, "node-a", "node-b")
	nodeA := site.start("node-a")
	nodeA.create(t, unitRequest())
	require.Equal(t, api.RegistrationStatusPending, nodeA.get(t, unitKey).Status)

	site.start("node-b")
	site.reconcile()

	view := nodeA.get(t, unitKey)
	require.Equal(t, api.RegistrationStatusAccepted, view.Status)
	require.Equal(t, api.RegistrationStatusAccepted, instanceStatus(t, view, "node-a").Status)
	require.Equal(t, api.RegistrationStatusAccepted, instanceStatus(t, view, "node-b").Status)

	// Accepted is the record existing, not the confirmations looking complete.
	record, committed := site.accepted(nodeA, unitKey)
	require.True(t, committed)
	require.Equal(t, "node-a", record.OriginMachine, "the origin is where the client asked, whoever committed it")
	require.Equal(t, "127.0.0.1", record.OriginIP)
}

// TestEveryExpectedMachineRecordsOneMatchingConfirmation checks the acceptance
// set is really every expected machine including the origin, and that each of
// them decides once however often the site reconciles.
func TestEveryExpectedMachineRecordsOneMatchingConfirmation(t *testing.T) {
	site := newSite(t, "node-a", "node-b", "node-c")
	nodeA := site.start("node-a")
	site.start("node-b")
	site.start("node-c")
	nodeA.create(t, unitRequest())

	for range 3 {
		site.reconcile()
	}

	request := site.storedRequest(nodeA, unitKey)
	decisions := site.decisions(nodeA, unitKey)
	require.Len(t, decisions, 3, "every expected machine, the origin included, decides")
	for _, machine := range []string{"node-a", "node-b", "node-c"} {
		decision := decisions[machine]
		require.Equal(t, api.RegistrationStatusAccepted, decision.Status)
		require.Equal(t, machine, decision.Machine)
		require.Equal(t, request.Fingerprint, decision.Fingerprint,
			"a confirmation states which proposal it is about")
	}

	// Repeated passes state the fact once, because the transition happened once.
	require.Equal(t, []events.Type{TypeRequested, TypeConfirmed, TypeAccepted}, nodeA.recorder.types())
}

// TestConfirmationOfAnotherProposalCannotApproveThisOne is why a confirmation is
// keyed by fingerprint rather than by registration key: a decision is only ever
// about the data it was made on, so a stale or forged one is not weighed and
// discarded, it is never consulted at all.
func TestConfirmationOfAnotherProposalCannotApproveThisOne(t *testing.T) {
	site := newSite(t, "node-a", "node-b")
	nodeA := site.start("node-a")
	nodeA.create(t, unitRequest())

	// node-b accepts, but of something else.
	created, err := nodeA.service.store.createConfirmation(t.Context(), confirmationRecord{
		Version:     recordVersion,
		UnitType:    unitKey.UnitType,
		UnitID:      unitKey.UnitID,
		Fingerprint: "0000000000000000000000000000000000000000000000000000000000000000",
		Machine:     "node-b",
		Status:      api.RegistrationStatusAccepted,
	})
	require.NoError(t, err)
	require.True(t, created)

	site.reconcile()

	view := nodeA.get(t, unitKey)
	require.Equal(t, api.RegistrationStatusPending, view.Status,
		"a confirmation of another proposal must not approve this one")
	require.Equal(t, api.RegistrationStatusPending, instanceStatus(t, view, "node-b").Status,
		"node-b has still said nothing about this proposal")
	_, committed := site.accepted(nodeA, unitKey)
	require.False(t, committed)
}

// TestListShowsPendingThenAcceptedThenRejected checks the list is a view of
// requests rather than of registrations: a request that has not been accepted,
// and one that never will be, are both visible.
func TestListShowsPendingThenAcceptedThenRejected(t *testing.T) {
	site := newSite(t, "node-a", "node-b")
	nodeA := site.start("node-a")
	nodeA.create(t, unitRequest())

	site.reconcile()
	pending := nodeA.list(t)
	require.Len(t, pending, 1, "a pending request is listed, not hidden until it is accepted")
	require.Equal(t, api.RegistrationStatusPending, pending[0].Status)

	site.start("node-b")
	site.reconcile()
	accepted := nodeA.list(t)
	require.Len(t, accepted, 1)
	require.Equal(t, api.RegistrationStatusAccepted, accepted[0].Status)

	// A second request, refused by one expected machine, is listed as rejected.
	other := api.RegistrationRequest{UnitType: 1, UnitID: 2, UnitTypeNameAdvertised: "Worker"}
	nodeA.create(t, other)
	otherKey := Key{UnitType: 1, UnitID: 2}
	site.reject(nodeA, "node-b", otherKey, "unit_not_supported")
	site.reconcile()

	// Both requests originated on node-a, so the list is in unit-key order.
	listed := nodeA.list(t)
	require.Len(t, listed, 2)
	rejected := listed[0]
	require.Equal(t, otherKey, Key{UnitType: rejected.UnitType, UnitID: rejected.UnitID})
	require.Equal(t, api.RegistrationStatusRejected, rejected.Status)
	require.Equal(t, "unit_not_supported", *rejected.Reason)
	require.Equal(t, api.RegistrationStatusAccepted, listed[1].Status,
		"one request being refused says nothing about another")
}

// TestPlatformInstancesReportEveryExpectedMachine checks the per-instance
// projection covers the whole expected topology, so the interesting case, the
// machine that has not answered, is visible rather than missing.
func TestPlatformInstancesReportEveryExpectedMachine(t *testing.T) {
	site := newSite(t, "node-a", "node-b", "node-c")
	nodeA := site.start("node-a")
	nodeA.create(t, unitRequest())
	notSupported := "unit_not_supported"
	site.reject(nodeA, "node-b", unitKey, notSupported)
	site.reconcile()

	view := nodeA.get(t, unitKey)
	require.Equal(t, []api.PlatformInstanceRegistrationStatus{
		{Machine: "node-a", IP: "127.0.0.1", Status: api.RegistrationStatusAccepted},
		{Machine: "node-b", IP: "127.0.0.2", Status: api.RegistrationStatusRejected, Reason: &notSupported},
		{Machine: "node-c", IP: "127.0.0.3", Status: api.RegistrationStatusPending},
	}, view.PlatformInstances, "every expected machine reports its own descriptor identity and its own answer")

	require.Equal(t, api.RegistrationStatusRejected, view.Status,
		"one refusal is enough: acceptance needs all of them")
	require.Equal(t, "unit_not_supported", *view.Reason)
	_, committed := site.accepted(nodeA, unitKey)
	require.False(t, committed, "a rejected request commits nothing, even with node-c still to answer")
}

// TestStatusLookupWorksOnlyOnTheOriginMachine checks a client checks its request
// where it made it. Every machine holds the same state; only one was asked.
func TestStatusLookupWorksOnlyOnTheOriginMachine(t *testing.T) {
	site := newSite(t, "node-a", "node-b")
	nodeA := site.start("node-a")
	nodeB := site.start("node-b")
	nodeA.create(t, unitRequest())
	site.reconcile()

	_, found, err := nodeB.service.Get(t.Context(), unitKey)
	require.NoError(t, err)
	require.False(t, found, "node-b holds the request but is not who was asked")

	// The list is the site's state, so node-b answers that for the same request.
	listed := nodeB.list(t)
	require.Len(t, listed, 1)
	require.Equal(t, "node-a", listed[0].Machine)
	require.Equal(t, api.RegistrationStatusAccepted, listed[0].Status)
}

// TestExactRetryIsIdempotentWhilePendingAndOnceAccepted checks a retry is
// answered as the same claim in both resting states, and changes nothing.
func TestExactRetryIsIdempotentWhilePendingAndOnceAccepted(t *testing.T) {
	site := newSite(t, "node-a", "node-b")
	nodeA := site.start("node-a")
	require.Equal(t, CreateResultNew, nodeA.create(t, unitRequest()))

	site.reconcile()
	require.Equal(t, api.RegistrationStatusPending, nodeA.get(t, unitKey).Status)
	require.Equal(t, CreateResultRetry, nodeA.create(t, unitRequest()), "a retry of a pending request is the same claim")

	site.start("node-b")
	site.reconcile()
	require.Equal(t, api.RegistrationStatusAccepted, nodeA.get(t, unitKey).Status)
	require.Equal(t, CreateResultRetry, nodeA.create(t, unitRequest()), "a retry of an accepted request is still the same claim")

	require.Equal(t, []events.Type{TypeRequested, TypeConfirmed, TypeAccepted}, nodeA.recorder.types(),
		"a retry changes nothing, so it states nothing")
}

// TestSameMachineDifferentProposalIsConflict covers the limitation this phase
// accepts: with no caller identity, the platform can only compare claims. A
// second unit that asks for exactly the same thing is answered as a retry; every
// visible difference is a conflict.
func TestSameMachineDifferentProposalIsConflict(t *testing.T) {
	master, slave := api.RoleMaster, api.RoleSlave
	tests := []struct {
		name    string
		attempt api.RegistrationRequest
	}{
		{
			name:    "different advertised name",
			attempt: api.RegistrationRequest{UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "Payments", Role: &master},
		},
		{
			name:    "different role",
			attempt: api.RegistrationRequest{UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "Billing", Role: &slave},
		},
		{
			name:    "role dropped",
			attempt: api.RegistrationRequest{UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "Billing"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			site := newSite(t, "node-a")
			nodeA := site.start("node-a")
			original := api.RegistrationRequest{UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "Billing", Role: &master}
			nodeA.create(t, original)
			site.reconcile()

			_, err := nodeA.service.Create(t.Context(), test.attempt)
			require.ErrorIs(t, err, ErrConflict)

			recorded := nodeA.recorder.events()
			conflict := recorded[len(recorded)-1]
			require.Equal(t, TypeConflict, conflict.EventType())
			require.Equal(t, []string{events.TagWarning}, conflict.(events.Tagged).Tags())

			// The refused attempt left the site exactly as it was.
			view := nodeA.get(t, unitKey)
			require.Equal(t, "Billing", view.UnitTypeNameAdvertised)
			require.Equal(t, api.RoleMaster, *view.Role)
			require.Equal(t, api.RegistrationStatusAccepted, view.Status)
		})
	}
}

// TestAnotherMachineCannotTakeAnAcceptedKey checks the key is the site's, not a
// machine's: a request from elsewhere is a conflict, and the accepted record it
// collided with is untouched.
func TestAnotherMachineCannotTakeAnAcceptedKey(t *testing.T) {
	site := newSite(t, "node-a", "node-b")
	nodeA := site.start("node-a")
	nodeB := site.start("node-b")
	nodeA.create(t, unitRequest())
	site.reconcile()
	require.Equal(t, api.RegistrationStatusAccepted, nodeA.get(t, unitKey).Status)

	// The same request, byte for byte, from the other machine. The origin is
	// part of the claim, so this is a different claim.
	_, err := nodeB.service.Create(t.Context(), unitRequest())
	require.ErrorIs(t, err, ErrConflict)

	conflict := nodeB.recorder.only(t, TypeConflict).(Conflict)
	require.Equal(t, Fields{UnitTypeNameAdvertised: "Billing", Machine: "node-a", IP: "127.0.0.1"}, conflict.Existing)
	require.Equal(t, Fields{UnitTypeNameAdvertised: "Billing", Machine: "node-b", IP: "127.0.0.2"}, conflict.Attempted)
	require.Equal(t, ReasonKeyConflict, conflict.Reason)

	record, committed := site.accepted(nodeA, unitKey)
	require.True(t, committed)
	require.Equal(t, "node-a", record.OriginMachine, "the accepted registration is preserved as it was")
	require.Equal(t, api.RegistrationStatusAccepted, nodeA.get(t, unitKey).Status)
}

// TestConcurrentDistinctRequestsProduceOneWinner checks the claim is one atomic
// act against the stable in-memory site, so a race produces a winner and
// conflicts rather than two registrations or a torn one.
func TestConcurrentDistinctRequestsProduceOneWinner(t *testing.T) {
	site := newSite(t, "node-a", "node-b")
	nodeA := site.start("node-a")
	nodeB := site.start("node-b")

	// Three distinct claims for one key: two machines, and two proposals on one
	// of them. Exactly one of them can hold the key.
	claims := []struct {
		on      *instance
		request api.RegistrationRequest
	}{
		{nodeA, api.RegistrationRequest{UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "First"}},
		{nodeA, api.RegistrationRequest{UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "Second"}},
		{nodeB, api.RegistrationRequest{UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "First"}},
	}
	const attempts = 30
	results := make(chan error, attempts)
	var group sync.WaitGroup
	for i := range attempts {
		claim := claims[i%len(claims)]
		group.Go(func() {
			_, err := claim.on.service.Create(t.Context(), claim.request)
			results <- err
		})
	}
	group.Wait()
	close(results)

	var taken int
	for err := range results {
		if err == nil {
			taken++
			continue
		}
		require.ErrorIs(t, err, ErrConflict, "a losing claim is a conflict, not a failure")
	}
	require.Positive(t, taken, "some claim must have been taken")

	// Whoever won, the site holds one whole claim, and it is one of the three.
	request := site.storedRequest(nodeA, unitKey)
	require.Contains(t, []string{"First", "Second"}, request.UnitTypeNameAdvertised)
	require.Contains(t, []string{"node-a", "node-b"}, request.OriginMachine)
	require.Equal(t, fingerprintOf(request), request.Fingerprint, "the stored claim is whole")
}

// TestOneRejectionPreventsAcceptanceForGood checks a refusal is terminal: the
// request does not commit, and reconciling does not eventually override it.
func TestOneRejectionPreventsAcceptanceForGood(t *testing.T) {
	site := newSite(t, "node-a", "node-b")
	nodeA := site.start("node-a")
	site.start("node-b")
	nodeA.create(t, unitRequest())
	site.reject(nodeA, "node-b", unitKey, "unit_not_supported")

	for range 3 {
		site.reconcile()
	}

	view := nodeA.get(t, unitKey)
	require.Equal(t, api.RegistrationStatusRejected, view.Status)
	require.Equal(t, "unit_not_supported", *view.Reason)
	_, committed := site.accepted(nodeA, unitKey)
	require.False(t, committed, "a refused request has no registration record, which is what accepted means")

	// node-b already decided, so its own pass adds nothing and states nothing.
	require.Empty(t, site.running["node-b"].recorder.types())
}

// TestReconciliationIsIdempotentAfterRestart checks a process that comes back
// re-derives what it already decided instead of redoing it: every write is
// create-if-absent, so the site is where it was and the facts are stated once.
func TestReconciliationIsIdempotentAfterRestart(t *testing.T) {
	site := newSite(t, "node-a", "node-b")
	nodeA := site.start("node-a")
	site.start("node-b")
	nodeA.create(t, unitRequest())
	site.reconcile()
	require.Equal(t, api.RegistrationStatusAccepted, nodeA.get(t, unitKey).Status)
	before := nodeA.recorder.types()

	restarted := site.restart("node-a")
	site.reconcile()
	site.reconcile()

	require.Equal(t, before, restarted.recorder.types(),
		"a restarted process restates nothing: the transitions already happened")
	view := restarted.get(t, unitKey)
	require.Equal(t, api.RegistrationStatusAccepted, view.Status)
	require.Equal(t, "node-a", view.Machine, "the site's state outlived the process that took the request")
	require.Len(t, site.decisions(restarted, unitKey), 2, "a restart does not duplicate a confirmation")
}

// TestShutdownEndsScansWithBoundedErrors checks a reconciler on a fabric that is
// going away fails and says why, rather than hanging or pretending.
func TestShutdownEndsScansWithBoundedErrors(t *testing.T) {
	site := newSite(t, "node-a", "node-b")
	nodeA := site.start("node-a")
	nodeA.create(t, unitRequest())

	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, nodeA.reconciler.Reconcile(canceled), context.Canceled)
	require.ErrorIs(t, nodeA.reconciler.Run(canceled, time.Second, nil), nil,
		"a loop asked to stop has stopped, which is not a failure")

	require.NoError(t, nodeA.fabric.Close(t.Context()))
	require.ErrorIs(t, nodeA.reconciler.Reconcile(t.Context()), fabric.ErrClosed)
	_, _, err := nodeA.service.Get(t.Context(), unitKey)
	require.ErrorIs(t, err, fabric.ErrClosed)
	_, err = nodeA.service.List(t.Context())
	require.ErrorIs(t, err, fabric.ErrClosed)
}

// TestRunRejectsANonPositiveInterval checks a schedule that would never fire is
// refused at startup rather than silently leaving every request pending.
func TestRunRejectsANonPositiveInterval(t *testing.T) {
	site := newSite(t, "node-a")
	nodeA := site.start("node-a")
	require.ErrorContains(t, nodeA.reconciler.Run(t.Context(), 0, nil), "not positive")
}

// TestOpenValidatesItsDeployment checks the platform refuses to register
// anything on a deployment it cannot trust: the expected membership is the
// acceptance set, so a member it could not recognize is a startup failure rather
// than a surprise on some later request.
func TestOpenValidatesItsDeployment(t *testing.T) {
	descriptor := newSite(t, "node-a", "node-b").descriptorFor("node-a")

	_, _, err := Open(memory.Open(descriptor, "primary"), nil)
	require.ErrorContains(t, err, "recorder is required")

	_, _, err = Open(nil, events.NopRecorder{})
	require.ErrorContains(t, err, "fabric is required")

	broken := descriptor
	broken.Fabric.Peers = []deployment.FabricPeer{{Site: testSite, Machine: "node-b", IP: "not-an-ip"}}
	_, _, err = Open(memory.Open(broken, "primary"), events.NopRecorder{})
	require.ErrorContains(t, err, "node-b")
	require.ErrorContains(t, err, "IP")
}

// TestCreateValidatesTheRequest checks a request that is not valid is refused
// before it can claim a key.
func TestCreateValidatesTheRequest(t *testing.T) {
	site := newSite(t, "node-a")
	nodeA := site.start("node-a")

	_, err := nodeA.service.Create(t.Context(), api.RegistrationRequest{UnitType: 1, UnitID: 2, UnitTypeNameAdvertised: " "})
	require.ErrorContains(t, err, "blank")

	invalid := "master"
	_, err = nodeA.service.Create(t.Context(), api.RegistrationRequest{UnitType: 1, UnitID: 2, UnitTypeNameAdvertised: "Worker", Role: &invalid})
	require.ErrorContains(t, err, "role")

	// Neither attempt claimed the key, so it is still free.
	require.Empty(t, nodeA.list(t))
	require.Equal(t, CreateResultNew, nodeA.create(t, api.RegistrationRequest{UnitType: 1, UnitID: 2, UnitTypeNameAdvertised: "Worker"}))
}

// TestBothRolesAreAccepted checks the two roles a unit may advertise survive the
// round trip through the site.
func TestBothRolesAreAccepted(t *testing.T) {
	for _, role := range []string{api.RoleMaster, api.RoleSlave} {
		t.Run(role, func(t *testing.T) {
			site := newSite(t, "node-a")
			nodeA := site.start("node-a")
			nodeA.create(t, api.RegistrationRequest{UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "Billing", Role: &role})
			site.reconcile()

			view := nodeA.get(t, unitKey)
			require.Equal(t, api.RegistrationStatusAccepted, view.Status)
			require.Equal(t, role, *view.Role)
		})
	}
}

// TestListIsDeterministic checks the list's order is the contract's: origin
// machine, then unit type, then unit ID, whichever machine is asked and whatever
// order the requests arrived in.
func TestListIsDeterministic(t *testing.T) {
	site := newSite(t, "node-a", "node-b")
	nodeA := site.start("node-a")
	nodeB := site.start("node-b")

	nodeB.create(t, api.RegistrationRequest{UnitType: 2, UnitID: 1, UnitTypeNameAdvertised: "B"})
	nodeA.create(t, api.RegistrationRequest{UnitType: 2, UnitID: 2, UnitTypeNameAdvertised: "A"})
	nodeA.create(t, api.RegistrationRequest{UnitType: 1, UnitID: 9, UnitTypeNameAdvertised: "A"})

	want := []Key{{UnitType: 1, UnitID: 9}, {UnitType: 2, UnitID: 2}, {UnitType: 2, UnitID: 1}}
	for _, from := range []*instance{nodeA, nodeB} {
		listed := from.list(t)
		keys := make([]Key, 0, len(listed))
		for _, view := range listed {
			keys = append(keys, Key{UnitType: view.UnitType, UnitID: view.UnitID})
			// Every instance array is ordered by machine, always in full.
			require.Equal(t, []string{"node-a", "node-b"},
				[]string{view.PlatformInstances[0].Machine, view.PlatformInstances[1].Machine})
		}
		require.Equal(t, want, keys)
	}
}

// TestListIsEmptyWithoutARequest checks an empty site lists nothing rather than
// failing or inventing an entry.
func TestListIsEmptyWithoutARequest(t *testing.T) {
	site := newSite(t, "node-a")
	require.Empty(t, site.start("node-a").list(t))
}

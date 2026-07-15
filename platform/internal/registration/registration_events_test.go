package registration

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/api"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
)

// These tests check registration by the facts it states. An event exists if and
// only if the transition that owns it happened, which is what makes the event
// log usable as evidence of behavior rather than of calls: a retry, a validation
// failure, and a repeated scan all state nothing, because none of them changed
// anything.

// TestPhaseEventsAreStatedOnceInOrder checks the three phases of a registration
// that is accepted, each stated by the machine that owns the transition.
func TestPhaseEventsAreStatedOnceInOrder(t *testing.T) {
	site := newSite(t, "node-a", "node-b")
	nodeA := site.start("node-a")
	nodeB := site.start("node-b")
	nodeA.create(t, unitRequest())
	site.reconcile()

	require.Equal(t, []events.Type{TypeRequested, TypeConfirmed, TypeAccepted}, nodeA.recorder.types(),
		"the origin took the request, answered for itself, and committed it")
	require.Equal(t, []events.Type{TypeConfirmed}, nodeB.recorder.types(),
		"a machine that is not the origin states its own answer and nothing else")

	recorded := nodeA.recorder.events()
	require.Equal(t, Requested{
		UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "Billing", Machine: "node-a", IP: "127.0.0.1",
	}, recorded[0], "the origin is the platform's own descriptor identity, never the client's claim")
	require.Equal(t, Confirmed{
		UnitType: 7, UnitID: 42, OriginMachine: "node-a", ConfirmingMachine: "node-a",
	}, recorded[1])
	require.Equal(t, Accepted{
		UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "Billing", Machine: "node-a", IP: "127.0.0.1",
	}, recorded[2], "acceptance is stated once, by the origin, after the commit")

	require.Equal(t, Confirmed{
		UnitType: 7, UnitID: 42, OriginMachine: "node-a", ConfirmingMachine: "node-b",
	}, nodeB.recorder.events()[0], "a confirmation names who answered and whose request it was")
}

// TestAcceptedIsStatedOnlyAfterEveryInstanceHasConfirmed checks the accepted
// event follows the commit rather than the last confirmation that enabled it.
func TestAcceptedIsStatedOnlyAfterEveryInstanceHasConfirmed(t *testing.T) {
	site := newSite(t, "node-a", "node-b")
	nodeA := site.start("node-a")
	nodeA.create(t, unitRequest())
	site.reconcile()
	require.Equal(t, []events.Type{TypeRequested, TypeConfirmed}, nodeA.recorder.types(),
		"the origin has answered for itself, but the site has not accepted anything")

	site.start("node-b")
	site.reconcile()
	require.Equal(t, []events.Type{TypeRequested, TypeConfirmed, TypeAccepted}, nodeA.recorder.types())
}

// TestRejectionIsStatedAsAWarning checks a refusal is reported by the machine
// that refused, with the bounded reason a client can act on.
func TestRejectionIsStatedAsAWarning(t *testing.T) {
	site := newSite(t, "node-a")
	nodeA := site.start("node-a")

	// A proposal whose fingerprint is not the one its content produces: this
	// machine cannot trust it to be what it says, so it refuses it.
	tampered := requestRecord{
		Version: recordVersion, UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "Billing",
		OriginMachine: "node-a", OriginIP: "127.0.0.1",
		Fingerprint: "0000000000000000000000000000000000000000000000000000000000000000",
	}
	value, err := encode(tampered)
	require.NoError(t, err)
	created, err := nodeA.service.store.contenderRecords.Create(context.Background(), contenderKey(unitKey, tampered.Fingerprint), value)
	require.NoError(t, err)
	require.True(t, created)

	site.reconcile()

	recorded := nodeA.recorder.events()
	require.Len(t, recorded, 1)
	require.Equal(t, Rejected{
		UnitType: 7, UnitID: 42, OriginMachine: "node-a", RejectingMachine: "node-a",
		Reason: ReasonFingerprintMismatch,
	}, recorded[0])
	require.Equal(t, []string{events.TagWarning}, recorded[0].(events.Tagged).Tags())

	view := nodeA.get(t, unitKey)
	require.Equal(t, api.RegistrationStatusRejected, view.Status)
	require.Equal(t, ReasonFingerprintMismatch, *view.Reason)
}

// TestValidationRefusesUntrustworthyProposals checks what an instance refuses,
// and that its verdict is a function of the record and the deployment rather
// than of anything it observed.
func TestValidationRefusesUntrustworthyProposals(t *testing.T) {
	valid := requestRecord{
		Version: recordVersion, UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "Billing",
		OriginMachine: "node-a", OriginIP: "127.0.0.1",
	}
	tests := []struct {
		name string
		// break_ mutates a valid proposal into one this instance must refuse.
		break_ func(requestRecord) requestRecord
		reason string
	}{
		{
			name:   "a record from an encoding this instance does not know",
			break_: func(r requestRecord) requestRecord { r.Version = recordVersion + 1; return r },
			reason: ReasonUnsupportedVersion,
		},
		{
			name: "a proposal whose own fields are not valid",
			break_: func(r requestRecord) requestRecord {
				r.UnitTypeNameAdvertised = " "
				r.Fingerprint = fingerprintOf(r)
				return r
			},
			reason: ReasonInvalidProposal,
		},
		{
			name:   "a record whose fingerprint is not its content's",
			break_: func(r requestRecord) requestRecord { r.Fingerprint = "deadbeef"; return r },
			reason: ReasonFingerprintMismatch,
		},
		{
			name: "an origin this site has no platform instance on",
			break_: func(r requestRecord) requestRecord {
				r.OriginMachine = "node-z"
				r.Fingerprint = fingerprintOf(r)
				return r
			},
			reason: ReasonUnknownOrigin,
		},
		{
			name: "an origin at an IP the deployment does not put it at",
			break_: func(r requestRecord) requestRecord {
				r.OriginIP = "127.0.0.9"
				r.Fingerprint = fingerprintOf(r)
				return r
			},
			reason: ReasonUnknownOrigin,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			site := newSite(t, "node-a", "node-b")
			nodeA := site.start("node-a")

			proposal := valid
			proposal.Fingerprint = fingerprintOf(proposal)
			require.Empty(t, nodeA.reconciler.validate(proposal),
				"the unbroken proposal must be acceptable")
			require.Equal(t, test.reason, nodeA.reconciler.validate(test.break_(proposal)))
		})
	}
}

// TestASettledRegistrationIsLeftAlone checks a pass does no work for a
// registration that is committed. It cannot change, so touching it again would
// make every scan cost what the site has ever registered rather than what is
// still undecided.
func TestASettledRegistrationIsLeftAlone(t *testing.T) {
	site := newSite(t, "node-a")
	nodeA := site.start("node-a")
	nodeA.create(t, unitRequest())
	site.reconcile()
	require.Equal(t, api.RegistrationStatusAccepted, nodeA.get(t, unitKey).Status)

	// A recorder that fails on any further event, and a fabric closed underneath
	// the confirmations: a pass that touched a settled registration would have
	// to read or write, and would fail. It does neither.
	nodeA.recorder.err = errors.New("sink is gone")
	nodeA.recorder.failAfter = len(nodeA.recorder.types())
	require.NoError(t, nodeA.reconciler.Reconcile(context.Background()))
	require.NoError(t, nodeA.reconciler.Reconcile(context.Background()))
}

// TestKeyConflictIsStatedForEveryAttempt checks the conflict warning carries
// both sides of the difference, and is stated every time. The refused attempt
// changes nothing, so the event is the only trace it ever existed.
func TestKeyConflictIsStatedForEveryAttempt(t *testing.T) {
	site := newSite(t, "node-a", "node-b")
	nodeA := site.start("node-a")
	nodeB := site.start("node-b")

	role := api.RoleMaster
	nodeA.create(t, api.RegistrationRequest{UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "First", Role: &role})

	attempt := api.RegistrationRequest{UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "Second"}
	for range 2 {
		_, err := nodeB.service.Create(context.Background(), attempt)
		require.ErrorIs(t, err, ErrConflict)
	}

	require.Equal(t, []events.Type{TypeConflict, TypeConflict}, nodeB.recorder.types(),
		"every rejected occurrence is stated, not just the first")
	require.Equal(t, Conflict{
		UnitType: 7, UnitID: 42,
		Existing:  Fields{UnitTypeNameAdvertised: "First", Role: "Master", Machine: "node-a", IP: "127.0.0.1"},
		Attempted: Fields{UnitTypeNameAdvertised: "Second", Machine: "node-b", IP: "127.0.0.2"},
		Reason:    ReasonKeyConflict,
	}, nodeB.recorder.events()[0])

	require.Equal(t, []events.Type{TypeRequested}, nodeA.recorder.types(),
		"the machine holding the key was not involved and states nothing")
}

// TestRejectedRequestsStateNothingOnAValidationFailure checks a request the
// platform never stored states nothing: there was no transition to report.
func TestRejectedRequestsStateNothingOnAValidationFailure(t *testing.T) {
	site := newSite(t, "node-a")
	nodeA := site.start("node-a")

	_, err := nodeA.service.Create(context.Background(), api.RegistrationRequest{UnitType: 1, UnitID: 2, UnitTypeNameAdvertised: ""})
	require.Error(t, err)
	require.Empty(t, nodeA.recorder.types())
}

// TestCreateReportsARecordingFailureWithContext pins the trade-off: when a
// transition cannot be reported, the caller is told the platform failed. The
// store is not rolled back to match, so the transition outlives its lost event.
func TestCreateReportsARecordingFailureWithContext(t *testing.T) {
	site := newSite(t, "node-a")
	nodeA := site.start("node-a")
	nodeA.recorder.err = errors.New("sink is gone")

	_, err := nodeA.service.Create(context.Background(), unitRequest())
	require.ErrorContains(t, err, "registration: record platform.registration.requested")
	require.ErrorContains(t, err, "sink is gone")
	require.NotErrorIs(t, err, ErrConflict, "a recording failure must not read as a client conflict")

	nodeA.recorder.err = nil
	require.Equal(t, CreateResultRetry, nodeA.create(t, unitRequest()),
		"the request was stored: only saying so failed")
}

// TestConflictReportsARecordingFailureInsteadOfTheConflict pins the same
// trade-off where it costs the most: a clean 409 would be a nicer answer, but
// the refused attempt would then leave no trace anywhere at all.
func TestConflictReportsARecordingFailureInsteadOfTheConflict(t *testing.T) {
	site := newSite(t, "node-a")
	nodeA := site.start("node-a")
	nodeA.create(t, unitRequest())

	nodeA.recorder.err = errors.New("sink is gone")
	nodeA.recorder.failAfter = len(nodeA.recorder.types())

	_, err := nodeA.service.Create(context.Background(), api.RegistrationRequest{
		UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "Payments",
	})
	require.ErrorContains(t, err, "registration: record platform.registration.conflict")
	require.ErrorContains(t, err, "sink is gone")
	require.NotErrorIs(t, err, ErrConflict)
}

// TestReconcileReportsARecordingFailureWithContext checks the reconciler makes
// the same trade-off as the service, and reports which pass failed.
func TestReconcileReportsARecordingFailureWithContext(t *testing.T) {
	site := newSite(t, "node-a")
	nodeA := site.start("node-a")
	nodeA.create(t, unitRequest())

	nodeA.recorder.err = errors.New("sink is gone")
	nodeA.recorder.failAfter = len(nodeA.recorder.types())

	err := nodeA.reconciler.Reconcile(context.Background())
	require.ErrorContains(t, err, "registration: reconcile 7/42")
	require.ErrorContains(t, err, "registration: record platform.registration.confirmed")
	require.ErrorContains(t, err, "sink is gone")
}

// TestReconcileReportsEveryFailingRequest checks one unreadable record cannot
// stop the site from deciding everything else, and that nothing is swallowed to
// achieve that.
func TestReconcileReportsEveryFailingRequest(t *testing.T) {
	site := newSite(t, "node-a")
	nodeA := site.start("node-a")

	// Two records that are not this package's, under keys that are.
	for _, key := range []Key{{UnitType: 1, UnitID: 1}, {UnitType: 2, UnitID: 2}} {
		created, err := nodeA.service.store.contenderRecords.Create(context.Background(), contenderKey(key, "corrupt"), []byte("not a record"))
		require.NoError(t, err)
		require.True(t, created)
	}
	nodeA.create(t, unitRequest())

	err := nodeA.reconciler.Reconcile(context.Background())
	require.Error(t, err)

	// The healthy request was decided anyway, in the same pass that failed.
	require.Equal(t, api.RegistrationStatusAccepted, nodeA.get(t, unitKey).Status)
}

// TestReasonsAreBoundedAndMachineReadable checks every reason this package can
// state is one a client can act on: short, and made of the characters a code is
// made of rather than of prose.
func TestReasonsAreBoundedAndMachineReadable(t *testing.T) {
	reasons := []string{
		ReasonKeyConflict,
		ReasonUnsupportedVersion,
		ReasonInvalidProposal,
		ReasonFingerprintMismatch,
		ReasonUnknownOrigin,
	}
	for _, reason := range reasons {
		t.Run(reason, func(t *testing.T) {
			require.NotEmpty(t, reason)
			require.LessOrEqual(t, len(reason), 64, "a reason is bounded")
			for _, r := range reason {
				require.True(t,
					(r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.',
					"reason %q is not machine-readable at %q", reason, r)
			}
		})
	}
}

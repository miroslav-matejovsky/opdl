package registration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/miroslav-matejovsky/opdl/platform/internal/eventfabric"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
)

// Projection is one node's registration read model, rebuilt by folding the site
// journal in order. It is the authoritative source of every registration query
// answer once a node has caught up; nothing queries shared state.
//
// It is safe for concurrent use: Apply takes the write lock, queries take the
// read lock. Apply is pure, ordered by journal sequence, and idempotent by event
// identity, so replaying the whole journal from empty rebuilds the same views
// and a redelivered event changes nothing.
type Projection struct {
	mu sync.RWMutex

	// sequence is the highest journal sequence applied so far.
	sequence uint64
	// applyErr is the first permanent projection failure. Waiters receive it
	// instead of waiting for a sequence this projection can never apply.
	applyErr error
	// changed is closed and replaced whenever sequence or applyErr changes.
	changed chan struct{}
	// proposals holds every distinct proposal by its ID.
	proposals map[string]Proposed
	// proposalSequences holds the first journal sequence of each proposal.
	proposalSequences map[string]uint64
	// claims records which proposal claimed each unit key, and at what journal
	// sequence, so the earliest claim wins regardless of apply order.
	claims map[Key]claim
	// contenders lists every proposal ID seen for a unit key, in first-seen
	// order, so a conflict reports each side.
	contenders map[Key][]string
	// decisions holds each proposal's decisions by decision ID, collapsing
	// redelivered decisions onto one entry.
	decisions map[string]map[string]decision
	// accepted holds each accepted proposal by its ID.
	accepted map[string]Accepted
}

// claim is the proposal that holds a unit key and the sequence it claimed it at.
type claim struct {
	proposalID string
	sequence   uint64
}

// decision is one node's collapsed decision about a proposal.
type decision struct {
	machine string
	kind    DecisionKind
	reason  string
}

// Key is the site-local identity of a registration: a unit type and a unit ID.
type Key struct {
	// UnitType is the unit type identifier.
	UnitType uint8
	// UnitID is the unit identifier within UnitType.
	UnitID uint16
}

// String renders the key as unit_type/unit_id.
func (k Key) String() string {
	return strconv.FormatUint(uint64(k.UnitType), 10) + "/" + strconv.FormatUint(uint64(k.UnitID), 10)
}

// Status is a proposal's projected registration status.
type Status string

const (
	// StatusPending is a claiming proposal that is neither fully accepted nor
	// rejected yet.
	StatusPending Status = "pending"
	// StatusAccepted is a proposal the origin has committed.
	StatusAccepted Status = "accepted"
	// StatusRejected is a proposal a node refused, or one that lost its key.
	StatusRejected Status = "rejected"
)

// Conflict is a resolved contention over one unit key: the proposal that claimed
// it and the proposals that lost.
type Conflict struct {
	// Key is the contested unit key.
	Key Key
	// Winner is the proposal ID that claimed the key first in journal order.
	Winner string
	// Losers are the other proposal IDs, sorted.
	Losers []string
}

// NewProjection builds an empty registration read model.
func NewProjection() *Projection {
	return &Projection{
		proposals:         make(map[string]Proposed),
		proposalSequences: make(map[string]uint64),
		claims:            make(map[Key]claim),
		contenders:        make(map[Key][]string),
		decisions:         make(map[string]map[string]decision),
		accepted:          make(map[string]Accepted),
		changed:           make(chan struct{}),
	}
}

// Projection applies journal deliveries, so it is an Event Fabric projector.
var _ eventfabric.Projector = (*Projection)(nil)

// Apply folds one journal delivery into the read model. It decodes the payload
// for the delivery's event type and dispatches to the matching reducer. An
// unknown registration event or an undecodable payload is reported so catch-up
// stops rather than leaving a gap. Events outside registration are ignored but
// still advance the sequence because one node-wide projector carries all
// domains.
func (p *Projection) Apply(ctx context.Context, delivery eventfabric.Delivery) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.RLock()
	applyErr := p.applyErr
	p.mu.RUnlock()
	if applyErr != nil {
		return applyErr
	}
	if delivery.Sequence == 0 {
		return p.fail(errors.New("registration: delivery sequence must be positive"))
	}

	envelope := delivery.Envelope
	// Another domain's fact advances the projection's position and nothing else.
	// The source is derived from the event type, so this asks the envelope which
	// domain stated it rather than matching a name.
	if envelope.Type.Source() != eventSource {
		p.advance(delivery.Sequence)
		return nil
	}
	if envelope.SchemaVersion != schemaVersion {
		return p.fail(fmt.Errorf("registration: unsupported schema version %d for %s at sequence %d", envelope.SchemaVersion, envelope.Type, delivery.Sequence))
	}

	var err error
	switch envelope.Type {
	case TypeProposed:
		var event Proposed
		if err = decode(envelope, &event); err == nil {
			err = p.applyProposed(delivery, event)
		}
	case TypeConfirmed:
		var event Confirmed
		if err = decode(envelope, &event); err == nil {
			err = p.applyDecision(delivery, event.ProposalID, decision{machine: event.DecidingMachine, kind: DecisionConfirmed}, event.DecisionID)
		}
	case TypeRejected:
		var event Rejected
		if err = decode(envelope, &event); err == nil {
			err = p.applyDecision(delivery, event.ProposalID, decision{machine: event.DecidingMachine, kind: DecisionRejected, reason: event.Reason}, event.DecisionID)
		}
	case TypeAccepted:
		var event Accepted
		if err = decode(envelope, &event); err == nil {
			err = p.applyAccepted(delivery, event)
		}
	default:
		err = fmt.Errorf("registration: unsupported event %q at sequence %d", envelope.Type, delivery.Sequence)
	}
	if err != nil {
		return p.fail(err)
	}
	p.advance(delivery.Sequence)
	return nil
}

// applyProposed records a distinct proposal and resolves the key claim. An
// identical proposal ID already seen is an idempotent retry; a different one for
// a claimed key becomes a contender, and the lowest journal sequence keeps the
// claim.
func (p *Projection) applyProposed(delivery eventfabric.Delivery, event Proposed) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := validateProposed(delivery, event); err != nil {
		return err
	}

	if _, seen := p.proposals[event.ProposalID]; seen {
		return nil
	}
	p.proposals[event.ProposalID] = cloneProposed(event)
	p.proposalSequences[event.ProposalID] = delivery.Sequence

	key := event.Key()
	p.contenders[key] = append(p.contenders[key], event.ProposalID)
	if current, claimed := p.claims[key]; !claimed || delivery.Sequence < current.sequence {
		p.claims[key] = claim{proposalID: event.ProposalID, sequence: delivery.Sequence}
	}
	return nil
}

// applyDecision records one node's decision about a proposal, collapsing a
// redelivered decision onto the entry its decision ID already holds.
func (p *Projection) applyDecision(delivery eventfabric.Delivery, proposalID string, decided decision, decisionID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	proposal, ok := p.proposals[proposalID]
	if !ok {
		return fmt.Errorf("registration: decision %q refers to unknown proposal %q", decisionID, proposalID)
	}
	if !slices.Contains(proposal.ExpectedMachines, decided.machine) {
		return fmt.Errorf("registration: decision %q is from unexpected machine %q", decisionID, decided.machine)
	}
	if delivery.Envelope.Origin.Machine != decided.machine {
		return fmt.Errorf("registration: decision %q claims machine %q but envelope states %q", decisionID, decided.machine, delivery.Envelope.Origin.Machine)
	}
	if decisionID != NewDecisionID(proposalID, decided.kind, decided.machine) {
		return fmt.Errorf("registration: decision %q has an invalid identity", decisionID)
	}
	if selected := p.claims[proposal.Key()].proposalID; decided.kind == DecisionConfirmed && selected != proposalID {
		return fmt.Errorf("registration: confirmation %q targets losing proposal, selected is %q", decisionID, selected)
	}
	if decided.kind == DecisionRejected && strings.TrimSpace(decided.reason) == "" {
		return fmt.Errorf("registration: rejection %q has no reason", decisionID)
	}
	if _, accepted := p.accepted[proposalID]; accepted && decided.kind == DecisionRejected {
		return fmt.Errorf("registration: proposal %q was rejected after acceptance", proposalID)
	}

	byID, ok := p.decisions[proposalID]
	if !ok {
		byID = make(map[string]decision)
		p.decisions[proposalID] = byID
	}
	for existingID, existing := range byID {
		if existing.machine == decided.machine && existing != decided {
			return fmt.Errorf("registration: machine %q made conflicting decisions %q and %q for proposal %q", decided.machine, existingID, decisionID, proposalID)
		}
	}
	byID[decisionID] = decided
	return nil
}

// applyAccepted records the origin's commit of a proposal.
func (p *Projection) applyAccepted(delivery eventfabric.Delivery, event Accepted) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	proposal, ok := p.proposals[event.ProposalID]
	if !ok {
		return fmt.Errorf("registration: acceptance refers to unknown proposal %q", event.ProposalID)
	}
	if delivery.Envelope.Origin.Machine != proposal.OriginMachine {
		return fmt.Errorf("registration: acceptance for %q was stated by %q, not origin %q", event.ProposalID, delivery.Envelope.Origin.Machine, proposal.OriginMachine)
	}
	if event != NewAccepted(proposal) {
		return fmt.Errorf("registration: acceptance for %q does not match its proposal", event.ProposalID)
	}
	if selected := p.claims[proposal.Key()].proposalID; selected != event.ProposalID {
		return fmt.Errorf("registration: acceptance for %q targets losing proposal, selected is %q", event.ProposalID, selected)
	}
	if !p.allExpectedConfirmed(proposal) {
		return fmt.Errorf("registration: acceptance for %q precedes all expected confirmations", event.ProposalID)
	}
	p.accepted[event.ProposalID] = event
	return nil
}

// Sequence returns the highest journal sequence the projection has applied.
func (p *Projection) Sequence() uint64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.sequence
}

// WaitApplied blocks until the projection has applied sequence. It returns the
// first projection failure or the caller's cancellation instead of waiting for
// progress that cannot happen.
func (p *Projection) WaitApplied(ctx context.Context, sequence uint64) error {
	for {
		p.mu.RLock()
		applied, applyErr, changed := p.sequence, p.applyErr, p.changed
		p.mu.RUnlock()
		if applyErr != nil {
			return applyErr
		}
		if applied >= sequence {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

// Proposal returns a copy of a proposal by ID.
func (p *Projection) Proposal(proposalID string) (Proposed, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	proposal, ok := p.proposals[proposalID]
	return cloneProposed(proposal), ok
}

// Proposals returns copies of every proposal in journal order.
func (p *Projection) Proposals() []Proposed {
	p.mu.RLock()
	defer p.mu.RUnlock()
	proposals := make([]Proposed, 0, len(p.proposals))
	for _, proposal := range p.proposals {
		proposals = append(proposals, cloneProposed(proposal))
	}
	slices.SortFunc(proposals, func(a, b Proposed) int {
		if left, right := p.proposalSequences[a.ProposalID], p.proposalSequences[b.ProposalID]; left != right {
			if left < right {
				return -1
			}
			return 1
		}
		return strings.Compare(a.ProposalID, b.ProposalID)
	})
	return proposals
}

// ProposalSequence returns the proposal's first journal sequence.
func (p *Projection) ProposalSequence(proposalID string) (uint64, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	sequence, ok := p.proposalSequences[proposalID]
	return sequence, ok
}

// SelectedProposal returns the proposal ID that claimed key, if any proposal
// has.
func (p *Projection) SelectedProposal(key Key) (string, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	current, claimed := p.claims[key]
	if !claimed {
		return "", false
	}
	return current.proposalID, true
}

// ProposalStatus returns the projected status of a proposal, and whether the
// projection has seen it. Acceptance wins over rejection, which wins over
// pending; a proposal that lost its key is rejected as a conflict.
func (p *Projection) ProposalStatus(proposalID string) (Status, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	proposal, seen := p.proposals[proposalID]
	if !seen {
		return "", false
	}
	if _, ok := p.accepted[proposalID]; ok {
		return StatusAccepted, true
	}
	if current, claimed := p.claims[proposal.Key()]; claimed && current.proposalID != proposalID {
		return StatusRejected, true
	}
	if p.hasRejection(proposalID) {
		return StatusRejected, true
	}
	return StatusPending, true
}

// Confirmations returns the sorted machines that have confirmed a proposal.
func (p *Projection) Confirmations(proposalID string) []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.machinesByKind(proposalID, DecisionConfirmed)
}

// Rejections returns the machines that rejected a proposal mapped to their
// reasons.
func (p *Projection) Rejections(proposalID string) map[string]string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	reasons := make(map[string]string)
	for _, decided := range p.decisions[proposalID] {
		if decided.kind == DecisionRejected {
			reasons[decided.machine] = decided.reason
		}
	}
	return reasons
}

// AllExpectedConfirmed reports whether every expected machine of a proposal has
// confirmed it. It is false for a proposal the projection has not seen.
func (p *Projection) AllExpectedConfirmed(proposalID string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	proposal, seen := p.proposals[proposalID]
	if !seen {
		return false
	}
	return p.allExpectedConfirmed(proposal)
}

// IsAccepted reports whether a proposal has been committed.
func (p *Projection) IsAccepted(proposalID string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	_, ok := p.accepted[proposalID]
	return ok
}

// advance records successful progress and wakes sequence waiters.
func (p *Projection) advance(sequence uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if sequence <= p.sequence {
		return
	}
	p.sequence = sequence
	close(p.changed)
	p.changed = make(chan struct{})
}

// fail records the first permanent projection error and wakes sequence waiters.
func (p *Projection) fail(err error) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.applyErr == nil {
		p.applyErr = err
		close(p.changed)
		p.changed = make(chan struct{})
	}
	return err
}

// allExpectedConfirmed reports whether every expected machine confirmed
// proposal. The caller holds at least the read lock.
func (p *Projection) allExpectedConfirmed(proposal Proposed) bool {
	confirmed := p.machinesByKind(proposal.ProposalID, DecisionConfirmed)
	for _, machine := range proposal.ExpectedMachines {
		if !slices.Contains(confirmed, machine) {
			return false
		}
	}
	return true
}

// Conflicts returns every unit key that has more than one proposal, resolved
// into a winner and its losers, ordered by unit key.
func (p *Projection) Conflicts() []Conflict {
	p.mu.RLock()
	defer p.mu.RUnlock()

	conflicts := make([]Conflict, 0)
	for key, proposalIDs := range p.contenders {
		if len(proposalIDs) < 2 {
			continue
		}
		winner := p.claims[key].proposalID
		losers := make([]string, 0, len(proposalIDs)-1)
		for _, proposalID := range proposalIDs {
			if proposalID != winner {
				losers = append(losers, proposalID)
			}
		}
		slices.Sort(losers)
		conflicts = append(conflicts, Conflict{Key: key, Winner: winner, Losers: losers})
	}
	slices.SortFunc(conflicts, func(a, b Conflict) int {
		return compareKeys(a.Key, b.Key)
	})
	return conflicts
}

// hasRejection reports whether any node rejected a proposal. The caller holds
// the read lock.
func (p *Projection) hasRejection(proposalID string) bool {
	for _, decided := range p.decisions[proposalID] {
		if decided.kind == DecisionRejected {
			return true
		}
	}
	return false
}

// machinesByKind returns the sorted, de-duplicated machines that decided a
// proposal a given way. The caller holds the read lock.
func (p *Projection) machinesByKind(proposalID string, kind DecisionKind) []string {
	machines := make([]string, 0)
	for _, decided := range p.decisions[proposalID] {
		if decided.kind == kind && !slices.Contains(machines, decided.machine) {
			machines = append(machines, decided.machine)
		}
	}
	slices.Sort(machines)
	return machines
}

// compareKeys orders two keys by unit type then unit ID.
func compareKeys(a, b Key) int {
	if a.UnitType != b.UnitType {
		return int(a.UnitType) - int(b.UnitType)
	}
	return int(a.UnitID) - int(b.UnitID)
}

// decode reads one event payload from an envelope, reporting a decode failure
// with the event type for context.
func decode(envelope events.Envelope, target events.Event) error {
	if err := json.Unmarshal(envelope.Data, target); err != nil {
		return fmt.Errorf("registration: decode %s: %w", envelope.Type, err)
	}
	return nil
}

func validateProposed(delivery eventfabric.Delivery, proposal Proposed) error {
	if delivery.Envelope.Origin.Machine != proposal.OriginMachine {
		return fmt.Errorf("registration: proposal %q claims origin %q but envelope states %q", proposal.ProposalID, proposal.OriginMachine, delivery.Envelope.Origin.Machine)
	}
	if len(proposal.ExpectedMachines) == 0 {
		return fmt.Errorf("registration: proposal %q expects no machines", proposal.ProposalID)
	}
	for i, machine := range proposal.ExpectedMachines {
		if strings.TrimSpace(machine) == "" {
			return fmt.Errorf("registration: proposal %q has a blank expected machine", proposal.ProposalID)
		}
		if i > 0 && proposal.ExpectedMachines[i-1] >= machine {
			return fmt.Errorf("registration: proposal %q expected machines are not sorted and unique", proposal.ProposalID)
		}
	}
	want := NewProposalID(ProposalIdentity{
		UnitType:               proposal.UnitType,
		UnitID:                 proposal.UnitID,
		UnitTypeNameAdvertised: proposal.UnitTypeNameAdvertised,
		Role:                   proposal.Role,
		OriginMachine:          proposal.OriginMachine,
		OriginIP:               proposal.OriginIP,
		ExpectedMachines:       proposal.ExpectedMachines,
	})
	if proposal.ProposalID != want {
		return fmt.Errorf("registration: proposal %q has an invalid identity", proposal.ProposalID)
	}
	return nil
}

func cloneProposed(proposal Proposed) Proposed {
	proposal.ExpectedMachines = slices.Clone(proposal.ExpectedMachines)
	return proposal
}

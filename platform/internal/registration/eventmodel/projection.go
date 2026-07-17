package eventmodel

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
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
	// proposals holds every distinct proposal by its ID.
	proposals map[string]Proposed
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
		proposals:  make(map[string]Proposed),
		claims:     make(map[Key]claim),
		contenders: make(map[Key][]string),
		decisions:  make(map[string]map[string]decision),
		accepted:   make(map[string]Accepted),
	}
}

// Projection applies journal deliveries, so it is an Event Fabric projector.
var _ eventfabric.Projector = (*Projection)(nil)

// Apply folds one journal delivery into the read model. It decodes the payload
// for the delivery's event type and dispatches to the matching reducer. An
// unknown event type or an undecodable payload is reported so catch-up stops
// rather than leaving a gap; a canceled context is honored before any work.
func (p *Projection) Apply(ctx context.Context, delivery eventfabric.Delivery) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	record := delivery.Record
	switch record.Type {
	case TypeProposed:
		var event Proposed
		if err := decode(record, &event); err != nil {
			return err
		}
		p.applyProposed(delivery.Sequence, event)
	case TypeConfirmed:
		var event Confirmed
		if err := decode(record, &event); err != nil {
			return err
		}
		p.applyDecision(event.ProposalID, decision{machine: event.DecidingMachine, kind: DecisionConfirmed}, event.DecisionID)
	case TypeRejected:
		var event Rejected
		if err := decode(record, &event); err != nil {
			return err
		}
		p.applyDecision(event.ProposalID, decision{machine: event.DecidingMachine, kind: DecisionRejected, reason: event.Reason}, event.DecisionID)
	case TypeAccepted:
		var event Accepted
		if err := decode(record, &event); err != nil {
			return err
		}
		p.applyAccepted(event)
	default:
		return fmt.Errorf("eventmodel: unsupported event %q at sequence %d", record.Type, delivery.Sequence)
	}

	p.mu.Lock()
	if delivery.Sequence > p.sequence {
		p.sequence = delivery.Sequence
	}
	p.mu.Unlock()
	return nil
}

// applyProposed records a distinct proposal and resolves the key claim. An
// identical proposal ID already seen is an idempotent retry; a different one for
// a claimed key becomes a contender, and the lowest journal sequence keeps the
// claim.
func (p *Projection) applyProposed(sequence uint64, event Proposed) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, seen := p.proposals[event.ProposalID]; seen {
		return
	}
	p.proposals[event.ProposalID] = event

	key := event.Key()
	p.contenders[key] = append(p.contenders[key], event.ProposalID)
	if current, claimed := p.claims[key]; !claimed || sequence < current.sequence {
		p.claims[key] = claim{proposalID: event.ProposalID, sequence: sequence}
	}
}

// applyDecision records one node's decision about a proposal, collapsing a
// redelivered decision onto the entry its decision ID already holds.
func (p *Projection) applyDecision(proposalID string, decided decision, decisionID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	byID, ok := p.decisions[proposalID]
	if !ok {
		byID = make(map[string]decision)
		p.decisions[proposalID] = byID
	}
	byID[decisionID] = decided
}

// applyAccepted records the origin's commit of a proposal.
func (p *Projection) applyAccepted(event Accepted) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.accepted[event.ProposalID] = event
}

// Sequence returns the highest journal sequence the projection has applied.
func (p *Projection) Sequence() uint64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.sequence
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
	confirmed := p.machinesByKind(proposalID, DecisionConfirmed)
	for _, machine := range proposal.ExpectedMachines {
		if !slices.Contains(confirmed, machine) {
			return false
		}
	}
	return true
}

// IsAccepted reports whether a proposal has been committed.
func (p *Projection) IsAccepted(proposalID string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	_, ok := p.accepted[proposalID]
	return ok
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

// decode reads one event payload from a record, reporting a decode failure with
// the event type for context.
func decode(record events.Record, target events.Event) error {
	if err := json.Unmarshal(record.Data, target); err != nil {
		return fmt.Errorf("eventmodel: decode %s: %w", record.Type, err)
	}
	return nil
}

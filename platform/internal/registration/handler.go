package registration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/miroslav-matejovsky/opdl/platform/api"
	"github.com/miroslav-matejovsky/opdl/platform/internal/eventfabric"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
)

// Handler is one node's durable registration decision worker. It consumes only
// proposed and confirmed registration events and emits a finite consequence.
type Handler struct {
	publisher        eventfabric.Publisher
	projection       *Projection
	location         Location
	locations        map[string]Location
	expectedMachines []string
	routes           []eventfabric.Route
}

// NewHandler builds one node's registration handler with exact routes.
func NewHandler(publisher eventfabric.Publisher, projection *Projection, location Location, expected []Location, scope eventfabric.SiteScope) (*Handler, error) {
	if publisher == nil {
		return nil, errors.New("registration: handler publisher is required")
	}
	if projection == nil {
		return nil, errors.New("registration: handler projection is required")
	}
	locations, machines, err := validateTopology(location, expected)
	if err != nil {
		return nil, err
	}
	proposed, err := eventfabric.NewRoute(scope, TypeProposed)
	if err != nil {
		return nil, err
	}
	confirmed, err := eventfabric.NewRoute(scope, TypeConfirmed)
	if err != nil {
		return nil, err
	}
	return &Handler{
		publisher: publisher, projection: projection, location: location,
		locations: locations, expectedMachines: machines,
		routes: []eventfabric.Route{proposed, confirmed},
	}, nil
}

var _ eventfabric.Handler = (*Handler)(nil)

// Name returns the stable durable-consumer service token.
func (*Handler) Name() string { return "registration" }

// Routes returns copies of the exact proposed and confirmed routes consumed.
func (h *Handler) Routes() []eventfabric.Route { return slices.Clone(h.routes) }

// Handle waits for the input and its predecessors to be projected before it
// decides. A publication failure is returned so the input remains unacknowledged.
func (h *Handler) Handle(ctx context.Context, delivery eventfabric.Delivery) error {
	if err := h.projection.WaitApplied(ctx, delivery.Sequence); err != nil {
		return fmt.Errorf("registration: wait for projection sequence %d: %w", delivery.Sequence, err)
	}
	switch delivery.Record.Type {
	case TypeProposed:
		return h.handleProposed(ctx, delivery)
	case TypeConfirmed:
		return h.handleConfirmed(ctx, delivery)
	default:
		return fmt.Errorf("registration: handler received unsupported event %q", delivery.Record.Type)
	}
}

func (h *Handler) handleProposed(ctx context.Context, delivery eventfabric.Delivery) error {
	var proposed Proposed
	if err := json.Unmarshal(delivery.Record.Data, &proposed); err != nil {
		return fmt.Errorf("registration: decode proposal at %d: %w", delivery.Sequence, err)
	}
	projected, found := h.projection.Proposal(proposed.ProposalID)
	if !found {
		return fmt.Errorf("registration: proposal %q was not projected", proposed.ProposalID)
	}

	reason := ""
	if err := h.validateProposal(projected); err != nil {
		reason = ReasonInvalidProposal
	} else if selected, claimed := h.projection.SelectedProposal(projected.Key()); !claimed || selected != projected.ProposalID {
		reason = ReasonKeyConflict
	}
	var consequence events.Event
	if reason == "" {
		consequence = NewConfirmed(projected.ProposalID, h.location.Machine)
	} else {
		consequence = NewRejected(projected.ProposalID, h.location.Machine, reason)
	}
	if _, err := h.publisher.Publish(eventfabric.CausalContext(ctx, delivery), consequence); err != nil {
		return fmt.Errorf("registration: publish decision for proposal %s: %w", projected.ProposalID, err)
	}
	return nil
}

func (h *Handler) handleConfirmed(ctx context.Context, delivery eventfabric.Delivery) error {
	var confirmed Confirmed
	if err := json.Unmarshal(delivery.Record.Data, &confirmed); err != nil {
		return fmt.Errorf("registration: decode confirmation at %d: %w", delivery.Sequence, err)
	}
	proposed, found := h.projection.Proposal(confirmed.ProposalID)
	if !found || proposed.OriginMachine != h.location.Machine {
		return nil
	}
	status, _ := h.projection.ProposalStatus(proposed.ProposalID)
	if status != StatusPending || !h.projection.AllExpectedConfirmed(proposed.ProposalID) {
		return nil
	}
	if _, err := h.publisher.Publish(eventfabric.CausalContext(ctx, delivery), NewAccepted(proposed)); err != nil {
		return fmt.Errorf("registration: publish acceptance for proposal %s: %w", proposed.ProposalID, err)
	}
	return nil
}

func (h *Handler) validateProposal(proposed Proposed) error {
	role := optionalString(proposed.Role)
	if err := validateRequest(api.RegistrationRequest{
		UnitType: proposed.UnitType, UnitID: proposed.UnitID,
		UnitTypeNameAdvertised: proposed.UnitTypeNameAdvertised, Role: role,
	}); err != nil {
		return err
	}
	if !slices.Equal(proposed.ExpectedMachines, h.expectedMachines) {
		return errors.New("expected machine set does not match trusted topology")
	}
	origin, found := h.locations[proposed.OriginMachine]
	if !found || origin.IP != proposed.OriginIP {
		return errors.New("origin does not match trusted topology")
	}
	return nil
}

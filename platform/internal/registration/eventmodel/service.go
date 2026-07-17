package eventmodel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"

	"github.com/miroslav-matejovsky/opdl/platform/api"
	"github.com/miroslav-matejovsky/opdl/platform/internal/eventfabric"
)

// ErrJournalUnavailable reports that a valid command could not be durably
// appended to the site journal.
var ErrJournalUnavailable = errors.New("registration: site journal unavailable")

// Location is one trusted platform machine identity from the deployment
// descriptor. Clients never supply it.
type Location struct {
	// Machine is the immutable descriptor machine name.
	Machine string
	// IP is the immutable descriptor IP address.
	IP string
}

// CommandService validates registration commands and publishes proposals. It
// does not read state or decide conflicts.
type CommandService struct {
	publisher        eventfabric.Publisher
	location         Location
	expectedMachines []string
}

// ProposalReceipt acknowledges that the site journal durably accepted a
// registration proposal for asynchronous processing.
type ProposalReceipt struct {
	// ProposalID is the deterministic status key.
	ProposalID string
	// Sequence is the proposal's site journal position.
	Sequence uint64
}

// QueryService answers registration queries from one node-local projection.
// It never calls the Event Fabric or NATS.
type QueryService struct {
	projection *Projection
	locations  map[string]Location
}

// Open builds the command and query services for one platform node.
func Open(publisher eventfabric.Publisher, projection *Projection, location Location, expected []Location) (*CommandService, *QueryService, error) {
	if publisher == nil {
		return nil, nil, errors.New("registration: publisher is required")
	}
	if projection == nil {
		return nil, nil, errors.New("registration: projection is required")
	}
	locations, machines, err := validateTopology(location, expected)
	if err != nil {
		return nil, nil, err
	}
	return &CommandService{publisher: publisher, location: location, expectedMachines: machines},
		&QueryService{projection: projection, locations: locations}, nil
}

// Create validates request and durably publishes its proposal. A successful
// return means only that asynchronous site processing may begin.
func (s *CommandService) Create(ctx context.Context, request api.RegistrationRequest) (ProposalReceipt, error) {
	if err := validateRequest(request); err != nil {
		return ProposalReceipt{}, err
	}
	proposed := NewProposed(ProposalIdentity{
		UnitType:               request.UnitType,
		UnitID:                 request.UnitID,
		UnitTypeNameAdvertised: request.UnitTypeNameAdvertised,
		Role:                   stringValue(request.Role),
		OriginMachine:          s.location.Machine,
		OriginIP:               s.location.IP,
		ExpectedMachines:       s.expectedMachines,
	})
	receipt, err := s.publisher.Publish(ctx, proposed)
	if err != nil {
		return ProposalReceipt{}, fmt.Errorf("%w: publish proposal %s: %w", ErrJournalUnavailable, proposed.ProposalID, err)
	}
	return ProposalReceipt{ProposalID: proposed.ProposalID, Sequence: receipt.Sequence}, nil
}

// Get returns one proposal by its canonical proposal ID.
func (s *QueryService) Get(proposalID string) (api.Registration, bool) {
	proposal, found := s.projection.Proposal(proposalID)
	if !found {
		return api.Registration{}, false
	}
	return s.view(proposal), true
}

// List returns every proposal in journal order.
func (s *QueryService) List() []api.Registration {
	proposals := s.projection.Proposals()
	views := make([]api.Registration, 0, len(proposals))
	for _, proposal := range proposals {
		views = append(views, s.view(proposal))
	}
	return views
}

// Conflicts returns every projected key conflict in deterministic key order.
func (s *QueryService) Conflicts() ([]api.RegistrationConflict, error) {
	projected := s.projection.Conflicts()
	conflicts := make([]api.RegistrationConflict, 0, len(projected))
	for _, conflict := range projected {
		winner, found := s.projection.Proposal(conflict.Winner)
		if !found {
			return nil, fmt.Errorf("registration: conflict winner proposal %q is missing", conflict.Winner)
		}
		losers := make([]api.Registration, 0, len(conflict.Losers))
		for _, proposalID := range conflict.Losers {
			loser, ok := s.projection.Proposal(proposalID)
			if !ok {
				return nil, fmt.Errorf("registration: conflict loser proposal %q is missing", proposalID)
			}
			losers = append(losers, s.view(loser))
		}
		conflicts = append(conflicts, api.RegistrationConflict{
			UnitType:         conflict.Key.UnitType,
			UnitID:           conflict.Key.UnitID,
			ResolutionStatus: api.RegistrationConflictResolutionResolved,
			Winner:           s.view(winner),
			Losers:           losers,
		})
	}
	return conflicts, nil
}

func (s *QueryService) view(proposal Proposed) api.Registration {
	status, _ := s.projection.ProposalStatus(proposal.ProposalID)
	rejections := s.projection.Rejections(proposal.ProposalID)
	confirmations := s.projection.Confirmations(proposal.ProposalID)
	selected, _ := s.projection.SelectedProposal(proposal.Key())

	instances := make([]api.PlatformInstanceRegistrationStatus, 0, len(proposal.ExpectedMachines))
	for _, machine := range proposal.ExpectedMachines {
		instance := api.PlatformInstanceRegistrationStatus{Machine: machine, Status: api.RegistrationStatusPending}
		if location, ok := s.locations[machine]; ok {
			instance.IP = location.IP
		}
		if reason, rejected := rejections[machine]; rejected {
			instance.Status = api.RegistrationStatusRejected
			instance.Reason = copyString(reason)
		} else if slices.Contains(confirmations, machine) {
			instance.Status = api.RegistrationStatusAccepted
		}
		instances = append(instances, instance)
	}

	var reason *string
	if selected != proposal.ProposalID {
		reason = copyString(ReasonKeyConflict)
	} else if status == StatusRejected {
		for _, machine := range proposal.ExpectedMachines {
			if rejected, ok := rejections[machine]; ok {
				reason = copyString(rejected)
				break
			}
		}
	}
	return api.Registration{
		UnitType:               proposal.UnitType,
		UnitID:                 proposal.UnitID,
		UnitTypeNameAdvertised: proposal.UnitTypeNameAdvertised,
		Role:                   optionalString(proposal.Role),
		Machine:                proposal.OriginMachine,
		IP:                     proposal.OriginIP,
		Status:                 string(status),
		Reason:                 reason,
		PlatformInstances:      instances,
	}
}

func validateTopology(self Location, expected []Location) (locations map[string]Location, machines []string, err error) {
	if err := validateLocation(self); err != nil {
		return nil, nil, fmt.Errorf("registration: local location: %w", err)
	}
	if len(expected) == 0 {
		return nil, nil, errors.New("registration: expected machine set is empty")
	}
	locations = make(map[string]Location, len(expected))
	for _, location := range expected {
		if err := validateLocation(location); err != nil {
			return nil, nil, fmt.Errorf("registration: expected machine %q: %w", location.Machine, err)
		}
		if _, duplicate := locations[location.Machine]; duplicate {
			return nil, nil, fmt.Errorf("registration: expected machine %q is duplicated", location.Machine)
		}
		locations[location.Machine] = location
	}
	if location, found := locations[self.Machine]; !found || location != self {
		return nil, nil, fmt.Errorf("registration: local location %q is not in the expected machine set", self.Machine)
	}
	machines = make([]string, 0, len(locations))
	for machine := range locations {
		machines = append(machines, machine)
	}
	slices.Sort(machines)
	return locations, machines, nil
}

func validateRequest(request api.RegistrationRequest) error {
	if strings.TrimSpace(request.UnitTypeNameAdvertised) == "" {
		return errors.New("registration: unit type name advertised is blank")
	}
	if request.Role != nil && *request.Role != api.RoleMaster && *request.Role != api.RoleSlave {
		return fmt.Errorf("registration: role %q is invalid", *request.Role)
	}
	return nil
}

func validateLocation(location Location) error {
	if strings.TrimSpace(location.Machine) == "" {
		return errors.New("location machine is blank")
	}
	if net.ParseIP(location.IP) == nil {
		return fmt.Errorf("location IP %q is invalid", location.IP)
	}
	return nil
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return copyString(value)
}

func copyString(value string) *string {
	valueCopy := value
	return &valueCopy
}

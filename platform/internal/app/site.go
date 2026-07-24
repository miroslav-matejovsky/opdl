package app

import (
	"context"
	"github.com/miroslav-matejovsky/opdl/platform/api"
	"github.com/miroslav-matejovsky/opdl/platform/config"
	"github.com/miroslav-matejovsky/opdl/platform/internal/registration"
)

type site struct {
	fabric   progressFabric
	commands *registration.CommandService
	queries  *registration.QueryService
	stopped  chan struct{}
}

func open(ctx context.Context, proc process, active bool) (*site, error) {
	return nil, api.ErrNotImplemented
}

func (s *site) close(ctx context.Context) error {
	return nil
}

func topology(descriptor config.Descriptor) (self registration.Location, expected []registration.Location) {
	self = registration.Location{Machine: descriptor.Machine, IP: descriptor.IP}
	expected = append(expected, self)
	seen := map[string]bool{descriptor.Machine: true}
	for _, peer := range descriptor.Peers {
		if seen[peer.Machine] {
			continue
		}
		seen[peer.Machine] = true
		expected = append(expected, registration.Location{Machine: peer.Machine, IP: peer.IP})
	}
	return self, expected
}

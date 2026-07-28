package app

import (
	"context"

	"github.com/miroslav-matejovsky/opdl/platform/api"
)

type site struct {
	fabric  progressFabric
	stopped chan struct{}
}

func open(ctx context.Context, proc process, active bool) (*site, error) {
	return nil, api.ErrNotImplemented
}

func (s *site) close(ctx context.Context) error {
	return nil
}

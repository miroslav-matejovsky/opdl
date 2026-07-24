package registration

import (
	"github.com/miroslav-matejovsky/opdl/platform/api"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
)

// Handler is one node's durable registration decision worker.
type Handler struct{}

// NewHandler returns ErrNotImplemented as registration event handling is not implemented.
func NewHandler(events.Publisher, *Projection, Location, []Location) (*Handler, error) {
	return nil, api.ErrNotImplemented
}

// Name returns the stable durable-consumer service token.
func (*Handler) Name() string { return "registration" }

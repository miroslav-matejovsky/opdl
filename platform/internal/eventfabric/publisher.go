package eventfabric

import (
	"context"

	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
)

// publisher is the business-facing publisher: it stamps a typed payload with the
// process's one envelope factory and appends the result to the site journal.
//
// The split is what keeps identity out of the transport. Stamping is a property
// of the process that states the fact, so it happens once, here, before any
// adapter sees the event. An adapter receives a completed envelope and decides
// only where to put it.
type publisher struct {
	factory  events.Factory
	appender Appender
}

// NewPublisher composes the node's publisher from the process's envelope factory
// and the journal appender. Business code is handed the result and never the
// halves: it can state a fact, and it learns nothing about stamping or storage.
func NewPublisher(factory events.Factory, appender Appender) Publisher {
	return publisher{factory: factory, appender: appender}
}

// Publish stamps event and appends it, returning the journal's receipt. A
// stamping failure and an append failure are both reported: in either case the
// fact is not retained, and a caller must not assume it happened.
func (p publisher) Publish(ctx context.Context, event events.Event) (Receipt, error) {
	envelope, err := p.factory.Wrap(ctx, event)
	if err != nil {
		return Receipt{}, err
	}
	return p.appender.Append(ctx, envelope)
}

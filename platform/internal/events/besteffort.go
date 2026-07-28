package events

import (
	"context"
	"log/slog"
)

// Diagnostic is a Publisher used from a path that cannot return a publication
// failure: a background loop, a transport callback, or a defer that is already
// carrying the error worth reporting.
//
// It is deliberately a separate type rather than a mode of Publisher. A producer
// that holds one has said, in the type it stores, that it has nowhere to return
// a failure to, and a reviewer can see which call sites gave up the error
// without reading their bodies.
type Diagnostic struct{ publisher Publisher }

// BestEffort adapts publisher for the paths that cannot return a failure.
//
// Use it only there. A caller whose signature can carry the failure must return
// or join it instead, because a fact the platform meant to state and did not is
// not a detail.
func BestEffort(publisher Publisher) Diagnostic { return Diagnostic{publisher: publisher} }

// State publishes event and reports a failure to the application log.
//
// The failure goes no further. It is deliberately not restated as an event: the
// pipeline that could not carry the first one is the same pipeline a diagnostic
// about it would use, so republishing it would either fail again or, worse,
// succeed and hide that something was lost. The application log is the one
// destination that is not that pipeline.
//
// It logs through slog's default logger rather than one this package holds. A
// fact this package could not carry is not this package's business to describe;
// it goes wherever the process that composed the publisher decided its
// diagnostics go, which for a running instance is that instance's log file.
func (d Diagnostic) State(ctx context.Context, event Event) {
	if d.publisher == nil {
		slog.ErrorContext(ctx, "event was not published: no publisher was composed",
			"event_type", string(event.EventType()))
		return
	}
	if err := d.publisher.Publish(ctx, event); err != nil {
		slog.ErrorContext(ctx, "publishing event failed",
			"event_type", string(event.EventType()), "error", err.Error())
	}
}

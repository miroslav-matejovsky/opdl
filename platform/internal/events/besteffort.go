package events

import (
	"context"
	"fmt"
	"io"
	"os"
)

// diagnostics is where a publication failure is reported when there is nowhere
// to return it. It is a variable so this package's own tests can read what an
// operator would see; no caller outside this package can replace it.
var diagnostics io.Writer = os.Stderr

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

// State publishes event and reports a failure to the process error stream as
// plain text.
//
// The failure goes no further. It is deliberately not restated as an event: the
// pipeline that could not carry the first one is the same pipeline a diagnostic
// about it would use, so republishing it would either fail again or, worse,
// succeed and hide that something was lost.
func (d Diagnostic) State(ctx context.Context, event Event) {
	if d.publisher == nil {
		report("platform: event %s was not published: no publisher was composed\n", event.EventType())
		return
	}
	if err := d.publisher.Publish(ctx, event); err != nil {
		report("platform: publishing event %s failed: %v\n", event.EventType(), err)
	}
}

// report is the terminal reporting path for a publication nothing could carry.
// There is nowhere left to return or state this, so the write is deliberately
// best effort: a process whose error stream is also broken still has to run.
func report(format string, args ...any) {
	_, _ = fmt.Fprintf(diagnostics, format, args...)
}

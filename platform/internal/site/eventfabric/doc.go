// Package eventfabric defines the read side of site event distribution.
//
// Delivery pairs an immutable envelope with the site's sequence. Consumer
// replays unacknowledged deliveries, follows live, and records durable progress.
// Delivery is at least once and consumer names are machine-scoped.
//
// Site publication uses events.Publisher. No concrete distribution or storage
// implementation exists yet. See internal/site/README.md and
// docs/plans/hierarchy.
package eventfabric

// Package eventfabric is the site level's half of event distribution: the
// contract for reading the site's stream, and the client that reaches the
// broker carrying it.
//
// # The read contract
//
// Delivery pairs an immutable envelope with the site's sequence. Consumer
// replays unacknowledged deliveries, follows live, and records durable progress.
// Delivery is at least once and consumer names are machine-scoped.
//
// Site publication uses events.Publisher. This package defines no second event
// or publisher model.
//
// # The client
//
// Client is the connection this instance holds to its embedded NATS broker. The
// broker itself is instance-level and lives in internal/instance/natsserver;
// the client is here because reaching the site's stream is a site concern, and
// it takes the broker as an InProcessConnProvider rather than by importing the
// package that runs one.
//
// Client does not implement Consumer. The deployment runs NATS without
// JetStream, so the broker carries messages between whoever is connected and
// stores nothing, and there is no durable order to replay or acknowledge. What
// exists today is the connection and Check, which proves the fabric carries a
// message end to end. See internal/site/README.md and docs/plans/hierarchy.
package eventfabric

package nats

import "github.com/miroslav-matejovsky/opdl/platform/internal/events"

// This file is the NATS adapter's event catalog: what this node's transport is
// doing, in the transport's own terms.
//
// Server and client:
//
//   - platform.nats.server_starting: the embedded server is starting.
//   - platform.nats.server_ready: it accepts connections.
//   - platform.nats.client_connected: this node reached a server.
//   - platform.nats.client_connect_retry: it could not, and is retrying.
//   - platform.nats.client_disconnected: the connection dropped, expectedly or
//     not.
//   - platform.nats.client_reconnected: it came back.
//   - platform.nats.client_closed: the connection was closed for good.
//   - platform.nats.client_async_error: the client reported an error nobody
//     asked for, so no caller received it.
//
// Journal:
//
//   - platform.nats.journal_ready: the site journal is usable by this node.
//   - platform.nats.journal_retry: it is not yet, and this node is waiting.
//   - platform.nats.journal_recovered: it became usable after retrying.
//
// Consumers:
//
//   - platform.nats.projector_started, projector_reset, projector_attach_retry,
//     projector_stopped: the node-wide ordered consumer's life.
//   - platform.nats.handler_started, handler_reset, handler_attach_retry,
//     handler_stopped: one durable per-service consumer's life.
//   - platform.nats.consumer_heartbeat_missed: a pull consumer missed an idle
//     heartbeat and the client is recovering.
//
// Adapter lifecycle:
//
//   - platform.nats.stopping and platform.nats.stopped: this adapter releasing
//     its client and server.
//
// These are adapter facts and stay adapter-shaped: server addresses, stream
// names, consumer attempts. None of it belongs in the common envelope, and none
// of it is published through the journal these events describe. They are
// recorded locally, because connection loss and journal unavailability are
// exactly the conditions under which the journal cannot carry an event about
// itself.

const (
	// TypeServerStarting is stated when the embedded server begins starting.
	TypeServerStarting events.Type = "platform.nats.server_starting"
	// TypeServerReady is stated when it accepts connections.
	TypeServerReady events.Type = "platform.nats.server_ready"

	// TypeClientConnected is stated when this node reaches a site server.
	TypeClientConnected events.Type = "platform.nats.client_connected"
	// TypeClientConnectRetry is stated while it cannot reach one.
	TypeClientConnectRetry events.Type = "platform.nats.client_connect_retry"
	// TypeClientDisconnected is stated when the connection drops.
	TypeClientDisconnected events.Type = "platform.nats.client_disconnected"
	// TypeClientReconnected is stated when the client is back on a server.
	TypeClientReconnected events.Type = "platform.nats.client_reconnected"
	// TypeClientClosed is stated when the connection was closed for good.
	TypeClientClosed events.Type = "platform.nats.client_closed"
	// TypeClientAsyncError is stated for an error no caller received.
	TypeClientAsyncError events.Type = "platform.nats.client_async_error"

	// TypeJournalReady is stated when this node can use the site journal.
	TypeJournalReady events.Type = "platform.nats.journal_ready"
	// TypeJournalRetry is stated while the journal is unavailable.
	TypeJournalRetry events.Type = "platform.nats.journal_retry"
	// TypeJournalRecovered is stated when it became available after retrying.
	TypeJournalRecovered events.Type = "platform.nats.journal_recovered"

	// TypeProjectorStarted is stated when the ordered consumer loop starts.
	TypeProjectorStarted events.Type = "platform.nats.projector_started"
	// TypeProjectorReset is stated when it is recreated after a reconnect.
	TypeProjectorReset events.Type = "platform.nats.projector_reset"
	// TypeProjectorAttachRetry is stated while it cannot attach.
	TypeProjectorAttachRetry events.Type = "platform.nats.projector_attach_retry"
	// TypeProjectorStopped is stated when the loop ends.
	TypeProjectorStopped events.Type = "platform.nats.projector_stopped"

	// TypeHandlerStarted is stated when a durable handler loop starts.
	TypeHandlerStarted events.Type = "platform.nats.handler_started"
	// TypeHandlerReset is stated when it is recreated after a reconnect.
	TypeHandlerReset events.Type = "platform.nats.handler_reset"
	// TypeHandlerAttachRetry is stated while its consumer cannot be created.
	TypeHandlerAttachRetry events.Type = "platform.nats.handler_attach_retry"
	// TypeHandlerStopped is stated when the loop ends.
	TypeHandlerStopped events.Type = "platform.nats.handler_stopped"

	// TypeConsumerHeartbeatMissed is stated when a pull consumer misses an idle
	// heartbeat and the client has already begun recovering.
	TypeConsumerHeartbeatMissed events.Type = "platform.nats.consumer_heartbeat_missed"

	// TypeStopping is stated when this adapter begins releasing.
	TypeStopping events.Type = "platform.nats.stopping"
	// TypeStopped is stated when it has released.
	TypeStopped events.Type = "platform.nats.stopped"
)

// stopSeverity ranks a loop or an adapter that has ended: one that gave up is an
// error, and one that was asked to stop is routine. Whether it failed is already
// in the payload, so severity is read from there rather than stated twice.
func stopSeverity(err string) events.Severity {
	if err != "" {
		return events.SeverityError
	}
	return events.SeverityInfo
}

// ServerStarting states that this node's embedded server is starting. Only a
// storage node runs one.
type ServerStarting struct {
	// ClientAddress is the host:port clients of this node connect to.
	ClientAddress string `json:"client_address"`
	// ClusterAddress is the host:port the site's other servers route to.
	ClusterAddress string `json:"cluster_address"`
	// Routes are the cluster addresses of the site's other storage nodes.
	Routes []string `json:"routes,omitempty"`
	// JetStreamStoreDir is where this node keeps the journal it stores.
	JetStreamStoreDir string `json:"jetstream_store_dir"`
}

// EventType returns the event's stable dotted kind.
func (ServerStarting) EventType() events.Type { return TypeServerStarting }

// ServerReady states that the embedded server accepts connections.
type ServerReady struct {
	// ClientAddress is the host:port it accepts them on.
	ClientAddress string `json:"client_address"`
}

// EventType returns the event's stable dotted kind.
func (ServerReady) EventType() events.Type { return TypeServerReady }

// ClientConnected states that this node reached one of the site's servers.
type ClientConnected struct {
	// Server is the connected server URL with any credentials redacted. It is
	// never the raw URL: a connection string can carry a token.
	Server string `json:"server"`
	// Attempts is how many tries it took, counted from one.
	Attempts int `json:"attempts"`
	// DurationMS is how long connecting took, across all attempts.
	DurationMS int64 `json:"duration_ms"`
}

// EventType returns the event's stable dotted kind.
func (ClientConnected) EventType() events.Type { return TypeClientConnected }

// ClientConnectRetry states that this node could not reach any of the site's
// servers and is still trying.
type ClientConnectRetry struct {
	// Servers are the addresses it is trying.
	Servers []string `json:"servers"`
	// Attempt is which try this is, counted from one.
	Attempt int `json:"attempt"`
	// Error is why the last try failed.
	Error string `json:"error"`
}

// EventType returns the event's stable dotted kind.
func (ClientConnectRetry) EventType() events.Type { return TypeClientConnectRetry }

// Severity reports a node that has not reached its site as a degradation: it is
// still starting, and it may yet succeed.
func (ClientConnectRetry) Severity() events.Severity { return events.SeverityWarn }

// ClientDisconnected states that the connection to a server dropped. It is one
// fact, not two: the connection is gone either way, and Shutdown says only
// whether this node asked for that.
type ClientDisconnected struct {
	// LastServer is the redacted URL of the server it was connected to.
	LastServer string `json:"last_server"`
	// Shutdown reports that this node is closing, so the drop was expected.
	Shutdown bool `json:"shutdown"`
	// Error is what the client reported, empty when it reported nothing.
	Error string `json:"error,omitempty"`
}

// EventType returns the event's stable dotted kind.
func (ClientDisconnected) EventType() events.Type { return TypeClientDisconnected }

// Severity reports an unasked-for disconnect as a degradation and an expected
// one as routine.
func (e ClientDisconnected) Severity() events.Severity {
	if e.Shutdown {
		return events.SeverityInfo
	}
	return events.SeverityWarn
}

// ClientReconnected states that the client is attached to a server again.
type ClientReconnected struct {
	// Server is the redacted URL it reconnected to, which may not be the one it
	// lost.
	Server string `json:"server"`
}

// EventType returns the event's stable dotted kind.
func (ClientReconnected) EventType() events.Type { return TypeClientReconnected }

// ClientClosed states that the connection was closed for good. It is routine
// even when the client reports a last error: by the time a connection closes,
// whatever went wrong has already been stated as its own fact.
type ClientClosed struct {
	// Error is the client's last error, empty when there was none.
	Error string `json:"error,omitempty"`
}

// EventType returns the event's stable dotted kind.
func (ClientClosed) EventType() events.Type { return TypeClientClosed }

// ClientAsyncError states an error the client reported outside any call, so no
// caller received it and nothing else would report it.
type ClientAsyncError struct {
	// Subject is the subscription's subject, empty when the error was not about
	// one.
	Subject string `json:"subject,omitempty"`
	// Error is what the client reported.
	Error string `json:"error"`
}

// EventType returns the event's stable dotted kind.
func (ClientAsyncError) EventType() events.Type { return TypeClientAsyncError }

// Severity reports it as an error: nobody else was told.
func (ClientAsyncError) Severity() events.Severity { return events.SeverityError }

// JournalReady states that this node can use the site journal.
type JournalReady struct {
	// Journal is the site journal's stream name.
	Journal string `json:"journal"`
	// HostsStorage reports whether this node stores the journal or routes to the
	// nodes that do.
	HostsStorage bool `json:"hosts_storage"`
	// Replicas is the journal's replica count.
	Replicas int `json:"replicas"`
}

// EventType returns the event's stable dotted kind.
func (JournalReady) EventType() events.Type { return TypeJournalReady }

// JournalRetry states that the site journal is not usable yet.
type JournalRetry struct {
	// Journal is the site journal's stream name.
	Journal string `json:"journal"`
	// Attempt is which try this is, counted from one.
	Attempt int `json:"attempt"`
	// Error is why the last try failed.
	Error string `json:"error"`
}

// EventType returns the event's stable dotted kind.
func (JournalRetry) EventType() events.Type { return TypeJournalRetry }

// Severity reports a node without a journal as a degradation: it is still
// starting, and the storage nodes may still be coming up.
func (JournalRetry) Severity() events.Severity { return events.SeverityWarn }

// JournalRecovered states that the site journal became usable after retrying.
type JournalRecovered struct {
	// Journal is the site journal's stream name.
	Journal string `json:"journal"`
	// Attempts is how many tries it took.
	Attempts int `json:"attempts"`
	// DurationMS is how long the node waited in total.
	DurationMS int64 `json:"duration_ms"`
}

// EventType returns the event's stable dotted kind.
func (JournalRecovered) EventType() events.Type { return TypeJournalRecovered }

// ProjectorStarted states that the node-wide ordered consumer loop began. It
// carries no payload: which node it is about is the envelope's origin.
type ProjectorStarted struct{}

// EventType returns the event's stable dotted kind.
func (ProjectorStarted) EventType() events.Type { return TypeProjectorStarted }

// ProjectorReset states that the ordered consumer is being recreated after a
// reconnect, resuming from where the projection got to.
type ProjectorReset struct {
	// NextSequence is the journal sequence the new consumer starts from.
	NextSequence uint64 `json:"next_sequence"`
}

// EventType returns the event's stable dotted kind.
func (ProjectorReset) EventType() events.Type { return TypeProjectorReset }

// Severity reports a reset as a degradation: the node is not receiving events
// while it reattaches.
func (ProjectorReset) Severity() events.Severity { return events.SeverityWarn }

// ProjectorAttachRetry states that the ordered consumer could not be attached.
type ProjectorAttachRetry struct {
	// NextSequence is the journal sequence it would start from.
	NextSequence uint64 `json:"next_sequence"`
	// Attempt is which try this is, counted from one.
	Attempt int `json:"attempt"`
	// Error is why the last try failed.
	Error string `json:"error"`
}

// EventType returns the event's stable dotted kind.
func (ProjectorAttachRetry) EventType() events.Type { return TypeProjectorAttachRetry }

// Severity reports a projection that is not advancing as a degradation.
func (ProjectorAttachRetry) Severity() events.Severity { return events.SeverityWarn }

// ProjectorStopped states that the ordered consumer loop ended.
type ProjectorStopped struct {
	// Error is why it gave up, empty when it was canceled cleanly.
	Error string `json:"error,omitempty"`
}

// EventType returns the event's stable dotted kind.
func (ProjectorStopped) EventType() events.Type { return TypeProjectorStopped }

// Severity reports a projector that gave up as an error and a canceled one as
// routine.
func (e ProjectorStopped) Severity() events.Severity { return stopSeverity(e.Error) }

// HandlerStarted states that one durable per-service consumer loop began.
type HandlerStarted struct {
	// Handler is the service name the durable consumer belongs to.
	Handler string `json:"handler"`
}

// EventType returns the event's stable dotted kind.
func (HandlerStarted) EventType() events.Type { return TypeHandlerStarted }

// HandlerReset states that a durable consumer is being recreated after a
// reconnect. Its position is durable, so it resumes where it was.
type HandlerReset struct {
	// Handler is the service name the durable consumer belongs to.
	Handler string `json:"handler"`
}

// EventType returns the event's stable dotted kind.
func (HandlerReset) EventType() events.Type { return TypeHandlerReset }

// Severity reports a reset as a degradation: the service is not reacting while
// it reattaches.
func (HandlerReset) Severity() events.Severity { return events.SeverityWarn }

// HandlerAttachRetry states that a durable consumer could not be created.
type HandlerAttachRetry struct {
	// Handler is the durable consumer name being created.
	Handler string `json:"handler"`
	// Attempt is which try this is, counted from one.
	Attempt int `json:"attempt"`
	// Error is why the last try failed.
	Error string `json:"error"`
}

// EventType returns the event's stable dotted kind.
func (HandlerAttachRetry) EventType() events.Type { return TypeHandlerAttachRetry }

// Severity reports a service that is not reacting as a degradation.
func (HandlerAttachRetry) Severity() events.Severity { return events.SeverityWarn }

// HandlerStopped states that a durable consumer loop ended.
type HandlerStopped struct {
	// Handler is the service name the durable consumer belongs to.
	Handler string `json:"handler"`
	// Error is why it gave up, empty when it was canceled cleanly.
	Error string `json:"error,omitempty"`
}

// EventType returns the event's stable dotted kind.
func (HandlerStopped) EventType() events.Type { return TypeHandlerStopped }

// Severity reports a handler that gave up as an error and a canceled one as
// routine.
func (e HandlerStopped) Severity() events.Severity { return stopSeverity(e.Error) }

// ConsumerHeartbeatMissed states that a pull consumer missed an idle heartbeat.
// The client has already issued another pull, so the loop continues; the event
// exists because a node that keeps missing them is a node with a sick link.
type ConsumerHeartbeatMissed struct {
	// Stream is the journal stream the consumer reads.
	Stream string `json:"stream"`
	// Error is what the client reported.
	Error string `json:"error"`
}

// EventType returns the event's stable dotted kind.
func (ConsumerHeartbeatMissed) EventType() events.Type { return TypeConsumerHeartbeatMissed }

// Severity reports a missed heartbeat as a degradation, not a failure.
func (ConsumerHeartbeatMissed) Severity() events.Severity { return events.SeverityWarn }

// Stopping states that this adapter began releasing its client and server. It
// carries no payload: which node is stopping is the envelope's origin.
type Stopping struct{}

// EventType returns the event's stable dotted kind.
func (Stopping) EventType() events.Type { return TypeStopping }

// Stopped states that this adapter finished releasing.
type Stopped struct {
	// Error is what failed while releasing, empty when it was clean.
	Error string `json:"error,omitempty"`
}

// EventType returns the event's stable dotted kind.
func (Stopped) EventType() events.Type { return TypeStopped }

// Severity reports a release that did not complete as an error.
func (e Stopped) Severity() events.Severity { return stopSeverity(e.Error) }

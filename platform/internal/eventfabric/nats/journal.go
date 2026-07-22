package nats

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/miroslav-matejovsky/opdl/platform/internal/eventfabric"
	"github.com/miroslav-matejovsky/opdl/platform/internal/operations"
)

// journalConfig is the site journal's required configuration. The journal is
// file-backed and limits-retained, keeps history without age or count deletion,
// and rejects new events when full so a replay is never quietly made impossible.
//
// Its replicas are spread across machines, but not by anything stated here. The
// constraint is the server's JetStreamUniqueTag, set in serverOptions, because
// the client's per-stream Placement carries only a cluster and a tag list and has
// no way to say "distinct values of this tag". Declaring it once per server is
// also what D2 of the plan asked for: it cannot be forgotten by a stream added
// later.
func (f *Fabric) journalConfig() jetstream.StreamConfig {
	return jetstream.StreamConfig{
		Name:        f.scope.StreamName(),
		Subjects:    []string{f.scope.SubjectFilter()},
		Storage:     jetstream.FileStorage,
		Retention:   jetstream.LimitsPolicy,
		Discard:     jetstream.DiscardNew,
		MaxAge:      0,
		MaxMsgs:     -1,
		MaxBytes:    f.cfg.MaxBytes,
		MaxMsgSize:  f.cfg.MaxMessageBytes,
		Replicas:    f.cfg.Replicas,
		Description: "OPDL site event journal",
	}
}

// journalRetryInterval is how often startup re-attempts the journal while the
// site's JetStream cluster is still coming up.
const journalRetryInterval = 200 * time.Millisecond

// ensureJournal creates the site journal or validates an existing one against
// what this build requires, waiting for the site's journal to become reachable.
//
// The waiting is the point on a site with three storage nodes. Their JetStream
// metadata group has no leader until a quorum of the three servers has found
// each other, and until it does the JetStream API does not answer at all: a
// request against it times out rather than reporting a missing journal. Every
// storage node starts at once, so on a cold site that is the normal first
// answer, not a failure. Treating it as one is what would make a correctly
// configured three-node site refuse to boot.
//
// The bound is the same StartupTimeout that bounds connecting, and it separates
// "the site's cluster is still forming" from "this site has no reachable
// journal". A journal whose stored configuration is incompatible is refused
// immediately rather than retried: waiting cannot make it compatible.
func (f *Fabric) ensureJournal(ctx context.Context) (jetstream.Stream, error) {
	want := f.journalConfig()
	deadline := time.Now().Add(f.cfg.StartupTimeout)

	var last error
	attempt := 0
	started := time.Now()
	for {
		attempt++
		stream, err := f.attemptJournal(ctx, want)
		if err == nil {
			// Creation being acknowledged by the metadata leader does not mean the
			// server this client is attached to can already read the stream. Verify
			// that local view before readiness uses the returned handle.
			_, infoErr := stream.Info(ctx)
			if infoErr == nil {
				if attempt > 1 {
					f.observer.Emit("event_fabric.journal_recovered", operations.LevelInfo, "event_fabric.nats", "site journal became available", map[string]any{
						attributeJournal: want.Name, "attempts": attempt, operations.AttributeDurationMS: time.Since(started).Milliseconds(),
					})
				}
				return stream, nil
			}
			err = fmt.Errorf("nats: verify journal %s locally: %w", want.Name, infoErr)
		}
		if !retryableJournalError(ctx, err) {
			return nil, err
		}
		last = err
		if attempt == 1 || attempt%10 == 0 {
			f.observer.Emit("event_fabric.journal_retry", operations.LevelWarn, "event_fabric.nats", "site journal is unavailable; retrying", map[string]any{
				attributeJournal: want.Name, attributeAttempt: attempt, operations.AttributeError: err.Error(),
			})
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("nats: wait for journal %s: %w", want.Name, ctxErr)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("nats: journal %s was not available within %s: %w",
				want.Name, f.cfg.StartupTimeout, last)
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("nats: wait for journal %s: %w", want.Name, ctx.Err())
		case <-time.After(journalRetryInterval):
		}
	}
}

// attemptJournal is one attempt at reaching the site journal: look it up, and
// create it when it is absent and this node stores the journal.
//
// A node that does not host storage never creates it. The journal belongs to the
// storage nodes, and a client that created it could create it with a replica
// count the site's storage cannot hold.
func (f *Fabric) attemptJournal(ctx context.Context, want jetstream.StreamConfig) (jetstream.Stream, error) {
	stream, err := f.js.Stream(ctx, want.Name)
	if err == nil {
		return f.checkJournal(stream, want)
	}
	if !errors.Is(err, jetstream.ErrStreamNotFound) {
		return nil, fmt.Errorf("nats: look up journal %s: %w", want.Name, err)
	}
	if !f.cfg.HostsStorage {
		return nil, fmt.Errorf("nats: journal %s does not exist yet: %w", want.Name, jetstream.ErrStreamNotFound)
	}

	stream, err = f.js.CreateStream(ctx, want)
	if errors.Is(err, jetstream.ErrStreamNameAlreadyInUse) {
		// Another storage node created it between the lookup and now; validate it.
		return f.validateJournal(ctx, want)
	}
	if err != nil {
		return nil, fmt.Errorf("nats: create journal %s: %w", want.Name, err)
	}
	return stream, nil
}

// errCodeClusterNoPeers is the JetStream API error for "no suitable peers for
// placement". The nats.go client does not name it.
//
// A three-replica journal needs three storage peers the metadata leader already
// knows about. Immediately after an election the leader may know about fewer,
// so a cold site can answer this to the first create and accept the same request
// a moment later.
const errCodeClusterNoPeers jetstream.ErrorCode = 10005

// errCodeStreamNotFound is returned briefly by a server whose local metadata
// view has not caught up with a stream the cluster leader just created.
const errCodeStreamNotFound jetstream.ErrorCode = 10059

// retryableJournalError reports whether err is a site that has not finished
// coming up rather than one this node cannot join.
//
// The retryable shapes all mean "not yet". A missing journal means no storage
// node has created it, which is what a client-only machine waits through. No
// responder means the JetStream API subject has nobody serving it. A deadline
// from the request itself, as opposed to from ctx, means the API accepted the
// request and no metadata leader answered, which is a cluster mid-election. And
// no suitable peers means the leader has not yet seen enough of the site's
// storage nodes to place the journal's replicas.
//
// Everything else, including an incompatible journal, is this node's answer and
// is returned immediately. Waiting cannot make an incompatible journal
// compatible, so retrying one would only turn a clear refusal into a timeout.
func retryableJournalError(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	if errors.Is(err, eventfabric.ErrIncompatibleJournal) {
		return false
	}
	var apiErr *jetstream.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode == errCodeClusterNoPeers || apiErr.ErrorCode == errCodeStreamNotFound
	}
	return errors.Is(err, jetstream.ErrStreamNotFound) ||
		errors.Is(err, jetstream.ErrNoStreamResponse) ||
		errors.Is(err, nats.ErrNoResponders) ||
		errors.Is(err, jetstream.ErrJetStreamNotEnabled) ||
		errors.Is(err, jetstream.ErrJetStreamNotEnabledForAccount) ||
		errors.Is(err, context.DeadlineExceeded)
}

// validateJournal loads an existing journal and checks it against want.
func (f *Fabric) validateJournal(ctx context.Context, want jetstream.StreamConfig) (jetstream.Stream, error) {
	stream, err := f.js.Stream(ctx, want.Name)
	if err != nil {
		return nil, fmt.Errorf("nats: look up journal %s: %w", want.Name, err)
	}
	return f.checkJournal(stream, want)
}

// checkJournal reports whether an existing journal is compatible with what this
// build requires. An incompatible journal is refused with
// eventfabric.ErrIncompatibleJournal and the first difference found, so an
// operator sees exactly what does not match.
func (f *Fabric) checkJournal(stream jetstream.Stream, want jetstream.StreamConfig) (jetstream.Stream, error) {
	have := stream.CachedInfo().Config
	if difference := journalDifference(want, have); difference != "" {
		return nil, fmt.Errorf("nats: journal %s: %w: %s", want.Name, eventfabric.ErrIncompatibleJournal, difference)
	}
	return stream, nil
}

// journalDifference returns the first way have differs from want that matters
// for correctness, or an empty string when the two are compatible. It compares
// the subjects, storage, retention, discard, limits, and replicas a replay
// depends on; it ignores fields NATS may set itself.
func journalDifference(want, have jetstream.StreamConfig) string {
	switch {
	case !equalStrings(want.Subjects, have.Subjects):
		return fmt.Sprintf("subjects are %v, need %v", have.Subjects, want.Subjects)
	case want.Storage != have.Storage:
		return fmt.Sprintf("storage is %s, need %s", have.Storage, want.Storage)
	case want.Retention != have.Retention:
		return fmt.Sprintf("retention is %s, need %s", have.Retention, want.Retention)
	case want.Discard != have.Discard:
		return fmt.Sprintf("discard is %s, need %s", have.Discard, want.Discard)
	case want.MaxBytes != have.MaxBytes:
		return fmt.Sprintf("max bytes is %d, need %d", have.MaxBytes, want.MaxBytes)
	case want.MaxMsgSize != have.MaxMsgSize:
		return fmt.Sprintf("max message bytes is %d, need %d", have.MaxMsgSize, want.MaxMsgSize)
	case want.Replicas != have.Replicas:
		return fmt.Sprintf("replicas is %d, need %d", have.Replicas, want.Replicas)
	default:
		return ""
	}
}

// equalStrings reports whether two string slices hold the same values in the
// same order.
func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

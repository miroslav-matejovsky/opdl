package nats

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/miroslav-matejovsky/opdl/platform/internal/eventfabric"
)

// journalConfig is the site journal's required configuration. The journal is
// file-backed and limits-retained, keeps history without age or count deletion,
// and rejects new events when full so a replay is never quietly made impossible.
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

// ensureJournal creates the site journal or validates an existing one against
// what this build requires. It looks the journal up first: an existing journal
// is validated, and only its absence leads to creation. A storage node creates a
// missing journal; a node that does not host storage waits for a storage node to
// create it. Either way, a journal whose stored configuration is incompatible is
// refused rather than adopted or silently mutated.
func (f *Fabric) ensureJournal(ctx context.Context) (jetstream.Stream, error) {
	want := f.journalConfig()

	stream, err := f.js.Stream(ctx, want.Name)
	if err == nil {
		return f.checkJournal(stream, want)
	}
	if !errors.Is(err, jetstream.ErrStreamNotFound) {
		return nil, fmt.Errorf("nats: look up journal %s: %w", want.Name, err)
	}

	if !f.cfg.HostsStorage {
		return f.awaitJournal(ctx, want)
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

// awaitJournal waits for a storage node to create the journal, then validates
// it. It is how a non-storage node joins a site whose journal it does not host.
func (f *Fabric) awaitJournal(ctx context.Context, want jetstream.StreamConfig) (jetstream.Stream, error) {
	deadline := time.Now().Add(f.cfg.StartupTimeout)
	for {
		stream, err := f.js.Stream(ctx, want.Name)
		if err == nil {
			return f.checkJournal(stream, want)
		}
		if !errors.Is(err, jetstream.ErrStreamNotFound) {
			return nil, fmt.Errorf("nats: look up journal %s: %w", want.Name, err)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("nats: journal %s was not created within %s", want.Name, f.cfg.StartupTimeout)
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("nats: wait for journal %s: %w", want.Name, ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
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

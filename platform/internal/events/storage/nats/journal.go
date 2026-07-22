package nats

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/miroslav-matejovsky/opdl/platform/internal/events/storage/eventfabric"
)

func (b *Backend) journalConfig() jetstream.StreamConfig {
	return jetstream.StreamConfig{
		Name:        b.scope.StreamName(),
		Subjects:    []string{b.scope.SubjectFilter()},
		Storage:     jetstream.FileStorage,
		Retention:   jetstream.LimitsPolicy,
		Discard:     jetstream.DiscardNew,
		MaxAge:      0,
		MaxMsgs:     -1,
		MaxBytes:    b.cfg.MaxBytes,
		MaxMsgSize:  b.cfg.MaxMessageBytes,
		Replicas:    b.cfg.Replicas,
		Description: "OPDL site event journal",
	}
}

const journalRetryInterval = 200 * time.Millisecond

func (b *Backend) ensureJournal(ctx context.Context) (jetstream.Stream, error) {
	want := b.journalConfig()
	deadline := time.Now().Add(b.cfg.StartupTimeout)

	var last error
	attempt := 0
	started := time.Now()
	for {
		attempt++
		stream, err := b.attemptJournal(ctx, want)
		if err == nil {
			_, infoErr := stream.Info(ctx)
			switch {
			case infoErr != nil:
				err = fmt.Errorf("nats: verify journal %s locally: %w", want.Name, infoErr)
			case attempt == 1:
				// It worked first time, so nothing recovered from anything.
				return stream, nil
			default:
				return stream, b.local.Publish(ctx, JournalRecovered{
					Journal:    want.Name,
					Attempts:   attempt,
					DurationMS: time.Since(started).Milliseconds(),
				})
			}
		}
		if !retryableJournalError(ctx, err) {
			return nil, err
		}
		last = err
		if attempt == 1 || attempt%10 == 0 {
			if stateErr := b.local.Publish(ctx, JournalRetry{Journal: want.Name, Attempt: attempt, Error: err.Error()}); stateErr != nil {
				return nil, errors.Join(err, stateErr)
			}
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("nats: wait for journal %s: %w", want.Name, ctxErr)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("nats: journal %s was not available within %s: %w",
				want.Name, b.cfg.StartupTimeout, last)
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("nats: wait for journal %s: %w", want.Name, ctx.Err())
		case <-time.After(journalRetryInterval):
		}
	}
}

func (b *Backend) attemptJournal(ctx context.Context, want jetstream.StreamConfig) (jetstream.Stream, error) {
	stream, err := b.js.Stream(ctx, want.Name)
	if err == nil {
		return b.checkJournal(stream, want)
	}
	if !errors.Is(err, jetstream.ErrStreamNotFound) {
		return nil, fmt.Errorf("nats: look up journal %s: %w", want.Name, err)
	}
	if !b.cfg.HostsStorage {
		return nil, fmt.Errorf("nats: journal %s does not exist yet: %w", want.Name, jetstream.ErrStreamNotFound)
	}

	stream, err = b.js.CreateStream(ctx, want)
	if errors.Is(err, jetstream.ErrStreamNameAlreadyInUse) {
		return b.validateJournal(ctx, want)
	}
	if err != nil {
		return nil, fmt.Errorf("nats: create journal %s: %w", want.Name, err)
	}
	return stream, nil
}

const errCodeClusterNoPeers jetstream.ErrorCode = 10005
const errCodeStreamNotFound jetstream.ErrorCode = 10059

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

func (b *Backend) validateJournal(ctx context.Context, want jetstream.StreamConfig) (jetstream.Stream, error) {
	stream, err := b.js.Stream(ctx, want.Name)
	if err != nil {
		return nil, fmt.Errorf("nats: look up journal %s: %w", want.Name, err)
	}
	return b.checkJournal(stream, want)
}

func (b *Backend) checkJournal(stream jetstream.Stream, want jetstream.StreamConfig) (jetstream.Stream, error) {
	have := stream.CachedInfo().Config
	if difference := journalDifference(want, have); difference != "" {
		return nil, fmt.Errorf("nats: journal %s: %w: %s", want.Name, eventfabric.ErrIncompatibleJournal, difference)
	}
	return stream, nil
}

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

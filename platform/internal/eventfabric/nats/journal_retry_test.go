package nats

import (
	"context"
	"errors"
	"testing"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/internal/eventfabric"
)

func TestRetryableJournalErrorIncludesLocalMetadataConvergence(t *testing.T) {
	err := &jetstream.APIError{
		Code:        404,
		ErrorCode:   errCodeStreamNotFound,
		Description: "stream not found",
	}
	require.True(t, retryableJournalError(t.Context(), err))

	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	require.False(t, retryableJournalError(canceled, err), "caller cancellation always stops retries")
}

func TestOrderedConsumerConfigResumesAfterLastAppliedSequence(t *testing.T) {
	cfg := orderedConsumerConfig("events.>", 42)
	require.Equal(t, jetstream.DeliverByStartSequencePolicy, cfg.DeliverPolicy)
	require.Equal(t, uint64(42), cfg.OptStartSeq)
	require.Equal(t, []string{"events.>"}, cfg.FilterSubjects)
}

func TestHandlerPendingErrorTreatsConsumerConvergenceAsNotAttached(t *testing.T) {
	missing := &jetstream.APIError{
		Code:        404,
		ErrorCode:   jetstream.JSErrCodeConsumerNotFound,
		Description: "consumer not found",
	}
	err := handlerPendingError("registration", "info", missing)
	require.ErrorIs(t, err, eventfabric.ErrHandlerNotAttached)
	require.ErrorIs(t, err, jetstream.ErrConsumerNotFound)
	require.ErrorContains(t, err, "registration consumer info")

	other := errors.New("permission denied")
	err = handlerPendingError("registration", "lookup", other)
	require.NotErrorIs(t, err, eventfabric.ErrHandlerNotAttached)
	require.ErrorIs(t, err, other)
}

func TestConsumerReadErrorTreatsMissingHeartbeatAsRecoverable(t *testing.T) {
	require.True(t, recoverableConsumerReadError(jetstream.ErrNoHeartbeat))
	require.True(t, recoverableConsumerReadError(errors.Join(errors.New("read interrupted"), jetstream.ErrNoHeartbeat)))
	require.False(t, recoverableConsumerReadError(jetstream.ErrConsumerDeleted))
}

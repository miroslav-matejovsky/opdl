package nats

import (
	"context"
	"testing"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"
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

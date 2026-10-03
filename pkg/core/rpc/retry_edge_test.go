package rpc

import (
	"context"
	"testing"
	"time"

	coreerrors "github.com/ryuux05/godex/pkg/core/errors"
	"github.com/stretchr/testify/assert"
)

func TestRetryCanceledBeforeFirstAttempt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	err := RetryWithBackoff(ctx, DefaultRetryConfig(), func() error { calls++; return nil })
	assert.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, calls)
}

func TestRetryCanceledDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	cfg := DefaultRetryConfig()
	cfg.InitialBackoff = time.Hour
	err := RetryWithBackoff(ctx, cfg, func() error { calls++; cancel(); return &coreerrors.HTTPError{StatusCode: 503} })
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, calls)
}

func TestRetryResponseTooBigDoesNotRetry(t *testing.T) {
	for _, failure := range []*coreerrors.RPCError{{Code: -32008}, {Code: -32602, Message: "response is too big"}} {
		calls := 0
		err := RetryWithBackoff(context.Background(), DefaultRetryConfig(), func() error { calls++; return failure })
		assert.ErrorIs(t, err, failure)
		assert.Equal(t, 1, calls)
	}
}

func TestRetryJitterWithTinyBackoff(t *testing.T) {
	for _, backoff := range []time.Duration{0, time.Nanosecond, 3 * time.Nanosecond} {
		cfg := DefaultRetryConfig()
		cfg.InitialBackoff = backoff
		cfg.MaxAttempts = 2
		calls := 0
		assert.NotPanics(t, func() {
			err := RetryWithBackoff(context.Background(), cfg, func() error {
				calls++
				if calls == 1 {
					return &coreerrors.HTTPError{StatusCode: 503}
				}
				return nil
			})
			assert.NoError(t, err)
			assert.Equal(t, 2, calls)
		})
	}
}

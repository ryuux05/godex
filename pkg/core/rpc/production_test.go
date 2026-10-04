package rpc

import (
	"context"
	"math"
	"testing"
	"time"

	coreerrors "github.com/ryuux05/godex/pkg/core/errors"
	"github.com/stretchr/testify/assert"
)

func TestRejectNullMissingAndMismatchedResponses(t *testing.T) {
	for _, body := range []string{`null`, `{}`, `{"id":1,"result":null}`, `{"id":1}`, `{"result":"0x1"}`, `{"id":9,"result":"0x1"}`} {
		t.Run(body, func(t *testing.T) { _, err := rpcWithResponse(body).Head(context.Background()); assert.Error(t, err) })
	}
	for _, body := range []string{`[{"id":0,"result":null}]`, `[{"result":{"hash":"one"}}]`, `[{"id":0}]`, `null`} {
		t.Run("batch "+body, func(t *testing.T) {
			blocks, err := rpcWithResponse(body).GetBlocks(context.Background(), []string{"0x1"})
			assert.Error(t, err)
			assert.Nil(t, blocks)
		})
	}
}

func TestRateLimitDefaultsZeroBurst(t *testing.T) {
	r := rpcWithResponse(`{"id":1,"result":"0x1"}`)
	limiter := NewHTTPRPC("http://rpc.test", 10, 0)
	r.limiter = limiter.limiter
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := r.Head(ctx)
	assert.NoError(t, err)
}

func TestRetryRejectsInvalidArithmeticBeforeCallback(t *testing.T) {
	for _, change := range []func(*RetryConfig){
		func(c *RetryConfig) { c.MaxAttempts = 0 }, func(c *RetryConfig) { c.InitialBackoff = -1 },
		func(c *RetryConfig) { c.Multiplier = math.NaN() }, func(c *RetryConfig) { c.Multiplier = math.Inf(1) },
		func(c *RetryConfig) { c.Multiplier = 0.5 }, func(c *RetryConfig) { c.MaxBackoff = -1 },
	} {
		cfg := DefaultRetryConfig()
		change(&cfg)
		called := false
		assert.Error(t, RetryWithBackoff(context.Background(), cfg, func() error { called = true; return nil }))
		assert.False(t, called)
	}
	assert.Error(t, RetryWithBackoff(context.Background(), DefaultRetryConfig(), nil))
}

func TestRetryClampsOverflowingBackoff(t *testing.T) {
	cfg := DefaultRetryConfig()
	cfg.MaxAttempts = 3
	cfg.InitialBackoff = time.Duration(math.MaxInt64)
	cfg.MaxBackoff = time.Nanosecond
	cfg.Multiplier = math.MaxFloat64
	calls := 0
	err := RetryWithBackoff(context.Background(), cfg, func() error { calls++; return &coreerrors.HTTPError{StatusCode: 503} })
	assert.Error(t, err)
	assert.Equal(t, 3, calls)
}

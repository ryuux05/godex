package core

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRetryWithBackoff_Success(t *testing.T) {
	config := RetryConfig{
		MaxAttempts:    3,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     100 * time.Millisecond,
		Multiplier:     2.0,
		EnableJitter:   false,
	}

	calls := 0
	fn := func() error {
		calls++
		return nil // Success on first try
	}

	err := RetryWithBackoff(context.Background(), config, fn)
	assert.NoError(t, err)
	assert.Equal(t, 1, calls)
}

func TestRetryWithBackoff_SuccessAfterRetries(t *testing.T) {
	config := RetryConfig{
		MaxAttempts:    3,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     100 * time.Millisecond,
		Multiplier:     2.0,
		EnableJitter:   false,
	}


	calls := 0
	fn := func() error {
		calls ++;
		if calls < 3 {
			return &HTTPError{
				StatusCode: 504,
				Message: "timeout",
			}
		}
		return nil
	}

	err := RetryWithBackoff(context.Background(), config, fn)
	assert.NoError(t, err)
	assert.Equal(t, 3, calls)
}

func TestRetryWithBackoff_MaxAttemptExceeded(t *testing.T) {
	config := RetryConfig{
		MaxAttempts:    3,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     100 * time.Millisecond,
		Multiplier:     2.0,
		EnableJitter:   false,
	}

	fn := func() error {
		return &HTTPError{
			StatusCode: 504,
			Message: "timeout",
		}
	}

	err := RetryWithBackoff(context.Background(), config, fn)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "max retry attempts")
}

func TestRetryWithBackoff_NonRetryableError(t *testing.T) {
	config := RetryConfig{
		MaxAttempts:    3,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     100 * time.Millisecond,
		Multiplier:     2.0,
		EnableJitter:   false,
	}

	calls := 0
	fn := func() error {
		calls++
		return &HTTPError{
			StatusCode: 400,
			Message: "bad request",
		}
	}

	err := RetryWithBackoff(context.Background(), config, fn)
	assert.Error(t, err)
	assert.Equal(t, calls, 1)
	assert.Contains(t, err.Error(), "non-retryable error")
}

func TestRetryWithBackoff_ExponentialBackoff(t *testing.T) {
	config := RetryConfig{
		MaxAttempts:    4,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     1 * time.Second,
		Multiplier:     2.0,
		EnableJitter:   false,
	}

	start := time.Now()
	calls := 0
	fn := func() error {
		calls++
		return &HTTPError{
			StatusCode: 504,
			Message: "gateway timeout",
		}
	}

	_ = RetryWithBackoff(context.Background(), config, fn)
	elapsed := time.Since(start)

	// Should wait: 10ms + 20ms + 40ms = 70ms minimum
	assert.GreaterOrEqual(t, elapsed, 70*time.Millisecond)
	assert.Equal(t, 4, calls)
} 

func TestRetryWithBackoff_MaxBackoff(t *testing.T) {
	config := RetryConfig{
		MaxAttempts:    4,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     50 * time.Millisecond,
		Multiplier:     10.0,
		EnableJitter:   false,
	}

	start := time.Now()
	calls := 0
	fn := func() error {
		calls++
		return &HTTPError{
			StatusCode: 504,
			Message: "gateway timeout",
		}
	}

	_ = RetryWithBackoff(context.Background(), config, fn)
	elapsed := time.Since(start)

	// Should wait: 10ms + 50ms + 50ms = 110ms minimum
	assert.GreaterOrEqual(t, elapsed, 110*time.Millisecond)
	assert.Equal(t, 4, calls)
}
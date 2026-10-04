package rpc

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"time"

	"github.com/ryuux05/godex/pkg/core/errors"
)

type RetryConfig struct {
	// MaxAttempt is the maximum number of retry attemps (including the initial attempt)
	// Default: 3
	MaxAttempts int
	// InitialBackoff is the initial backoff time duration before the first retry
	// Default: 1s
	InitialBackoff time.Duration
	// MaxBackoff is the maximum backoff time between retry
	// Default: 30s
	MaxBackoff time.Duration
	// Multiplier is the factor by which backoff increases after each retry
	// Default: 2.0 (exponential backoff)
	Multiplier float64
	// EnableJitter adds randomess to backoff to prevent thundering herd
	// To spread retry out.
	// Default: true
	EnableJitter bool
	// PerRequestTimeout is timeout duration per each duration
	// Default: 10 seconds
	PerRequestTimeout time.Duration
}

// Validate checks retry arithmetic. PerRequestTimeout is consumed by callers;
// zero is allowed for standalone retry callbacks that do not make RPC requests.
func (c RetryConfig) Validate() error {
	if c.MaxAttempts <= 0 {
		return fmt.Errorf("MaxAttempts must be positive")
	}
	if c.InitialBackoff < 0 || c.MaxBackoff < 0 || c.PerRequestTimeout < 0 {
		return fmt.Errorf("retry durations cannot be negative")
	}
	if c.MaxAttempts > 1 && (c.Multiplier < 1 || math.IsNaN(c.Multiplier) || math.IsInf(c.Multiplier, 0)) {
		return fmt.Errorf("Multiplier must be finite and at least 1")
	}
	return nil
}

func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts:       3,
		InitialBackoff:    1 * time.Second,
		MaxBackoff:        30 * time.Second,
		Multiplier:        2.0,
		EnableJitter:      true,
		PerRequestTimeout: 10 * time.Second,
	}
}

// RetryWithBackoff executes function with exponential backoff
// Only for retriable error.
//
// Example:
//
//	var result []Log
//	err := RetryWithBackoff(ctx, config, func() error {
//	    var err error
//	    result, err = rpc.GetLogs(ctx, filter)
//	    return err
//	})
func RetryWithBackoff(ctx context.Context, config RetryConfig, fn func() error) error {
	if err := config.Validate(); err != nil {
		return err
	}
	if fn == nil {
		return fmt.Errorf("retry callback is required")
	}
	var lastErr error
	backoff := config.InitialBackoff
	if backoff > config.MaxBackoff {
		backoff = config.MaxBackoff
	}

	for attempt := 0; attempt < config.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("retry cancelled: %w", err)
		}
		// Execute function
		lastErr = fn()

		// If there is no error return nil
		if lastErr == nil {
			return nil
		}

		// Check if the error is retriable
		if !errors.IsRetryableError(lastErr) {
			return fmt.Errorf("non-retryable error: %w", lastErr)
		}

		if errors.IsResponseTooBigError(lastErr) {
			return lastErr
		}
		// Last attempt failed - don't wait, just return
		if attempt == config.MaxAttempts-1 {
			break
		}

		// Calculate wait time with exponential backoff and jitter
		wait := backoff
		if config.EnableJitter && backoff/4 > 0 {
			jitter := time.Duration(rand.Int63n(int64(backoff / 4)))
			if jitter > config.MaxBackoff-backoff {
				wait = config.MaxBackoff
			} else {
				wait = backoff + jitter
			}
		}

		slog.Warn("retry attempt failed",
			slog.Int("attempt", attempt+1),
			slog.Int("max_attempts", config.MaxAttempts),
			slog.Any("error", lastErr),
			slog.Duration("retry_in", wait),
		)

		// Wait for context cancellation and backoff
		select {
		case <-time.After(wait):
			next := float64(backoff) * config.Multiplier
			if next >= float64(config.MaxBackoff) {
				backoff = config.MaxBackoff
			} else {
				backoff = time.Duration(next)
			}
		case <-ctx.Done():
			return fmt.Errorf("retry cancelled: %w", ctx.Err())
		}

	}

	return fmt.Errorf("max retry attempts (%d) exceeded: %w", config.MaxAttempts, lastErr)
}

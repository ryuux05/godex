package core

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"time"
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
}

func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts: 3,
		InitialBackoff: 1 * time.Second,
		MaxBackoff: 30 * time.Second,
		Multiplier: 2.0,
		EnableJitter: true,
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
	var lastErr error
	backoff := config.InitialBackoff

	for attempt := 0; attempt < config.MaxAttempts; attempt ++ {
		// Execute function
		lastErr = fn()

		// If there is no error return nil
		if lastErr == nil {
			return nil
		}

		// Check if the error is retriable
		if !isRetryableError(lastErr) {
			return fmt.Errorf("non-retriabe error: %w", lastErr)
		}

		// Last attempt failed - don't wait, just return
		if attempt == config.MaxAttempts-1 {
			break
		}

		// Calculate wait time with exponential backoff and jitter
		wait := backoff
		if config.EnableJitter {
			jitter := time.Duration(rand.Int63n(int64(backoff / 4)))
			wait = backoff + jitter
		}

		log.Printf("Retry attempt %d/%d failed: %v. Retrying in %v...",
			attempt+1, config.MaxAttempts, lastErr, wait)

		// Wait for context cancellation and backoff
		select {
		case <- time.After(wait):
			backoff = time.Duration(float64(backoff) * config.Multiplier)
			if backoff > config.MaxBackoff {
				backoff = config.MaxBackoff
			}
		case <- ctx.Done():
			return fmt.Errorf("retry cancelled: %w", ctx.Err())
		}

	}

	return fmt.Errorf("max retry attempts (%d) exceeded: %w", config.MaxAttempts, lastErr)
}


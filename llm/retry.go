package llm

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"
)

// RetryConfig controls retry behaviour for LLM API calls.
type RetryConfig struct {
	// MaxRetries is the maximum number of retry attempts.
	// 0 means no retries — fail immediately.
	MaxRetries int

	// BaseDelay is the initial delay before the first retry.
	// Subsequent retries use exponential backoff:
	// attempt 1: BaseDelay, attempt 2: BaseDelay*2, attempt 3: BaseDelay*4
	BaseDelay time.Duration

	// MaxDelay caps the backoff delay regardless of the exponent.
	MaxDelay time.Duration
}

// DefaultRetryConfig returns sensible defaults for production use.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries: 3,
		BaseDelay:  time.Second,
		MaxDelay:   30 * time.Second,
	}
}

type retryableError struct {
	statusCode int
	message    string
	retryAfter time.Duration
}

func (e *retryableError) Error() string {
	return fmt.Sprintf("http %d: %s", e.statusCode, e.message)
}

// isRetryable returns true for errors that should be retried.
// We retry:
//   - 429 Too Many Requests — rate limited, back off and retry
//   - 500 Internal Server Error — transient server error
//   - 502 Bad Gateway — transient infrastructure error
//   - 503 Service Unavailable — transient server error
//   - 504 Gateway Timeout — transient infrastructure error
func isRetryable(err error) bool {
	var re *retryableError
	if errors.As(err, &re) {
		switch re.statusCode {
		case http.StatusTooManyRequests,
			http.StatusInternalServerError,
			http.StatusBadGateway,
			http.StatusServiceUnavailable,
			http.StatusGatewayTimeout:
			return true
		}
	}
	return false
}

// withRetry wraps an LLM API call with exponential backoff retry logic.
//
// On a retryable error it:
//  1. Checks the Retry-After header — if present, waits that long
//  2. Otherwise uses exponential backoff: BaseDelay * 2^attempt
//  3. Caps the delay at MaxDelay
//  4. Gives up after MaxRetries attempts
//
// Non-retryable errors (400, 401, 403, 404) are returned immediately
// without retrying — these are configuration errors not transient failures.
func withRetry(
	ctx context.Context,
	cfg RetryConfig,
	fn func() error,
) error {
	var lastErr error

	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		// First attempt has no delay
		if attempt > 0 {
			delay := calculateDelay(cfg, attempt, lastErr)

			// Respect context cancellation during the wait
			select {
			case <-ctx.Done():
				return fmt.Errorf("context cancelled during retry: %w", ctx.Err())
			case <-time.After(delay):
			}
		}

		err := fn()
		if err == nil {
			return nil // success
		}

		lastErr = err

		if !isRetryable(err) {
			// Not worth retrying — configuration error or permanent failure
			return err
		}

		// Log retry attempt
		if attempt < cfg.MaxRetries {
			// We will retry — continue the loop
			continue
		}
	}

	return fmt.Errorf("failed after %d retries: %w", cfg.MaxRetries, lastErr)
}

// calculateDelay returns how long to wait before the next retry.
func calculateDelay(cfg RetryConfig, attempt int, err error) time.Duration {
	var re *retryableError
	if errors.As(err, &re) && re.retryAfter > 0 {
		jitter := time.Duration(float64(re.retryAfter) * 0.1)
		return re.retryAfter + jitter
	}

	// Exponential backoff: BaseDelay * 2^(attempt-1)
	backoff := float64(cfg.BaseDelay) * math.Pow(2, float64(attempt-1))

	// Cap at MaxDelay
	if backoff > float64(cfg.MaxDelay) {
		backoff = float64(cfg.MaxDelay)
	}

	return time.Duration(backoff)
}

// parseRetryAfter parses the Retry-After HTTP header.
func parseRetryAfter(header string) time.Duration {
	if header == "" {
		return 0
	}
	seconds, err := strconv.ParseFloat(header, 64)
	if err != nil {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
}

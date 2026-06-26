package llm

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestWithRetry_RetriesOn429(t *testing.T) {
	attempts := 0
	err := withRetry(context.Background(), RetryConfig{
		MaxRetries: 3,
		BaseDelay:  time.Millisecond, // fast for tests
		MaxDelay:   time.Millisecond * 10,
	}, func() error {
		attempts++
		if attempts < 3 {
			return &retryableError{statusCode: 429, message: "rate limited"}
		}
		return nil // succeed on third attempt
	})

	assert.NoError(t, err)
	assert.Equal(t, 3, attempts)
}

func TestWithRetry_DoesNotRetryOn400(t *testing.T) {
	attempts := 0
	err := withRetry(context.Background(), RetryConfig{
		MaxRetries: 3,
		BaseDelay:  time.Millisecond,
		MaxDelay:   time.Millisecond,
	}, func() error {
		attempts++
		return &retryableError{statusCode: 400, message: "bad request"}
	})

	assert.Error(t, err)
	assert.Equal(t, 1, attempts) // no retries on 400
}

func TestWithRetry_RespectsRetryAfterHeader(t *testing.T) {
	start := time.Now()
	withRetry(context.Background(), RetryConfig{
		MaxRetries: 1,
		BaseDelay:  time.Second, // would be 1s without Retry-After
		MaxDelay:   time.Second,
	}, func() error {
		return &retryableError{
			statusCode: 429,
			retryAfter: 50 * time.Millisecond, // server says wait 50ms
		}
	})
	elapsed := time.Since(start)

	// Should have waited ~50ms not 1s
	assert.Less(t, elapsed, 200*time.Millisecond)
}

func TestWithRetry_CancelsOnContextDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	attempts := 0
	err := withRetry(ctx, RetryConfig{
		MaxRetries: 3,
		BaseDelay:  time.Second,
		MaxDelay:   time.Second,
	}, func() error {
		attempts++
		return &retryableError{statusCode: 429}
	})

	assert.Error(t, err)
	// May have run once before context was checked
	assert.LessOrEqual(t, attempts, 1)
}

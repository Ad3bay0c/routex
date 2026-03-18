package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisStore is the Redis-backed implementation of MemoryStore.
//
// Unlike InMemStore, data here survives process restarts.
// Multiple agent processes (or separate deployments) can share
// the same Redis instance and read each other's memory.
//
// Switch to this backend in agents.yaml:
//
//	memory:
//	  backend:   redis
//	  redis_url: redis://localhost:6379
//	  ttl:       1h
//
// Everything agents call is identical to InMemStore — they never
// know which backend they are talking to. That is the point of
// the MemoryStore interface.
type RedisStore struct {
	// client is the Redis connection.
	// go-redis handles connection pooling automatically —
	// we create one client and reuse it across all calls.
	client *redis.Client

	// defaultTTL is used when Set() is called with a zero ttl.
	// Prevents keys from accumulating forever in Redis.
	defaultTTL time.Duration
}

// NewRedisStore creates a RedisStore connected to the given URL.
// Called by config.go's buildMemoryStore() when backend is "redis".
//
// redisURL format: "redis://localhost:6379" or "redis://:password@host:port/db"
// defaultTTL is applied to keys stored without an explicit TTL.
func NewRedisStore(redisURL string, defaultTTL time.Duration) (*RedisStore, error) {
	// ParseURL converts the URL string into go-redis options.
	// It handles host, port, password, and database number.
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("redis: invalid url %q: %w", redisURL, err)
	}

	client := redis.NewClient(opts)

	// Ping the server immediately to catch connection problems
	// at startup rather than silently failing on the first agent call.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis: cannot connect to %q: %w", redisURL, err)
	}

	// Apply a sensible default TTL if none was configured
	if defaultTTL == 0 {
		defaultTTL = 24 * time.Hour
	}

	return &RedisStore{
		client:     client,
		defaultTTL: defaultTTL,
	}, nil
}

// Set stores a value in Redis under the given key.
// If ttl is zero, defaultTTL is used — keys never live forever in Redis.
//
// This satisfies the MemoryStore interface.
func (s *RedisStore) Set(ctx context.Context, key string, value string, ttl time.Duration) error {
	if ttl == 0 {
		ttl = s.defaultTTL
	}

	// redis.Set(ctx, key, value, ttl)
	// ttl=0 in go-redis means no expiry — but we always want one,
	// so we use defaultTTL above to ensure it is never zero here.
	if err := s.client.Set(ctx, key, value, ttl).Err(); err != nil {
		return fmt.Errorf("redis: set %q: %w", key, err)
	}
	return nil
}

// Get retrieves a value from Redis by key.
// Returns ErrNotFound if the key does not exist or has expired.
// Redis handles TTL expiry automatically — we never need to check timestamps.
//
// This satisfies the MemoryStore interface.
func (s *RedisStore) Get(ctx context.Context, key string) (string, error) {
	val, err := s.client.Get(ctx, key).Result()
	if err != nil {
		// redis.Nil is go-redis's sentinel for "key not found"
		// We translate it to our own ErrNotFound so callers
		// never need to import the redis package just to check errors.
		if errors.Is(err, redis.Nil) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("redis: get %q: %w", key, err)
	}
	return val, nil
}

// Delete removes a key from Redis immediately.
// Safe to call on keys that do not exist.
//
// This satisfies the MemoryStore interface.
func (s *RedisStore) Delete(ctx context.Context, key string) error {
	if err := s.client.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("redis: delete %q: %w", key, err)
	}
	return nil
}

// Append adds a message to a named history list in Redis.
// We use a Redis List to store history — RPUSH appends to the right end
// so messages stay in chronological order (oldest at index 0).
//
// Messages are JSON-serialised before storage so the full Message
// struct — including Timestamp and ToolCallRecord — is preserved.
//
// This satisfies the MemoryStore interface.
func (s *RedisStore) Append(ctx context.Context, key string, message Message) error {
	// Fill in timestamp if not set
	if message.Timestamp.IsZero() {
		message.Timestamp = time.Now()
	}

	// Serialise the Message to JSON bytes
	// json.Marshal converts our Go struct into a JSON string
	data, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("redis: append: cannot serialise message: %w", err)
	}

	// RPush appends to the right (end) of the list
	if err := s.client.RPush(ctx, key, data).Err(); err != nil {
		return fmt.Errorf("redis: append to %q: %w", key, err)
	}

	// Refresh the TTL on the history list every time we append.
	// Without this, a long-running conversation would expire mid-run.
	if err := s.client.Expire(ctx, key, s.defaultTTL).Err(); err != nil {
		return fmt.Errorf("redis: expire %q: %w", key, err)
	}

	return nil
}

// History retrieves conversation messages from a Redis List.
// If limit is zero, all messages are returned.
// If limit is set, only the most recent `limit` messages are returned.
//
// LRANGE retrieves a slice of the list by index range.
// Index 0 is the oldest message (first appended).
// Index -1 is the newest (last appended).
//
// This satisfies the MemoryStore interface.
func (s *RedisStore) History(ctx context.Context, key string, limit int) ([]Message, error) {
	var raw []string
	var err error

	if limit <= 0 {
		// Retrieve all messages: index 0 to -1 means "everything"
		raw, err = s.client.LRange(ctx, key, 0, -1).Result()
	} else {
		// Retrieve only the last `limit` messages
		// Negative index counts from the end: -limit to -1
		raw, err = s.client.LRange(ctx, key, int64(-limit), -1).Result()
	}

	if err != nil {
		// An empty list returns redis.Nil — treat as empty, not error
		if errors.Is(err, redis.Nil) {
			return []Message{}, nil
		}
		return nil, fmt.Errorf("redis: history %q: %w", key, err)
	}

	// Deserialize each JSON string back into a Message struct
	messages := make([]Message, 0, len(raw))
	for _, item := range raw {
		var msg Message
		if err := json.Unmarshal([]byte(item), &msg); err != nil {
			// Skip corrupted entries rather than failing the whole history.
			continue
		}
		messages = append(messages, msg)
	}

	return messages, nil
}

// ClearHistory removes all messages from a history list.
// Uses DEL which removes the entire Redis key, not just its contents.
//
// This satisfies the MemoryStore interface.
func (s *RedisStore) ClearHistory(ctx context.Context, key string) error {
	if err := s.client.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("redis: clear history %q: %w", key, err)
	}
	return nil
}

// Close gracefully shuts down the Redis connection pool.
// Called by the runtime during shutdown — always call this, even in tests.
//
// This satisfies the MemoryStore interface.
func (s *RedisStore) Close() error {
	if err := s.client.Close(); err != nil {
		return fmt.Errorf("redis: close: %w", err)
	}
	return nil
}

// compile-time interface check — see inmem.go for explanation.
var _ Store = (*RedisStore)(nil)

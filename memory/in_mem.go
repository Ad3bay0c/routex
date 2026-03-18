package memory

import (
	"context"
	"sync"
	"time"
)

// InMemStore is the in-memory implementation of MemoryStore.
//
// Everything is stored in plain Go maps, protected by a RWMutex.
// Data lives only as long as the process runs — restart the program
// and it starts fresh. That is a feature during development and testing.
//
// For production runs where agents need to remember things across
// restarts, switch to RedisStore by changing one line in agents.yaml:
//
//	memory:
//	  backend: redis
//
// InMemStore satisfies the MemoryStore interface. The Go compiler
// checks this automatically — if we are missing any method the
// interface requires, the code will not compile.
type InMemStore struct {
	// mu protects both maps from concurrent reads and writes.
	// Multiple agents run as goroutines simultaneously — without
	// this lock, two agents writing at the same moment would cause
	// a data race and corrupt the maps.
	//
	// RWMutex has two modes:
	//   Lock() / Unlock()     — exclusive write lock, one writer at a time
	//   RLock() / RUnlock()   — shared read lock, many readers at once
	// This means reads never block each other — only writes block.
	mu sync.RWMutex

	// values holds key-value pairs set via Set().
	// The value is wrapped in an entry struct so we can track expiry.
	values map[string]entry

	// history holds conversation message lists set via Append().
	// Kept separate from values because the access patterns differ —
	// values are point lookups, history is always read as a full list.
	history map[string][]Message
}

// entry wraps a stored value with an optional expiry time.
type entry struct {
	value     string
	expiresAt time.Time // zero value means no expiry
}

// isExpired reports whether this entry has passed its TTL.
func (e entry) isExpired() bool {
	if e.expiresAt.IsZero() {
		return false // no TTL set — never expires
	}
	return time.Now().After(e.expiresAt)
}

// NewInMemStore creates a ready-to-use in-memory store.
// Called by config.go's buildMemoryStore() when backend is "inmem".
func NewInMemStore() *InMemStore {
	return &InMemStore{
		values:  make(map[string]entry),
		history: make(map[string][]Message),
	}
}

// Set stores a value under the given key.
// If ttl is zero, the value never expires.
// If a value already exists under this key, it is replaced.
//
// This satisfies the MemoryStore interface.
func (s *InMemStore) Set(_ context.Context, key string, value string, ttl time.Duration) error {
	s.mu.Lock()         // exclusive lock — we are writing
	defer s.mu.Unlock() // released when this function returns, no matter what

	e := entry{value: value}
	if ttl > 0 {
		e.expiresAt = time.Now().Add(ttl)
	}

	s.values[key] = e
	return nil
}

// Get retrieves a value by key.
// Returns ErrNotFound if the key does not exist or has expired.
//
// This satisfies the MemoryStore interface.
func (s *InMemStore) Get(_ context.Context, key string) (string, error) {
	s.mu.RLock() // shared read lock — other reads can happen simultaneously
	defer s.mu.RUnlock()

	e, ok := s.values[key]
	if !ok {
		return "", ErrNotFound
	}

	// Treat expired entries the same as missing ones
	if e.isExpired() {
		return "", ErrNotFound
	}

	return e.value, nil
}

// Delete removes a key from the store immediately.
// Does not return an error if the key does not exist — deleting
// something that is already gone is not a problem.
//
// This satisfies the MemoryStore interface.
func (s *InMemStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.values, key) // Go's built-in delete — safe even if key is missing
	return nil
}

// Append adds a message to the end of a named history list.
// Creates the list if it does not already exist.
//
// This satisfies the MemoryStore interface.
func (s *InMemStore) Append(_ context.Context, key string, message Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// If Timestamp is not set, fill it in now
	// so history always has accurate timing
	if message.Timestamp.IsZero() {
		message.Timestamp = time.Now()
	}

	s.history[key] = append(s.history[key], message)
	return nil
}

// History retrieves the full conversation history for a key.
// If limit is greater than zero, only the most recent `limit`
// messages are returned — useful for keeping context windows small.
// Returns an empty slice (not ErrNotFound) if the key does not exist.
//
// This satisfies the MemoryStore interface.
func (s *InMemStore) History(_ context.Context, key string, limit int) ([]Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	messages, ok := s.history[key]
	if !ok || len(messages) == 0 {
		return []Message{}, nil // empty history is not an error
	}

	// No limit requested — return everything
	if limit <= 0 || limit >= len(messages) {
		// Return a copy so the caller cannot mutate our internal slice
		result := make([]Message, len(messages))
		copy(result, messages)
		return result, nil
	}

	// Return only the most recent `limit` messages
	// Example: 10 messages, limit 3 → return messages[7:10]
	start := len(messages) - limit
	result := make([]Message, limit)
	copy(result, messages[start:])
	return result, nil
}

// ClearHistory removes all messages from a history list.
// Called by the supervisor when restarting an agent after a crash —
// the agent gets a clean slate rather than a confusing partial history.
//
// This satisfies the MemoryStore interface.
func (s *InMemStore) ClearHistory(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.history, key)
	return nil
}

// Close is a no-op for the in-memory store.
// There are no connections to close, no files to flush.
// It exists only to satisfy the MemoryStore interface so the runtime
// can call Close() on any backend without knowing which one it has.
//
// This satisfies the MemoryStore interface.
func (s *InMemStore) Close() error {
	return nil
}

// compile-time interface check.
// This line does nothing at runtime — it is zero cost.
// But if InMemStore is ever missing a method that MemoryStore requires,
// this line makes the compiler say exactly which method is missing,
// right here in this file, instead of in some distant caller.
//
// You will see this pattern in every implementation file we write.
var _ Store = (*InMemStore)(nil)

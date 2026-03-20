package memory

import (
	"context"
	"errors"
	"testing"
	"time"
)

func newTestStore() *InMemStore {
	return NewInMemStore()
}

func TestInMemStore_SetAndGet(t *testing.T) {
	ctx := context.Background()
	s := newTestStore()

	// Set a value
	if err := s.Set(ctx, "key1", "hello", 0); err != nil {
		t.Fatalf("Set() error: %v", err)
	}

	// Get it back
	val, err := s.Get(ctx, "key1")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if val != "hello" {
		t.Errorf("Get() = %q, want %q", val, "hello")
	}
}

func TestInMemStore_Get_NotFound(t *testing.T) {
	ctx := context.Background()
	s := newTestStore()

	_, err := s.Get(ctx, "does-not-exist")

	// Must return ErrNotFound — not a generic error
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Get() on missing key should return ErrNotFound, got: %v", err)
	}
}

func TestInMemStore_Set_Overwrites(t *testing.T) {
	ctx := context.Background()
	s := newTestStore()

	_ = s.Set(ctx, "key", "first", 0)
	_ = s.Set(ctx, "key", "second", 0)

	val, _ := s.Get(ctx, "key")
	if val != "second" {
		t.Errorf("overwrite: got %q, want %q", val, "second")
	}
}

func TestInMemStore_TTL_Expiry(t *testing.T) {
	ctx := context.Background()
	s := newTestStore()

	// Store with a 50ms TTL
	_ = s.Set(ctx, "expiring", "value", 50*time.Millisecond)

	// Should be readable immediately
	val, err := s.Get(ctx, "expiring")
	if err != nil {
		t.Fatalf("Get() before expiry error: %v", err)
	}
	if val != "value" {
		t.Errorf("Get() before expiry = %q, want %q", val, "value")
	}

	// Wait for expiry
	time.Sleep(80 * time.Millisecond)

	// Should now return ErrNotFound
	_, err = s.Get(ctx, "expiring")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Get() after TTL expiry should return ErrNotFound, got: %v", err)
	}
}

func TestInMemStore_Delete(t *testing.T) {
	ctx := context.Background()
	s := newTestStore()

	_ = s.Set(ctx, "key", "value", 0)
	_ = s.Delete(ctx, "key")

	_, err := s.Get(ctx, "key")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Get() after Delete should return ErrNotFound, got: %v", err)
	}
}

func TestInMemStore_Delete_NonExistent(t *testing.T) {
	ctx := context.Background()
	s := newTestStore()

	// Deleting a non-existent key must not error
	if err := s.Delete(ctx, "ghost"); err != nil {
		t.Errorf("Delete() on non-existent key should not error, got: %v", err)
	}
}

func TestInMemStore_Append_And_History(t *testing.T) {
	ctx := context.Background()
	s := newTestStore()

	msgs := []Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
		{Role: "user", Content: "how are you?"},
	}

	for _, msg := range msgs {
		if err := s.Append(ctx, "hist", msg); err != nil {
			t.Fatalf("Append() error: %v", err)
		}
	}

	history, err := s.History(ctx, "hist", 0)
	if err != nil {
		t.Fatalf("History() error: %v", err)
	}

	if len(history) != 3 {
		t.Fatalf("History() returned %d messages, want 3", len(history))
	}

	// Messages must be in insertion order
	for i, msg := range history {
		if msg.Content != msgs[i].Content {
			t.Errorf("history[%d].Content = %q, want %q", i, msg.Content, msgs[i].Content)
		}
	}
}

func TestInMemStore_History_WithLimit(t *testing.T) {
	ctx := context.Background()
	s := newTestStore()

	for i := 0; i < 5; i++ {
		_ = s.Append(ctx, "hist", Message{Role: "user", Content: "msg"})
	}

	msgs := []Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
		{Role: "user", Content: "how are you?"},
	}

	for _, msg := range msgs {
		if err := s.Append(ctx, "hist", msg); err != nil {
			t.Fatalf("Append() error: %v", err)
		}
	}

	// Retrieve all
	history, _ := s.History(ctx, "hist", 0)
	if len(history) != 8 {
		t.Fatalf("History() returned %d messages, want 8", len(history))
	}

	// Request only last 3
	history, _ = s.History(ctx, "hist", 3)
	if len(history) != 3 {
		t.Fatalf("History(limit=3) returned %d messages, want 3", len(history))
	}
	// Check if it gets recent messages
	if history[len(history)-1].Content != "how are you?" {
		t.Errorf("Expected 'how are you?', got %q", history[len(history)-1].Content)
	}
}

func TestInMemStore_History_Empty(t *testing.T) {
	ctx := context.Background()
	s := newTestStore()

	// Empty history must return empty slice, not ErrNotFound
	history, err := s.History(ctx, "no-such-key", 0)
	if err != nil {
		t.Errorf("History() on empty key should not error, got: %v", err)
	}
	if len(history) != 0 {
		t.Errorf("History() on empty key should return empty slice, got %d messages", len(history))
	}
}

func TestInMemStore_ClearHistory(t *testing.T) {
	ctx := context.Background()
	s := newTestStore()

	_ = s.Append(ctx, "hist", Message{Role: "user", Content: "hello"})
	_ = s.ClearHistory(ctx, "hist")

	history, _ := s.History(ctx, "hist", 0)
	if len(history) != 0 {
		t.Errorf("History() after ClearHistory should be empty, got %d messages", len(history))
	}
}

func TestInMemStore_Append_SetsTimestamp(t *testing.T) {
	ctx := context.Background()
	s := newTestStore()

	before := time.Now()
	_ = s.Append(ctx, "hist", Message{Role: "user", Content: "hello"})
	after := time.Now()

	history, _ := s.History(ctx, "hist", 0)
	if len(history) == 0 {
		t.Fatal("expected 1 message")
	}

	ts := history[0].Timestamp
	if ts.Before(before) || ts.After(after) {
		t.Errorf("Append() timestamp %v outside expected range [%v, %v]", ts, before, after)
	}
}

func TestInMemStore_History_ReturnsCopy(t *testing.T) {
	ctx := context.Background()
	s := newTestStore()

	_ = s.Append(ctx, "hist", Message{Role: "user", Content: "original"})

	history, _ := s.History(ctx, "hist", 0)

	// Mutate the returned slice
	history[0].Content = "mutated"

	// Internal state must be unchanged
	history2, _ := s.History(ctx, "hist", 0)
	if history2[0].Content == "mutated" {
		t.Errorf("History() returned a reference to internal slice — should return a copy")
	}
}

func TestInMemStore_Concurrent_Access(t *testing.T) {
	// Run concurrent reads and writes to verify RWMutex prevents data races.
	// Run this test with: go test -race ./memory/...
	ctx := context.Background()
	s := newTestStore()

	done := make(chan struct{})

	// Writer goroutine
	go func() {
		for i := 0; i < 100; i++ {
			_ = s.Set(ctx, "key", "value", 0)
		}
		close(done)
	}()

	// Reader goroutine running concurrently
	for i := 0; i < 100; i++ {
		_, _ = s.Get(ctx, "key")
	}

	<-done
}

func TestInMemStore_Close(t *testing.T) {
	s := newTestStore()
	// Close on in-memory store must not error
	if err := s.Close(); err != nil {
		t.Errorf("Close() should not error, got: %v", err)
	}
}

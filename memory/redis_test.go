package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func TestNewRedisStore_InvalidURL(t *testing.T) {
	_, err := NewRedisStore("not-a-valid-redis-url", time.Hour)
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

func TestRedisStore_SetGetDelete(t *testing.T) {
	mr := miniredis.RunT(t)

	s, err := NewRedisStore("redis://"+mr.Addr(), time.Hour)
	if err != nil {
		t.Fatalf("NewRedisStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	ctx := context.Background()
	key, val := "k1", "hello"

	if err := s.Set(ctx, key, val, time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := s.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != val {
		t.Errorf("Get = %q, want %q", got, val)
	}
	if err := s.Delete(ctx, key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err = s.Get(ctx, key)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after delete: want ErrNotFound, got %v", err)
	}
}

func TestRedisStore_AppendHistoryClear(t *testing.T) {
	mr := miniredis.RunT(t)

	s, err := NewRedisStore("redis://"+mr.Addr(), time.Hour)
	if err != nil {
		t.Fatalf("NewRedisStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	ctx := context.Background()
	key := "hist:1"

	msgs, err := s.History(ctx, key, 0)
	if err != nil {
		t.Fatalf("History empty: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("History len = %d, want 0", len(msgs))
	}

	if err := s.Append(ctx, key, Message{Role: "user", Content: "a"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := s.Append(ctx, key, Message{Role: "assistant", Content: "b"}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	msgs, err = s.History(ctx, key, 0)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("History len = %d, want 2", len(msgs))
	}
	if msgs[0].Content != "a" || msgs[1].Content != "b" {
		t.Errorf("messages = %#v", msgs)
	}

	last1, err := s.History(ctx, key, 1)
	if err != nil {
		t.Fatalf("History limit 1: %v", err)
	}
	if len(last1) != 1 || last1[0].Content != "b" {
		t.Errorf("last message = %#v", last1)
	}

	if err := s.ClearHistory(ctx, key); err != nil {
		t.Fatalf("ClearHistory: %v", err)
	}
	msgs, err = s.History(ctx, key, 0)
	if err != nil {
		t.Fatalf("History after clear: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("want empty history, got %d msgs", len(msgs))
	}
}

func TestRedisStore_DefaultTTLWhenZero(t *testing.T) {
	mr := miniredis.RunT(t)

	s, err := NewRedisStore("redis://"+mr.Addr(), 0)
	if err != nil {
		t.Fatalf("NewRedisStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if s.defaultTTL != 24*time.Hour {
		t.Errorf("defaultTTL = %v, want 24h", s.defaultTTL)
	}
}

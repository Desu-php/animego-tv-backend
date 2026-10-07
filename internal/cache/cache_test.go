package cache

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestCancellationDoesNotCancelSharedLoad(t *testing.T) {
	c := New(2, time.Minute, time.Second, func(v []int) []int { return append([]int(nil), v...) })
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	load := func(ctx context.Context) ([]int, error) {
		calls.Add(1)
		close(started)
		select {
		case <-release:
			return []int{1}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	first, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := c.GetOrLoad(first, "a", load); done <- err }()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("caller cancellation lost: %v", err)
	}
	close(release)
	v, err := c.GetOrLoad(context.Background(), "a", load)
	if err != nil || calls.Load() != 1 || v[0] != 1 {
		t.Fatalf("shared load cancelled or duplicated: %v, %v", v, err)
	}
	v[0] = 99
	v, err = c.GetOrLoad(context.Background(), "a", load)
	if err != nil || v[0] != 1 {
		t.Fatal("caller mutated cached state")
	}
}

func TestExpiryEvictionAndFailedLoad(t *testing.T) {
	c := New(2, time.Minute, time.Second, func(v int) int { return v })
	now := time.Unix(1000, 0)
	c.now = func() time.Time { return now }
	calls := 0
	load := func(context.Context) (int, error) { calls++; return calls, nil }
	ctx := context.Background()
	for _, key := range []string{"a", "b", "a", "c", "b"} {
		if _, err := c.GetOrLoad(ctx, key, load); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 4 {
		t.Fatalf("LRU did not bound entries: %d loads", calls)
	}
	now = now.Add(time.Minute)
	if _, err := c.GetOrLoad(ctx, "b", load); err != nil || calls != 5 {
		t.Fatal("expired entry was reused")
	}
	sentinel := errors.New("failed load")
	fail := func(context.Context) (int, error) { calls++; return 0, sentinel }
	for i := 0; i < 2; i++ {
		if _, err := c.GetOrLoad(ctx, "failed", fail); !errors.Is(err, sentinel) {
			t.Fatal(err)
		}
	}
	if calls != 7 {
		t.Fatal("failed result was cached")
	}
}

func TestSharedLoadHasDeadline(t *testing.T) {
	c := New(1, time.Minute, 10*time.Millisecond, func(v int) int { return v })
	_, err := c.GetOrLoad(context.Background(), "a", func(ctx context.Context) (int, error) { <-ctx.Done(); return 0, ctx.Err() })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unbounded shared load: %v", err)
	}
}

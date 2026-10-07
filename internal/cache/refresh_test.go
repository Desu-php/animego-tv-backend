package cache

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestRefreshPersistsSharesAndThrottles(t *testing.T) {
	c := New(2, time.Minute, time.Second, func(v int) int { return v })
	now := time.Unix(1000, 0)
	c.now = func() time.Time { return now }
	ctx := context.Background()
	if _, err := c.GetOrLoad(ctx, "a", func(context.Context) (int, error) { return 1, nil }); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	load := func(context.Context) (int, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
		}
		return 2, nil
	}
	done := make(chan error, 12)
	for i := 0; i < cap(done); i++ {
		go func() {
			v, err := c.Refresh(ctx, "a", time.Second, load)
			if err == nil && v != 2 {
				err = errors.New("refresh returned old value")
			}
			done <- err
		}()
	}
	<-started
	close(release)
	for i := 0; i < cap(done); i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("refreshes were not shared: %d", calls.Load())
	}
	v, err := c.GetOrLoad(ctx, "a", func(context.Context) (int, error) {
		t.Error("refreshed value missing from cache")
		return 0, nil
	})
	if err != nil || v != 2 {
		t.Fatalf("cached refresh: %d, %v", v, err)
	}
	now = now.Add(time.Second)
	if _, err := c.Refresh(ctx, "a", time.Second, load); err != nil || calls.Load() != 2 {
		t.Fatalf("refresh interval did not expire: %d, %v", calls.Load(), err)
	}
}

func TestFailedRefreshPreservesValueAndCanRetry(t *testing.T) {
	c := New(1, time.Minute, time.Second, func(v int) int { return v })
	ctx := context.Background()
	_, _ = c.GetOrLoad(ctx, "a", func(context.Context) (int, error) { return 1, nil })
	sentinel := errors.New("upstream failed")
	if _, err := c.Refresh(ctx, "a", time.Second, func(context.Context) (int, error) { return 0, sentinel }); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if v, ok := c.lookup("a"); !ok || v != 1 {
		t.Fatal("failed refresh destroyed previous value")
	}
	if v, err := c.Refresh(ctx, "a", time.Second, func(context.Context) (int, error) { return 2, nil }); err != nil || v != 2 {
		t.Fatalf("failed refresh poisoned retry: %d, %v", v, err)
	}
}

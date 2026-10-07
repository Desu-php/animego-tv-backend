package animego

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"animego/backend/internal/apperror"
)

func TestRequestSlotSurvivesCacheCallerCancellation(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	releaseLoad := sync.OnceFunc(func() { close(release) })
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if strings.HasPrefix(r.URL.Path, "/anime/") {
			close(started)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			_, _ = io.WriteString(w, strings.ReplaceAll(fixture(t, "anime.html"), "https://animego.me", server.URL))
			return
		}
		_, _ = io.WriteString(w, fixture(t, "search.html"))
	}))
	defer server.Close()
	defer releaseLoad()
	client, err := NewClient(server.URL, &http.Client{Timeout: 2 * time.Second},
		WithCatalogCache(2, time.Minute, time.Minute), WithRequestConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := client.GetAnime(ctx, testAnimeID); done <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("cache load did not reach upstream")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("caller cancellation lost: %v", err)
	}
	for i := 0; i < 12; i++ {
		if _, err := client.Search(context.Background(), "test"); !errors.Is(err, apperror.ErrBusy) {
			t.Fatalf("detached load released its upstream slot: %v", err)
		}
	}
	releaseLoad()
	if _, err := client.GetAnime(context.Background(), testAnimeID); err != nil {
		t.Fatalf("departing caller canceled shared load: %v", err)
	}
	if _, err := client.Search(context.Background(), "test"); err != nil {
		t.Fatalf("completed load leaked its upstream slot: %v", err)
	}
}

func TestRequestSlotReleasedAfterFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, &http.Client{Timeout: time.Second}, WithRequestConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		_, err := client.Search(context.Background(), "test")
		var responseError interface{ UpstreamStatus() int }
		if !errors.As(err, &responseError) || responseError.UpstreamStatus() != http.StatusServiceUnavailable {
			t.Fatalf("failed request leaked its slot: %v", err)
		}
	}
}

func TestInvalidRequestConcurrency(t *testing.T) {
	for _, limit := range []int{0, -1} {
		if _, err := NewClient("https://animego.me", &http.Client{Timeout: time.Second}, WithRequestConcurrency(limit)); err == nil {
			t.Fatalf("accepted request concurrency %d", limit)
		}
	}
}

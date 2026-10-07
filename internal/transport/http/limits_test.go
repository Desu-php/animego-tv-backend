package httptransport_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"animego/backend/internal/domain"
	httptransport "animego/backend/internal/transport/http"
)

type heldCatalog struct{ entered, release chan struct{} }

func (f heldCatalog) Search(ctx context.Context, _ string) ([]domain.Anime, error) {
	f.entered <- struct{}{}
	select {
	case <-f.release:
		return []domain.Anime{}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (f heldCatalog) GetAnime(context.Context, string) (*domain.AnimeDetails, error) {
	return &domain.AnimeDetails{}, nil
}
func (f heldCatalog) GetEpisodes(context.Context, string) ([]domain.Episode, error) { return nil, nil }
func (f heldCatalog) GetTranslations(context.Context, string, string) ([]domain.Translation, error) {
	return nil, nil
}

type heldPlayback struct{ entered, release chan struct{} }

func (f heldPlayback) GetPlayback(ctx context.Context, _, _, _ string) (*domain.Playback, error) {
	f.entered <- struct{}{}
	select {
	case <-f.release:
		return &domain.Playback{}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func TestConcurrencyLimitsAreIndependentAndReleaseSlots(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	catalog := heldCatalog{make(chan struct{}, 4), release}
	playback := heldPlayback{make(chan struct{}, 4), release}
	router := httptransport.NewRouter(catalog, slog.New(slog.NewTextHandler(io.Discard, nil)), httptransport.WithPlayback(playback), httptransport.WithConcurrencyLimits(1, 1))
	serve := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}
	wait := func(ch <-chan struct{}) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatal("request did not enter handler")
		}
	}
	search := "/api/v1/anime/search?q=a"
	video := "/api/v1/anime/a-1/episodes/1/players/1:2:3/playback"
	done := make(chan int, 2)
	go func() { done <- serve(search).Code }()
	wait(catalog.entered)
	if w := serve(search); w.Code != 503 || w.Header().Get("Retry-After") != "1" {
		t.Fatalf("catalog overflow: %d", w.Code)
	}
	go func() { done <- serve(video).Code }()
	wait(playback.entered)
	if w := serve(video); w.Code != 503 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("playback overflow: %d", w.Code)
	}
	for _, path := range []string{"/health", "/openapi.json"} {
		if w := serve(path); w.Code != 200 {
			t.Fatal("API saturation blocked health/docs")
		}
	}
	unblock()
	for i := 0; i < 2; i++ {
		if code := <-done; code != 200 {
			t.Fatalf("held request failed: %d", code)
		}
	}
	if serve(search).Code != 200 || serve(video).Code != 200 {
		t.Fatal("completed requests leaked slots")
	}
}

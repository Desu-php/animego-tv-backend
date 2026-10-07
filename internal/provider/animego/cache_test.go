package animego

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"animego/backend/internal/domain"
	"errors"
)

func TestCatalogCacheSharesDataButKeepsMenusFresh(t *testing.T) {
	var detailsCalls, episodeCalls, menuCalls atomic.Int32
	var publishNew atomic.Bool
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/anime/"):
			detailsCalls.Add(1)
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, strings.ReplaceAll(fixture(t, "anime.html"), "https://animego.me", server.URL))
		case strings.HasSuffix(r.URL.Path, "/episodes"):
			episodeCalls.Add(1)
			content := fixture(t, "episodes.html")
			if publishNew.Load() {
				content = strings.ReplaceAll(content, "32411", "999")
			}
			sendEnvelope(t, w, episodePageResponse{Content: &content, Page: ptr(0), PageSize: ptr(100), Total: ptr(2)})
		case strings.HasPrefix(r.URL.Path, "/player/videos/"):
			menuCalls.Add(1)
			if r.URL.Path == "/player/videos/999" {
				sendEnvelope(t, w, videoResponse{NumVideos: ptr(0)})
				return
			}
			content := fixture(t, "translations.html")
			sendEnvelope(t, w, videoResponse{Content: &content, NumVideos: ptr(3)})
		default:
			t.Errorf("unexpected upstream request %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL, &http.Client{Timeout: time.Second}, WithCatalogCache(2, time.Minute, time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	details, err := client.GetAnime(ctx, testAnimeID)
	if err != nil {
		t.Fatal(err)
	}
	expectedTitle := details.Title
	details.Title = "changed"
	if len(details.Genres) > 0 {
		details.Genres[0] = "changed"
	}
	details, err = client.GetAnime(ctx, testAnimeID)
	if err != nil || details.Title != expectedTitle || len(details.Genres) > 0 && details.Genres[0] == "changed" {
		t.Fatal("details cache aliased caller data")
	}
	episodes, err := client.GetEpisodes(ctx, testAnimeID)
	if err != nil {
		t.Fatal(err)
	}
	episodes[0].ID = "changed"
	episodes, err = client.GetEpisodes(ctx, testAnimeID)
	if err != nil || episodes[0].ID == "changed" {
		t.Fatal("episode cache aliased caller data")
	}
	for i := 0; i < 2; i++ {
		if _, err = client.GetTranslations(ctx, testAnimeID, "32410"); err != nil {
			t.Fatal(err)
		}
	}
	if detailsCalls.Load() != 1 || episodeCalls.Load() != 1 || menuCalls.Load() != 2 {
		t.Fatalf("unexpected requests: details=%d episodes=%d menus=%d", detailsCalls.Load(), episodeCalls.Load(), menuCalls.Load())
	}
	publishNew.Store(true)
	if _, err = client.GetTranslations(ctx, testAnimeID, "999"); err != nil {
		t.Fatalf("new episode blocked by stale cache: %v", err)
	}
	episodes, err = client.GetEpisodes(ctx, testAnimeID)
	if err != nil || len(episodes) != 2 || episodes[1].ID != "999" {
		t.Fatalf("refreshed list was not persisted: %v, %v", episodes, err)
	}
	if _, err = client.GetTranslations(ctx, testAnimeID, "999"); err != nil {
		t.Fatalf("refreshed episode was not reused: %v", err)
	}
	if _, err = client.GetTranslations(ctx, testAnimeID, "998"); !errors.Is(err, domain.ErrEpisodeNotFound) {
		t.Fatalf("foreign episode accepted: %v", err)
	}
	if _, err = client.GetTranslations(ctx, testAnimeID, "997"); !errors.Is(err, domain.ErrEpisodeNotFound) {
		t.Fatalf("foreign episode accepted: %v", err)
	}
	if episodeCalls.Load() != 2 {
		t.Fatalf("missing IDs triggered redundant refreshes: %d", episodeCalls.Load())
	}
	if menuCalls.Load() != 4 {
		t.Fatal("ownership failure reached player menu")
	}
}

func TestInvalidCacheConfiguration(t *testing.T) {
	for _, option := range []ClientOption{WithCatalogCache(-1, time.Minute, time.Minute), WithCatalogCache(1, 0, time.Minute), WithCatalogCache(1, time.Minute, 0)} {
		if _, err := NewClient("https://animego.me", &http.Client{Timeout: time.Second}, option); err == nil {
			t.Fatal("invalid cache bounds accepted")
		}
	}
}

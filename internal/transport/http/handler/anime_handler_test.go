package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"animego/backend/internal/domain"
	"animego/backend/internal/provider"
	"animego/backend/internal/service"
	httptransport "animego/backend/internal/transport/http"
)

type fakeProvider struct {
	search          func(context.Context, string) ([]domain.Anime, error)
	getAnime        func(context.Context, string) (*domain.AnimeDetails, error)
	getEpisodes     func(context.Context, string) ([]domain.Episode, error)
	getTranslations func(context.Context, string, string) ([]domain.Translation, error)
	getPlayer       func(context.Context, string, string, string) (*domain.PlayerSelection, error)
}

func (p fakeProvider) GetPlayer(ctx context.Context, animeID, episodeID, playerID string) (*domain.PlayerSelection, error) {
	if p.getPlayer == nil {
		return nil, domain.ErrPlayerNotFound
	}
	return p.getPlayer(ctx, animeID, episodeID, playerID)
}

func (p fakeProvider) GetAnime(ctx context.Context, id string) (*domain.AnimeDetails, error) {
	if p.getAnime == nil {
		return nil, domain.ErrAnimeNotFound
	}
	return p.getAnime(ctx, id)
}
func (p fakeProvider) GetEpisodes(ctx context.Context, id string) ([]domain.Episode, error) {
	if p.getEpisodes == nil {
		return nil, nil
	}
	return p.getEpisodes(ctx, id)
}
func (p fakeProvider) GetTranslations(ctx context.Context, animeID, episodeID string) ([]domain.Translation, error) {
	if p.getTranslations == nil {
		return nil, nil
	}
	return p.getTranslations(ctx, animeID, episodeID)
}

func (p fakeProvider) Search(ctx context.Context, query string) ([]domain.Anime, error) {
	return p.search(ctx, query)
}

func logger() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

func TestHandlerValidation(t *testing.T) {
	router := httptransport.NewRouter(service.NewAnimeService(fakeProvider{search: func(context.Context, string) ([]domain.Anime, error) {
		t.Fatal("invalid query reached provider")
		return nil, nil
	}}), logger())
	for _, query := range []string{"", "?q=", "?q=" + url.QueryEscape(" \t\u3000"), "?q=" + url.QueryEscape(strings.Repeat("я", 201)), "?q=%FF"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/anime/search"+query, nil))
		if w.Code != http.StatusBadRequest || !json.Valid(w.Body.Bytes()) {
			t.Errorf("query %q: status %d, body %s", query, w.Code, w.Body.String())
		}
	}
}

func TestHandlerSuccessAndEmptyResults(t *testing.T) {
	for _, results := range [][]domain.Anime{nil, {{ID: "anime-1", Title: "Аниме", Year: 2024}}} {
		router := httptransport.NewRouter(service.NewAnimeService(fakeProvider{search: func(ctx context.Context, query string) ([]domain.Anime, error) {
			if query != "Аниме" {
				t.Errorf("expected trimmed Unicode query, got %q", query)
			}
			return results, nil
		}}), logger())
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/anime/search?q="+url.QueryEscape(" Аниме "), nil))
		var body struct {
			Data []domain.Anime `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if w.Code != http.StatusOK || body.Data == nil || len(body.Data) != len(results) {
			t.Fatalf("unexpected response: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestHandlerErrorMapping(t *testing.T) {
	cases := []struct {
		err  error
		code int
	}{
		{&provider.HTTPError{StatusCode: 503, Err: errors.New("private upstream detail")}, 502},
		{fmt.Errorf("private upstream detail: %w", provider.ErrParsing), 502},
		{errors.New("private upstream detail"), 500},
	}
	for _, tc := range cases {
		router := httptransport.NewRouter(service.NewAnimeService(fakeProvider{search: func(context.Context, string) ([]domain.Anime, error) { return nil, tc.err }}), logger())
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/anime/search?q=anime", nil))
		if w.Code != tc.code || !json.Valid(w.Body.Bytes()) || strings.Contains(w.Body.String(), "private") {
			t.Fatalf("unexpected error response: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestHealthAndPanicRecovery(t *testing.T) {
	router := httptransport.NewRouter(service.NewAnimeService(fakeProvider{search: func(context.Context, string) ([]domain.Anime, error) { panic("private panic") }}), logger())
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
	if w.Code != 200 || w.Body.String() != `{"status":"ok"}` {
		t.Fatalf("unexpected health: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/anime/search?q=anime", nil))
	if w.Code != 500 || strings.Contains(w.Body.String(), "private") {
		t.Fatalf("panic leaked: %d %s", w.Code, w.Body.String())
	}
}

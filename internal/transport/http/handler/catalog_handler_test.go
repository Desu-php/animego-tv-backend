package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"animego/backend/internal/domain"
	"animego/backend/internal/provider"
	"animego/backend/internal/service"
	httptransport "animego/backend/internal/transport/http"
)

func TestCatalogEndpoints(t *testing.T) {
	router := httptransport.NewRouter(service.NewAnimeService(fakeProvider{
		getAnime: func(ctx context.Context, id string) (*domain.AnimeDetails, error) {
			if id != "example-1" {
				t.Error("wrong ID")
			}
			return &domain.AnimeDetails{Anime: domain.Anime{ID: id, Title: "Example"}, Type: "tv"}, nil
		},
		getEpisodes: func(ctx context.Context, id string) ([]domain.Episode, error) {
			return []domain.Episode{{ID: "12", Number: 1}}, nil
		},
		getTranslations: func(ctx context.Context, id, episode string) ([]domain.Translation, error) {
			if id != "example-1" || episode != "12" {
				t.Error("wrong IDs")
			}
			return []domain.Translation{{ID: "2", Name: "Test", Players: []domain.PlayerReference{{ID: "12:2:19", Provider: "kodik"}}}}, nil
		},
	}), logger())
	for _, path := range []string{"/api/v1/anime/example-1", "/api/v1/anime/example-1/episodes", "/api/v1/anime/example-1/episodes/12/translations"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || !json.Valid(w.Body.Bytes()) || !strings.Contains(w.Body.String(), `"data":`) {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
}

func TestCatalogEndpointErrors(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{
		{domain.ErrAnimeNotFound, 404}, {domain.ErrEpisodeNotFound, 404}, {provider.ErrParsing, 502}, {&provider.HTTPError{StatusCode: 503, Err: errors.New("private token")}, 502}, {errors.New("private token"), 500},
	} {
		router := httptransport.NewRouter(service.NewAnimeService(fakeProvider{
			getAnime:        func(context.Context, string) (*domain.AnimeDetails, error) { return nil, tc.err },
			getEpisodes:     func(context.Context, string) ([]domain.Episode, error) { return nil, tc.err },
			getTranslations: func(context.Context, string, string) ([]domain.Translation, error) { return nil, tc.err },
		}), logger())
		for _, path := range []string{"/api/v1/anime/example-1", "/api/v1/anime/example-1/episodes", "/api/v1/anime/example-1/episodes/12/translations"} {
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
			if w.Code != tc.status || strings.Contains(w.Body.String(), "private") {
				t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
			}
		}
	}
}

func TestCatalogEndpointValidationAndEmptyArrays(t *testing.T) {
	router := httptransport.NewRouter(service.NewAnimeService(fakeProvider{}), logger())
	for _, path := range []string{"/api/v1/anime/%2e%2e", "/api/v1/anime/example%252f1", "/api/v1/anime/" + strings.Repeat("a", 301), "/api/v1/anime/example-1/episodes/-1/translations", "/api/v1/anime/example-1/episodes/0/translations"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 400 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/api/v1/anime/example-1/episodes", "/api/v1/anime/example-1/episodes/12/translations"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || w.Body.String() != `{"data":[]}` {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
}

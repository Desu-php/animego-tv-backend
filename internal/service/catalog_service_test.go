package service

import (
	"context"
	"errors"
	"testing"

	"animego/backend/internal/domain"
)

func TestCatalogServicePropagation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	check := func(gotCtx context.Context, id string) {
		if gotCtx != ctx || id != "example-1" {
			t.Errorf("wrong context/id: %v, %q", gotCtx, id)
		}
	}
	svc := NewAnimeService(fakeProvider{
		getAnime: func(c context.Context, id string) (*domain.AnimeDetails, error) {
			check(c, id)
			return &domain.AnimeDetails{Anime: domain.Anime{ID: id, Title: "Example"}}, nil
		},
		getEpisodes: func(c context.Context, id string) ([]domain.Episode, error) { check(c, id); return nil, nil },
		getTranslations: func(c context.Context, id, episode string) ([]domain.Translation, error) {
			check(c, id)
			if episode != "12" {
				t.Error("wrong episode")
			}
			return nil, nil
		},
	})
	if got, err := svc.GetAnime(ctx, "example-1"); err != nil || got.Title != "Example" {
		t.Fatalf("details: %#v, %v", got, err)
	}
	if got, err := svc.GetEpisodes(ctx, "example-1"); err != nil || got == nil {
		t.Fatalf("episodes: %#v, %v", got, err)
	}
	if got, err := svc.GetTranslations(ctx, "example-1", "12"); err != nil || got == nil {
		t.Fatalf("translations: %#v, %v", got, err)
	}
}

func TestCatalogServiceValidationAndErrors(t *testing.T) {
	svc := NewAnimeService(fakeProvider{})
	if _, err := svc.GetAnime(context.Background(), "../1"); !errors.Is(err, domain.ErrInvalidIdentifier) {
		t.Fatal(err)
	}
	if _, err := svc.GetEpisodes(context.Background(), "https://example.com"); !errors.Is(err, domain.ErrInvalidIdentifier) {
		t.Fatal(err)
	}
	if _, err := svc.GetTranslations(context.Background(), "example-1", "../12"); !errors.Is(err, domain.ErrInvalidIdentifier) {
		t.Fatal(err)
	}
	want := errors.New("private provider error")
	svc = NewAnimeService(fakeProvider{
		getAnime:        func(context.Context, string) (*domain.AnimeDetails, error) { return nil, want },
		getEpisodes:     func(context.Context, string) ([]domain.Episode, error) { return nil, want },
		getTranslations: func(context.Context, string, string) ([]domain.Translation, error) { return nil, want },
	})
	_, animeErr := svc.GetAnime(context.Background(), "example-1")
	_, episodeErr := svc.GetEpisodes(context.Background(), "example-1")
	_, translationErr := svc.GetTranslations(context.Background(), "example-1", "12")
	for _, err := range []error{animeErr, episodeErr, translationErr} {
		if err == want || !errors.Is(err, want) {
			t.Fatalf("error not wrapped: %v", err)
		}
	}
	svc = NewAnimeService(fakeProvider{getAnime: func(context.Context, string) (*domain.AnimeDetails, error) { return nil, nil }})
	if _, err := svc.GetAnime(context.Background(), "example-1"); err == nil {
		t.Fatal("nil provider details accepted")
	}
}

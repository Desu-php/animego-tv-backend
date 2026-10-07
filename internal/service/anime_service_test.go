package service

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"animego/backend/internal/domain"
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

func TestSearchTrimsQueryAndPreservesContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	want := []domain.Anime{{ID: "anime-1", Title: "Аниме"}}
	svc := NewAnimeService(fakeProvider{search: func(gotCtx context.Context, query string) ([]domain.Anime, error) {
		if gotCtx != ctx || query != "Аниме" {
			t.Errorf("context/query not preserved: %v, %q", gotCtx, query)
		}
		return want, nil
	}})
	got, err := svc.Search(ctx, " \tАниме\u3000")
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, %v", got, err)
	}
}

func TestSearchValidation(t *testing.T) {
	svc := NewAnimeService(fakeProvider{search: func(context.Context, string) ([]domain.Anime, error) {
		t.Fatal("provider called for invalid query")
		return nil, nil
	}})
	for _, query := range []string{"", " \t\n\u3000", strings.Repeat("я", MaxQueryLength+1), string([]byte{0xff})} {
		_, err := svc.Search(context.Background(), query)
		var validation *domain.ValidationError
		if !errors.Is(err, domain.ErrInvalidQuery) || !errors.As(err, &validation) {
			t.Errorf("expected validation error for %q, got %v", query, err)
		}
	}
}

func TestSearchUnicodeLimitAndEmptyArray(t *testing.T) {
	svc := NewAnimeService(fakeProvider{search: func(context.Context, string) ([]domain.Anime, error) { return nil, nil }})
	got, err := svc.Search(context.Background(), strings.Repeat("я", MaxQueryLength))
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("expected non-nil empty results: %#v, %v", got, err)
	}
}

func TestSearchWrapsProviderError(t *testing.T) {
	want := errors.New("provider failure")
	svc := NewAnimeService(fakeProvider{search: func(context.Context, string) ([]domain.Anime, error) { return nil, want }})
	_, err := svc.Search(context.Background(), "anime")
	if !errors.Is(err, want) || err == want {
		t.Fatalf("provider error not wrapped: %v", err)
	}
}

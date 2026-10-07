package service

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"animego/backend/internal/domain"
)

const MaxQueryLength = 200 // Unicode code points, measured after trimming.

type AnimeService struct {
	provider CatalogProvider
}

func NewAnimeService(p CatalogProvider) *AnimeService {
	return &AnimeService{provider: p}
}

func (s *AnimeService) Search(ctx context.Context, query string) ([]domain.Anime, error) {
	query = strings.TrimSpace(query)
	if !utf8.ValidString(query) || query == "" || utf8.RuneCountInString(query) > MaxQueryLength {
		return nil, &domain.ValidationError{Reason: fmt.Sprintf("q must contain 1 to %d valid Unicode characters", MaxQueryLength)}
	}
	results, err := s.provider.Search(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("search anime catalog: %w", err)
	}
	if results == nil {
		results = make([]domain.Anime, 0)
	}
	return results, nil
}

func (s *AnimeService) GetAnime(ctx context.Context, id string) (*domain.AnimeDetails, error) {
	if !domain.ValidAnimeID(id) {
		return nil, domain.ErrInvalidIdentifier
	}
	result, err := s.provider.GetAnime(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get anime details: %w", err)
	}
	if result == nil {
		return nil, fmt.Errorf("provider returned nil anime details")
	}
	return result, nil
}

func (s *AnimeService) GetEpisodes(ctx context.Context, animeID string) ([]domain.Episode, error) {
	if !domain.ValidAnimeID(animeID) {
		return nil, domain.ErrInvalidIdentifier
	}
	results, err := s.provider.GetEpisodes(ctx, animeID)
	if err != nil {
		return nil, fmt.Errorf("get anime episodes: %w", err)
	}
	if results == nil {
		results = make([]domain.Episode, 0)
	}
	return results, nil
}

func (s *AnimeService) GetTranslations(ctx context.Context, animeID, episodeID string) ([]domain.Translation, error) {
	if !domain.ValidAnimeID(animeID) || !domain.ValidEpisodeID(episodeID) {
		return nil, domain.ErrInvalidIdentifier
	}
	results, err := s.provider.GetTranslations(ctx, animeID, episodeID)
	if err != nil {
		return nil, fmt.Errorf("get episode translations: %w", err)
	}
	if results == nil {
		results = make([]domain.Translation, 0)
	}
	return results, nil
}

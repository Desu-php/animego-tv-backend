package provider

import (
	"context"
	"errors"
	"fmt"

	"animego/backend/internal/domain"
)

type AnimeProvider interface {
	Search(ctx context.Context, query string) ([]domain.Anime, error)
	GetAnime(ctx context.Context, id string) (*domain.AnimeDetails, error)
	GetEpisodes(ctx context.Context, animeID string) ([]domain.Episode, error)
	GetTranslations(ctx context.Context, animeID, episodeID string) ([]domain.Translation, error)
	GetPlayer(ctx context.Context, animeID, episodeID, playerID string) (*domain.PlayerSelection, error)
}

var (
	ErrUpstream                 = errors.New("upstream provider failure")
	ErrHTTP                     = fmt.Errorf("%w: HTTP failure", ErrUpstream)
	ErrParsing                  = fmt.Errorf("%w: parsing failure", ErrUpstream)
	ErrUpstreamStructureChanged = ErrParsing
)

// HTTPError represents transport failures, rejected responses, and body errors.
// StatusCode is zero when no HTTP response was received.
type HTTPError struct {
	StatusCode int
	Err        error
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("provider HTTP request (status %d): %v", e.StatusCode, e.Err)
}

func (e *HTTPError) Unwrap() []error { return []error{ErrHTTP, e.Err} }

// UpstreamStatus exposes safe diagnostics without leaking adapter types into use cases.
func (e *HTTPError) UpstreamStatus() int { return e.StatusCode }

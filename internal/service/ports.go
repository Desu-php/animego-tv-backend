package service

import (
	"context"

	"animego/backend/internal/domain"
)

// Ports are owned by the use cases; network adapters implement them.
type CatalogProvider interface {
	Search(context.Context, string) ([]domain.Anime, error)
	GetAnime(context.Context, string) (*domain.AnimeDetails, error)
	GetEpisodes(context.Context, string) ([]domain.Episode, error)
	GetTranslations(context.Context, string, string) ([]domain.Translation, error)
}

type PlayerProvider interface {
	GetPlayer(context.Context, string, string, string) (*domain.PlayerSelection, error)
}

type SourceResolver interface {
	Resolve(context.Context, domain.PlayerSelection) ([]domain.VideoSource, error)
}

package animego

import (
	"context"
	"fmt"
	"time"

	"animego/backend/internal/cache"
	"animego/backend/internal/domain"
)

// WithCatalogCache caches only parsed catalog data, never player menus or media URLs.
// Zero capacity disables caching. Bounds are validated by NewClient.
func WithCatalogCache(capacity int, detailsTTL, episodesTTL time.Duration) ClientOption {
	return func(c *Client) {
		c.cacheCapacity, c.detailsTTL, c.episodesTTL = capacity, detailsTTL, episodesTTL
	}
}

func (c *Client) initializeCache() error {
	if c.cacheCapacity == 0 {
		return nil
	}
	if c.cacheCapacity < 0 || c.detailsTTL <= 0 || c.episodesTTL <= 0 {
		return fmt.Errorf("catalog cache requires positive capacity and TTLs")
	}
	c.pages = cache.New(c.cacheCapacity, c.detailsTTL, c.httpClient.Timeout, clonePage)
	c.episodes = cache.New(c.cacheCapacity, c.episodesTTL, c.httpClient.Timeout, cloneEpisodes)
	return nil
}

func clonePage(page *animePage) *animePage {
	copyPage := *page
	details := *page.details
	details.Genres = append([]string(nil), details.Genres...)
	copyPage.details = &details
	return &copyPage
}

func cloneEpisodes(episodes []domain.Episode) []domain.Episode {
	result := make([]domain.Episode, len(episodes))
	copy(result, episodes)
	return result
}

func (c *Client) loadAnime(ctx context.Context, id string) (*animePage, error) {
	if !domain.ValidAnimeID(id) {
		return nil, domain.ErrInvalidIdentifier
	}
	if c.pages == nil {
		return c.fetchAnime(ctx, id)
	}
	return c.pages.GetOrLoad(ctx, id, func(ctx context.Context) (*animePage, error) {
		return c.fetchAnime(ctx, id)
	})
}

func (c *Client) loadEpisodes(ctx context.Context, anime *animePage) ([]domain.Episode, error) {
	if c.episodes == nil {
		return c.fetchEpisodes(ctx, anime)
	}
	return c.episodes.GetOrLoad(ctx, anime.details.ID, func(ctx context.Context) ([]domain.Episode, error) {
		return c.fetchEpisodes(ctx, anime)
	})
}

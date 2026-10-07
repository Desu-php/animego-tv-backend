package animego

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"

	"animego/backend/internal/domain"
	"animego/backend/internal/provider"
)

// Bound the entire operation, including all episode pages, by the client timeout.
func (c *Client) GetAnime(ctx context.Context, id string) (*domain.AnimeDetails, error) {
	ctx, cancel := context.WithTimeout(ctx, c.httpClient.Timeout)
	defer cancel()
	page, err := c.loadAnime(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get AnimeGO anime: %w", err)
	}
	return page.details, nil
}

func (c *Client) GetEpisodes(ctx context.Context, animeID string) ([]domain.Episode, error) {
	ctx, cancel := context.WithTimeout(ctx, c.httpClient.Timeout)
	defer cancel()
	page, err := c.loadAnime(ctx, animeID)
	if err != nil {
		return nil, fmt.Errorf("get AnimeGO episodes: %w", err)
	}
	episodes, err := c.loadEpisodes(ctx, page)
	if err != nil {
		return nil, fmt.Errorf("get AnimeGO episodes: %w", err)
	}
	return episodes, nil
}

func (c *Client) GetTranslations(ctx context.Context, animeID, episodeID string) ([]domain.Translation, error) {
	_, result, err := c.loadTranslations(ctx, animeID, episodeID)
	if err != nil {
		return nil, err
	}
	return result.translations, nil
}

func (c *Client) GetPlayer(ctx context.Context, animeID, episodeID, playerID string) (*domain.PlayerSelection, error) {
	if !domain.ValidPlayerID(playerID, episodeID) {
		return nil, domain.ErrInvalidIdentifier
	}
	page, result, err := c.loadTranslations(ctx, animeID, episodeID)
	if err != nil {
		return nil, fmt.Errorf("get AnimeGO player: %w", err)
	}
	ref, exists := result.players[playerID]
	if !exists {
		return nil, domain.ErrPlayerNotFound
	}
	return &domain.PlayerSelection{AnimeID: page.details.ID, EpisodeID: episodeID, TranslationID: ref.TranslationID, Player: ref.Reference, EmbedURL: ref.URL.String()}, nil
}

func (c *Client) loadTranslations(ctx context.Context, animeID, episodeID string) (*animePage, *translationSet, error) {
	if !domain.ValidEpisodeID(episodeID) {
		return nil, nil, domain.ErrInvalidIdentifier
	}
	ctx, cancel := context.WithTimeout(ctx, c.httpClient.Timeout)
	defer cancel()
	page, err := c.loadAnime(ctx, animeID)
	if err != nil {
		return nil, nil, fmt.Errorf("get AnimeGO translations: %w", err)
	}
	episodes, err := c.loadEpisodes(ctx, page)
	if err != nil {
		return nil, nil, fmt.Errorf("check AnimeGO episode ownership: %w", err)
	}
	found := false
	for _, episode := range episodes {
		if episode.ID == episodeID {
			found = true
			break
		}
	}
	if !found && c.episodes != nil {
		// A newly released episode must not wait for the cache TTL to expire.
		if page.playerPath == "" {
			page, err = c.refreshAnime(ctx, animeID)
			if err != nil {
				return nil, nil, err
			}
		}
		episodes, err = c.refreshEpisodes(ctx, page)
		if err != nil {
			return nil, nil, fmt.Errorf("refresh episode ownership: %w", err)
		}
		for _, episode := range episodes {
			if episode.ID == episodeID {
				found = true
				break
			}
		}
	}
	if !found {
		return nil, nil, domain.ErrEpisodeNotFound
	}
	data, _, err := c.request(ctx, "/player/videos/"+episodeID, nil, requestOptions{accept: "application/json", referer: "/anime/" + page.details.ID, operation: "translations", animeID: animeID, episodeID: episodeID})
	if err != nil {
		return nil, nil, mapNotFound(err, domain.ErrEpisodeNotFound)
	}
	var videos videoResponse
	if err := decodeEnvelope(data, &videos); err != nil {
		return nil, nil, err
	}
	result, err := parseTranslations(videos, episodeID, c.baseURL)
	if err != nil {
		return nil, nil, fmt.Errorf("parse AnimeGO translations: %w", err)
	}
	return page, result, nil
}

func (c *Client) fetchAnime(ctx context.Context, id string) (*animePage, error) {
	if !domain.ValidAnimeID(id) {
		return nil, domain.ErrInvalidIdentifier
	}
	data, responseURL, err := c.request(ctx, "/anime/"+id, nil, requestOptions{accept: "text/html", operation: "details", animeID: id})
	if err != nil {
		return nil, mapNotFound(err, domain.ErrAnimeNotFound)
	}
	page, err := parseAnime(bytes.NewReader(data), responseURL)
	if err != nil {
		return nil, fmt.Errorf("parse AnimeGO details: %w", err)
	}
	return page, nil
}

func (c *Client) fetchEpisodes(ctx context.Context, anime *animePage) ([]domain.Episode, error) {
	results := make([]domain.Episode, 0)
	if anime.playerPath == "" {
		return results, nil
	}
	seen := make(map[string]bool)
	total, pageSize := -1, 0
	for pageNumber := 0; ; pageNumber++ {
		data, _, err := c.request(ctx, anime.playerPath+"/episodes", url.Values{"page": {strconv.Itoa(pageNumber)}}, requestOptions{accept: "application/json", referer: "/anime/" + anime.details.ID, operation: "episodes", animeID: anime.details.ID})
		if err != nil {
			return nil, fmt.Errorf("request episode page %d: %w", pageNumber, err)
		}
		var response episodePageResponse
		if err := decodeEnvelope(data, &response); err != nil {
			return nil, err
		}
		page, err := parseEpisodePage(response)
		if err != nil {
			return nil, err
		}
		if page.page != pageNumber || total != -1 && (page.total != total || page.pageSize != pageSize) {
			return nil, fmt.Errorf("%w: episode pagination changed during request", provider.ErrParsing)
		}
		total, pageSize = page.total, page.pageSize
		if (total+pageSize-1)/pageSize > 100 {
			return nil, fmt.Errorf("%w: episode pagination exceeds 100 pages", provider.ErrParsing)
		}
		for _, episode := range page.episodes {
			if seen[episode.ID] {
				return nil, fmt.Errorf("%w: duplicate episode across pages", provider.ErrParsing)
			}
			seen[episode.ID] = true
			results = append(results, episode)
		}
		if len(results) == total {
			break
		}
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].Number < results[j].Number })
	return results, nil
}

func decodeEnvelope(data []byte, target any) error {
	var envelope struct {
		Status string          `json:"status"`
		Data   json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("%w: decode AnimeGO JSON: %w", provider.ErrParsing, err)
	}
	if envelope.Status != "success" || len(envelope.Data) == 0 || bytes.Equal(envelope.Data, []byte("null")) {
		return fmt.Errorf("%w: invalid AnimeGO success envelope", provider.ErrParsing)
	}
	if err := json.Unmarshal(envelope.Data, target); err != nil {
		return fmt.Errorf("%w: decode AnimeGO data: %w", provider.ErrParsing, err)
	}
	return nil
}

func mapNotFound(err, notFound error) error {
	var httpError *provider.HTTPError
	if errors.As(err, &httpError) && httpError.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w: %w", notFound, err)
	}
	return err
}

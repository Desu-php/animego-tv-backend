package animego

import (
	"bytes"
	"context"
	"fmt"
	"net/url"

	"animego/backend/internal/domain"
)

const searchPath = "/search/anime"

func (c *Client) Search(ctx context.Context, query string) ([]domain.Anime, error) {
	body, responseURL, err := c.getHTML(ctx, searchPath, url.Values{"q": {query}})
	if err != nil {
		return nil, fmt.Errorf("AnimeGO search: %w", err)
	}
	results, err := parseSearch(bytes.NewReader(body), responseURL)
	if err != nil {
		return nil, fmt.Errorf("parse AnimeGO search: %w", err)
	}
	return results, nil
}

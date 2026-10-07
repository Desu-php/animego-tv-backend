package animego

import (
	"fmt"
	"strconv"
	"strings"

	"animego/backend/internal/domain"
	"animego/backend/internal/provider"
	"github.com/PuerkitoBio/goquery"
)

const episodeSelector = `.player-video-bar__item[data-anime-player-episodes-target~="episode"]`

const maxEpisodes = 10000

type episodePageResponse struct {
	Content  *string `json:"content"`
	Page     *int    `json:"page"`
	PageSize *int    `json:"pageSize"`
	Total    *int    `json:"total"`
}

type episodePage struct {
	episodes              []domain.Episode
	page, pageSize, total int
}

func parseEpisodePage(data episodePageResponse) (*episodePage, error) {
	if data.Content == nil || data.Page == nil || data.PageSize == nil || data.Total == nil || *data.Page < 0 || *data.PageSize <= 0 || *data.PageSize > maxEpisodes || *data.Total < 0 || *data.Total > maxEpisodes {
		return nil, fmt.Errorf("%w: missing or invalid episode pagination fields", provider.ErrParsing)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(*data.Content))
	if err != nil {
		return nil, fmt.Errorf("%w: read episode HTML: %w", provider.ErrParsing, err)
	}
	page := &episodePage{episodes: make([]domain.Episode, 0), page: *data.Page, pageSize: *data.PageSize, total: *data.Total}
	var cardError error
	seen := make(map[string]bool)
	doc.Find(episodeSelector).Each(func(_ int, card *goquery.Selection) {
		id, _ := card.Attr("data-episode")
		rawNumber, _ := card.Attr("data-episode-number")
		number, err := strconv.Atoi(rawNumber)
		if !domain.ValidEpisodeID(id) || err != nil || number < 0 || number > maxEpisodes || seen[id] {
			cardError = fmt.Errorf("%w: invalid or duplicate episode identity", provider.ErrParsing)
			return
		}
		seen[id] = true
		page.episodes = append(page.episodes, domain.Episode{ID: id, Number: number})
	})
	if cardError != nil {
		return nil, cardError
	}
	expected := page.total - page.page*page.pageSize
	if expected > page.pageSize {
		expected = page.pageSize
	}
	if expected < 0 {
		expected = 0
	}
	if len(page.episodes) != expected || page.total == 0 && strings.TrimSpace(*data.Content) != "" {
		return nil, fmt.Errorf("%w: episode count does not match pagination", provider.ErrParsing)
	}
	return page, nil
}

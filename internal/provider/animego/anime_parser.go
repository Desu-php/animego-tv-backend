package animego

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"

	"animego/backend/internal/domain"
	"animego/backend/internal/provider"
	"github.com/PuerkitoBio/goquery"
)

const (
	animeRootSelector        = ".entity"
	animeTitleSelector       = ".entity__title h1"
	animePosterSelector      = ".entity__poster img"
	animeDescriptionSelector = ".description"
	animeFieldsSelector      = ".entity__info .entity-field"
	canonicalSelector        = `link[rel="canonical"]`
	schemaSelector           = `script[type="application/ld+json"]`
	playerLoaderSelector     = `[data-controller~="anime-player-loader"]`
	playerLoaderAttribute    = "data-anime-player-loader-url-value"
)

type animePage struct {
	details    *domain.AnimeDetails
	playerPath string
	numericID  string
}

type animeSchema struct {
	Type          string   `json:"@type"`
	Name          string   `json:"name"`
	AltTitle      string   `json:"alternateName"`
	Description   string   `json:"description"`
	Image         string   `json:"image"`
	Genres        []string `json:"genre"`
	DatePublished string   `json:"datePublished"`
	Episodes      int      `json:"numberOfEpisodes"`
}

func parseAnime(r io.Reader, responseURL *url.URL) (*animePage, error) {
	doc, err := goquery.NewDocumentFromReader(r)
	if err != nil {
		return nil, fmt.Errorf("%w: read anime HTML: %w", provider.ErrParsing, err)
	}
	root := doc.Find(animeRootSelector)
	title := cleanText(root.Find(animeTitleSelector).Text())
	canonical, _ := doc.Find(canonicalSelector).Attr("href")
	u, ok := resolveHTTPURL(responseURL, canonical)
	if root.Length() != 1 || title == "" || !ok || !sameOrigin(responseURL, u) || !strings.HasPrefix(u.Path, "/anime/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("%w: missing anime title, entity, or canonical detail path", provider.ErrParsing)
	}
	id := strings.TrimPrefix(u.Path, "/anime/")
	if !domain.ValidAnimeID(id) {
		return nil, fmt.Errorf("%w: invalid canonical anime identifier", provider.ErrParsing)
	}
	page := &animePage{details: &domain.AnimeDetails{Anime: domain.Anime{ID: id, Title: title}}}
	details := page.details
	poster, _ := root.Find(animePosterSelector).First().Attr("src")
	if u, ok := resolveHTTPURL(responseURL, poster); ok {
		details.Poster = u.String()
	}
	details.Description = cleanText(root.Find(animeDescriptionSelector).First().Text())
	// JSON-LD is supplementary: the visible anime entity is the structure anchor.
	doc.Find(schemaSelector).Each(func(_ int, script *goquery.Selection) {
		var schema animeSchema
		if json.Unmarshal([]byte(script.Text()), &schema) != nil || (schema.Type != "TVSeries" && schema.Type != "Movie") {
			return
		}
		details.AltTitle = cleanText(schema.AltTitle)
		if schema.Description != "" {
			details.Description = cleanText(schema.Description)
		}
		for _, genre := range schema.Genres {
			if genre = cleanText(genre); genre != "" {
				details.Genres = append(details.Genres, genre)
			}
		}
		if len(schema.DatePublished) >= 4 {
			details.Year, _ = strconv.Atoi(schema.DatePublished[:4])
		}
		if schema.Episodes > 0 {
			details.EpisodeCount = schema.Episodes
		}
		if schema.Type == "TVSeries" {
			details.Type = "tv"
		} else {
			details.Type = "movie"
		}
	})
	fields := root.Find(animeFieldsSelector).ChildrenFiltered("div")
	fields.Each(func(i int, label *goquery.Selection) {
		if i%2 != 0 {
			return
		}
		value := fields.Eq(i + 1)
		switch cleanText(label.Text()) {
		case "Тип":
			if details.Type == "" {
				details.Type = cleanText(value.Text())
			}
		case "Эпизоды":
			if details.EpisodeCount == 0 {
				details.EpisodeCount, _ = strconv.Atoi(cleanText(value.Text()))
			}
		case "Статус":
			href, _ := value.Find("a").Attr("href")
			if strings.HasPrefix(href, "/anime/status/") {
				details.Status = strings.TrimPrefix(href, "/anime/status/")
			}
		case "Сезон":
			if details.Year == 0 {
				href, _ := value.Find("a").Attr("href")
				parts := strings.Split(strings.TrimPrefix(href, "/anime/season/"), "/")
				details.Year, _ = strconv.Atoi(parts[0])
			}
		case "Жанры":
			if len(details.Genres) == 0 {
				value.Find("a").Each(func(_ int, genre *goquery.Selection) {
					if text := cleanText(genre.Text()); text != "" {
						details.Genres = append(details.Genres, text)
					}
				})
			}
		}
	})
	loader := doc.Find(playerLoaderSelector)
	if loader.Length() == 0 {
		if doc.Find("#player").Length() > 0 {
			return nil, fmt.Errorf("%w: player section has no loader", provider.ErrParsing)
		}
		return page, nil // Valid catalog entry with no online player section.
	}
	path, _ := loader.Attr(playerLoaderAttribute)
	numericID := strings.TrimPrefix(path, "/player/")
	if loader.Length() != 1 || path != "/player/"+numericID || !domain.ValidEpisodeID(numericID) || !strings.HasSuffix(id, "-"+numericID) {
		return nil, fmt.Errorf("%w: invalid anime player endpoint", provider.ErrParsing)
	}
	page.playerPath, page.numericID = path, numericID
	return page, nil
}

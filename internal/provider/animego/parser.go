package animego

import (
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"

	"animego/backend/internal/domain"
	"animego/backend/internal/provider"
	"github.com/PuerkitoBio/goquery"
)

// Selectors are based on the full /search/anime HTML, not the navbar fragment.
const (
	searchRootSelector = ".search-entities"
	searchFormSelector = `form.search-form[action="/search/anime"] input[name="q"]`
	resultListSelector = ".ani-list"
	resultSelector     = ".ani-grid__item"
	titleSelector      = ".ani-grid__item-title a"
	altTitleSelector   = ".ani-grid__item-body > .fw-lighter"
	posterSelector     = ".ani-grid__item-picture img"
	yearSelector       = ".ani-grid__item-genres__link"
	emptySelector      = `.alert.alert-warning[role="alert"]`
)

func parseSearch(r io.Reader, baseURL *url.URL) ([]domain.Anime, error) {
	doc, err := goquery.NewDocumentFromReader(r)
	if err != nil {
		return nil, fmt.Errorf("%w: read HTML: %w", provider.ErrParsing, err)
	}
	root := doc.Find(searchRootSelector)
	if root.Length() != 1 || root.Find(searchFormSelector).Length() != 1 {
		return nil, fmt.Errorf("%w: missing AnimeGO anime search structure", provider.ErrParsing)
	}
	results := make([]domain.Anime, 0)
	cards := root.Find(resultListSelector).Find(resultSelector)
	if cards.Length() == 0 {
		// Remove the echoed query so it cannot masquerade as the empty-state text.
		alert := root.Find(emptySelector).Clone()
		alert.Find("strong").Remove()
		if strings.Contains(cleanText(alert.Text()), "ничего не найдено") {
			return results, nil
		}
		return nil, fmt.Errorf("%w: neither result cards nor explicit empty state found", provider.ErrParsing)
	}
	seen := make(map[string]struct{}, cards.Length())
	cards.Each(func(_ int, card *goquery.Selection) {
		anime, ok := parseCard(card, baseURL)
		if !ok {
			return
		}
		if _, duplicate := seen[anime.ID]; !duplicate {
			seen[anime.ID] = struct{}{}
			results = append(results, anime)
		}
	})
	if len(results) == 0 {
		return nil, fmt.Errorf("%w: all %d search cards are malformed", provider.ErrParsing, cards.Length())
	}
	return results, nil
}

func parseCard(card *goquery.Selection, baseURL *url.URL) (domain.Anime, bool) {
	link := card.Find(titleSelector).First()
	href, _ := link.Attr("href")
	u, ok := resolveHTTPURL(baseURL, href)
	if !ok || u.Host != baseURL.Host || u.Scheme != baseURL.Scheme || !strings.HasPrefix(u.Path, "/anime/") || u.RawQuery != "" || u.Fragment != "" {
		return domain.Anime{}, false
	}
	id := strings.TrimPrefix(u.Path, "/anime/")
	title := cleanText(link.Text())
	if !domain.ValidAnimeID(id) || title == "" {
		return domain.Anime{}, false
	}
	anime := domain.Anime{ID: id, Title: title, AltTitle: cleanText(card.Find(altTitleSelector).First().Text())}
	poster, _ := card.Find(posterSelector).First().Attr("src")
	if u, ok := resolveHTTPURL(baseURL, poster); ok {
		anime.Poster = u.String()
	}
	card.Find(yearSelector).EachWithBreak(func(_ int, field *goquery.Selection) bool {
		text := cleanText(field.Text())
		year, err := strconv.Atoi(text)
		if err == nil && len(text) == 4 && year >= 1900 && year <= 2100 {
			anime.Year = year
			return false
		}
		return true
	})
	return anime, true
}

func resolveHTTPURL(baseURL *url.URL, raw string) (*url.URL, bool) {
	if strings.TrimSpace(raw) == "" {
		return nil, false
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, false
	}
	u = baseURL.ResolveReference(u)
	return u, (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil
}

func cleanText(s string) string { return strings.Join(strings.Fields(s), " ") }

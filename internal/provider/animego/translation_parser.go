package animego

import (
	"fmt"
	"net/url"
	"strings"

	"animego/backend/internal/domain"
	"animego/backend/internal/provider"
	"github.com/PuerkitoBio/goquery"
)

const (
	translationSelector     = `[data-anime-player-target~="translation"]`
	playerReferenceSelector = `[data-anime-player-target~="provider"]`
	translationNameSelector = "span.text-truncate"
)

type videoResponse struct {
	Content   *string `json:"content"`
	NumVideos *int    `json:"numVideos"`
}

// playerReference stays inside this provider. Phase 3 can re-read the episode's
// menu, select the public ID, and use its iframe reference with a trusted resolver.
type playerReference struct {
	Reference     domain.PlayerReference
	URL           *url.URL
	EpisodeID     string
	TranslationID string
	ProviderID    string
}

type translationSet struct {
	translations []domain.Translation
	players      map[string]playerReference
}

func parseTranslations(data videoResponse, episodeID string, baseURL *url.URL) (*translationSet, error) {
	if data.NumVideos == nil || *data.NumVideos < 0 || *data.NumVideos > maxEpisodes {
		return nil, fmt.Errorf("%w: missing or invalid video count", provider.ErrParsing)
	}
	result := &translationSet{translations: make([]domain.Translation, 0), players: make(map[string]playerReference)}
	if *data.NumVideos == 0 {
		if data.Content != nil && strings.TrimSpace(*data.Content) != "" {
			return nil, fmt.Errorf("%w: zero videos with unexpected menu content", provider.ErrParsing)
		}
		return result, nil
	}
	if data.Content == nil {
		return nil, fmt.Errorf("%w: missing translation menu", provider.ErrParsing)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(*data.Content))
	if err != nil {
		return nil, fmt.Errorf("%w: read player menu: %w", provider.ErrParsing, err)
	}
	indices := make(map[string]int)
	var parseError error
	doc.Find(translationSelector).Each(func(_ int, button *goquery.Selection) {
		id, _ := button.Attr("data-translation-id")
		name := cleanText(button.Find(translationNameSelector).First().Text())
		if _, duplicate := indices[id]; duplicate || !domain.ValidEpisodeID(id) || name == "" {
			parseError = fmt.Errorf("%w: invalid translation identity", provider.ErrParsing)
			return
		}
		indices[id] = len(result.translations)
		result.translations = append(result.translations, domain.Translation{ID: id, Name: name, Players: make([]domain.PlayerReference, 0)})
	})
	doc.Find(playerReferenceSelector).Each(func(_ int, button *goquery.Selection) {
		translationID, _ := button.Attr("data-translation-id")
		parentTranslation, _ := button.Attr("data-ptranslation")
		providerID, _ := button.Attr("data-provider")
		providerSlug, _ := button.Attr("data-provider-slug")
		providerName, _ := button.Attr("data-provider-title")
		rawURL, _ := button.Attr("data-player")
		u, validURL := resolveHTTPURL(baseURL, rawURL)
		index, exists := indices[translationID]
		if !exists || translationID != parentTranslation || !domain.ValidEpisodeID(providerID) || !domain.ValidAnimeID(providerSlug) || !validURL {
			parseError = fmt.Errorf("%w: invalid player/translation relationship", provider.ErrParsing)
			return
		}
		id := episodeID + ":" + translationID + ":" + providerID
		if _, duplicate := result.players[id]; duplicate {
			parseError = fmt.Errorf("%w: duplicate player reference", provider.ErrParsing)
			return
		}
		ref := domain.PlayerReference{ID: id, Provider: providerSlug, Name: cleanText(providerName)}
		result.players[id] = playerReference{Reference: ref, URL: u, EpisodeID: episodeID, TranslationID: translationID, ProviderID: providerID}
		result.translations[index].Players = append(result.translations[index].Players, ref)
	})
	if parseError != nil {
		return nil, parseError
	}
	if len(result.translations) == 0 || len(result.players) != *data.NumVideos {
		return nil, fmt.Errorf("%w: menu does not match video count", provider.ErrParsing)
	}
	for _, translation := range result.translations {
		if len(translation.Players) == 0 {
			return nil, fmt.Errorf("%w: translation without player choices", provider.ErrParsing)
		}
	}
	return result, nil
}

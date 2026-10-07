package animego

import (
	"errors"
	"net/url"
	"strings"
	"testing"

	"animego/backend/internal/provider"
)

func ptr[T any](value T) *T { return &value }

func TestParseAnime(t *testing.T) {
	base, _ := url.Parse("https://animego.me/anime/podnyatie-urovnya-v-odinochku-s1-2477")
	page, err := parseAnime(strings.NewReader(fixture(t, "anime.html")), base)
	if err != nil {
		t.Fatal(err)
	}
	a := page.details
	if a.ID != "podnyatie-urovnya-v-odinochku-s1-2477" || a.Title != "Поднятие уровня в одиночку" || a.AltTitle != "Solo Leveling" || a.Year != 2024 || a.Status != "released" || a.Type != "tv" || len(a.Genres) != 3 || a.EpisodeCount != 12 || a.Description == "" || a.Poster == "" || page.numericID != "2477" || page.playerPath != "/player/2477" {
		t.Fatalf("unexpected details: %#v, player %s", a, page.playerPath)
	}
}

func TestParseAnimeOptionalMetadata(t *testing.T) {
	base, _ := url.Parse("https://animego.me/anime/example-1")
	html := `<link rel="canonical" href="/anime/example-1"><div class="entity"><div class="entity__title"><h1>Example</h1></div></div>`
	page, err := parseAnime(strings.NewReader(html), base)
	if err != nil || page.details.Title != "Example" || page.details.Description != "" || page.details.Year != 0 || page.playerPath != "" {
		t.Fatalf("optional fields: %#v, %v", page, err)
	}
	// Visible metadata remains usable when supplementary JSON-LD is absent.
	html = fixture(t, "anime.html")
	start := strings.Index(html, "<script")
	end := strings.Index(html, "</script>") + len("</script>")
	html = html[:start] + html[end:]
	base, _ = url.Parse("https://animego.me")
	page, err = parseAnime(strings.NewReader(html), base)
	if err != nil || page.details.Year != 2024 || len(page.details.Genres) != 1 || page.details.EpisodeCount != 12 {
		t.Fatalf("HTML fallback failed: %#v, %v", page, err)
	}
}

func TestParseAnimeMalformed(t *testing.T) {
	base, _ := url.Parse("https://animego.me")
	html := fixture(t, "anime.html")
	cases := []string{
		`<h1>Access denied</h1>`,
		strings.ReplaceAll(html, "entity__title", "changed-title"),
		strings.ReplaceAll(html, "https://animego.me/anime/", "https://evil.example/anime/"),
		strings.ReplaceAll(html, "/player/2477", "https://evil.example/player/2477"),
		strings.ReplaceAll(html, "/player/2477", "/player/999"),
		strings.ReplaceAll(html, "anime-player-loader", "changed-loader"),
	}
	for i, html := range cases {
		if _, err := parseAnime(strings.NewReader(html), base); !errors.Is(err, provider.ErrUpstreamStructureChanged) {
			t.Errorf("case %d: expected parsing error, got %v", i, err)
		}
	}
}

func TestParseEpisodes(t *testing.T) {
	response := episodePageResponse{Content: ptr(fixture(t, "episodes.html")), Page: ptr(0), PageSize: ptr(100), Total: ptr(2)}
	page, err := parseEpisodePage(response)
	if err != nil || len(page.episodes) != 2 || page.episodes[0].ID != "32410" || page.episodes[1].Number != 2 {
		t.Fatalf("unexpected episodes: %#v, %v", page, err)
	}
	response.Content, response.Total = ptr(" \n"), ptr(0)
	page, err = parseEpisodePage(response)
	if err != nil || page.episodes == nil || len(page.episodes) != 0 {
		t.Fatalf("empty episodes: %#v, %v", page, err)
	}
}

func TestParseEpisodesMalformed(t *testing.T) {
	html := fixture(t, "episodes.html")
	cases := []episodePageResponse{
		{},
		{Content: ptr(html), Page: ptr(0), PageSize: ptr(0), Total: ptr(2)},
		{Content: ptr(html), Page: ptr(0), PageSize: ptr(100), Total: ptr(maxEpisodes + 1)},
		{Content: ptr(`<h1>Challenge</h1>`), Page: ptr(0), PageSize: ptr(100), Total: ptr(0)},
		{Content: ptr(strings.ReplaceAll(html, "player-video-bar__item", "changed-card")), Page: ptr(0), PageSize: ptr(100), Total: ptr(2)},
		{Content: ptr(strings.ReplaceAll(html, "32411", "32410")), Page: ptr(0), PageSize: ptr(100), Total: ptr(2)},
		{Content: ptr(strings.ReplaceAll(html, `data-episode="32410"`, `data-episode="../secret"`)), Page: ptr(0), PageSize: ptr(100), Total: ptr(2)},
	}
	for i, response := range cases {
		if _, err := parseEpisodePage(response); !errors.Is(err, provider.ErrParsing) {
			t.Errorf("case %d: %v", i, err)
		}
	}
}

func TestParseTranslationsAndPrivatePlayerReferences(t *testing.T) {
	base, _ := url.Parse("https://animego.me")
	result, err := parseTranslations(videoResponse{Content: ptr(fixture(t, "translations.html")), NumVideos: ptr(3)}, "32410", base)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.translations) != 2 || len(result.translations[0].Players) != 2 || result.translations[0].Name != "AniLibria" || result.translations[1].Type != "" {
		t.Fatalf("unexpected translations: %#v", result.translations)
	}
	ref := result.players["32410:2:19"]
	if ref.URL.Host != "kodikplayer.com" || ref.ProviderID != "19" || ref.Reference.Provider != "kodik" || ref.EpisodeID != "32410" {
		t.Fatalf("unexpected private reference: %#v", ref)
	}
	empty, err := parseTranslations(videoResponse{NumVideos: ptr(0)}, "32410", base)
	if err != nil || empty.translations == nil || len(empty.translations) != 0 {
		t.Fatalf("empty variants: %#v, %v", empty, err)
	}
}

func TestParseTranslationsMalformed(t *testing.T) {
	base, _ := url.Parse("https://animego.me")
	html := fixture(t, "translations.html")
	cases := []videoResponse{
		{}, {NumVideos: ptr(1)},
		{Content: ptr(`<h1>Challenge</h1>`), NumVideos: ptr(3)},
		{Content: ptr(html), NumVideos: ptr(0)},
		{Content: ptr(html), NumVideos: ptr(4)},
		{Content: ptr(strings.ReplaceAll(html, `data-ptranslation="2"`, `data-ptranslation="999"`)), NumVideos: ptr(3)},
		{Content: ptr(strings.ReplaceAll(html, "//kodikplayer.com/seria/1/test-reference/720p?translations=false", "javascript:alert(1)")), NumVideos: ptr(3)},
	}
	for i, response := range cases {
		if _, err := parseTranslations(response, "32410", base); !errors.Is(err, provider.ErrParsing) {
			t.Errorf("case %d: %v", i, err)
		}
	}
}

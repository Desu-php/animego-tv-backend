package animego

import (
	"errors"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"

	"animego/backend/internal/domain"
	"animego/backend/internal/provider"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseSearchFixture(t *testing.T) {
	base, _ := url.Parse("https://animego.me/search/anime?q=solo")
	got, err := parseSearch(strings.NewReader(fixture(t, "search.html")), base)
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.Anime{{ID: "podnyatie-urovnya-v-odinochku-s1-2477", Title: "Поднятие уровня в одиночку", AltTitle: "Ore dake Level Up na Ken", Poster: "https://img.cdngos.com/v/250x350/anime/67/676487b86d5c0521559278", Year: 2024}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestParseSearchEmptyFixture(t *testing.T) {
	base, _ := url.Parse("https://animego.me")
	got, err := parseSearch(strings.NewReader(fixture(t, "empty.html")), base)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("expected non-nil empty results, got %#v, %v", got, err)
	}
}

func TestParseSearchOptionalAndMalformedCards(t *testing.T) {
	base, _ := url.Parse("https://animego.me")
	shell := `<div class="search-entities"><form class="search-form" action="/search/anime"><input name="q"></form><div class="ani-list">%s</div></div>`
	valid := `<div class="ani-grid__item"><div class="ani-grid__item-title"><a href="/anime/example-12">  Title &amp;   more </a></div><a class="ani-grid__item-picture"><img src="/poster.webp"></a></div>`
	malformed := `<div class="ani-grid__item"><div class="ani-grid__item-title"><a href="/character/12">Not anime</a></div></div>`
	html := strings.Replace(shell, "%s", malformed+valid+valid, 1)
	got, err := parseSearch(strings.NewReader(html), base)
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.Anime{{ID: "example-12", Title: "Title & more", Poster: "https://animego.me/poster.webp"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	withoutPoster := strings.Replace(html, `/poster.webp`, `javascript:alert(1)`, 1)
	got, err = parseSearch(strings.NewReader(withoutPoster), base)
	if err != nil || got[0].Poster != "" {
		t.Fatalf("unsafe optional poster should be omitted: %#v, %v", got, err)
	}
}

func TestParseSearchBrokenStructure(t *testing.T) {
	base, _ := url.Parse("https://animego.me")
	search := fixture(t, "search.html")
	empty := fixture(t, "empty.html")
	cases := map[string]string{
		"challenge":            `<html><h1>Please verify you are human</h1></html>`,
		"no results structure": `<div class="search-entities"><form class="search-form" action="/search/anime"><input name="q"></form></div>`,
		"changed selectors":    strings.ReplaceAll(search, "ani-grid__item", "new-card"),
		"all malformed":        strings.ReplaceAll(search, "/anime/", "/character/"),
		"external detail URL":  strings.ReplaceAll(search, "/anime/", "https://other.example/anime/"),
		"all-entity search":    strings.ReplaceAll(search, "/search/anime", "/search/all"),
		"echoed empty marker":  strings.ReplaceAll(strings.ReplaceAll(empty, "zxqvnonexistent739182", "ничего не найдено"), "» ничего не найдено.", "» ошибка."),
	}
	for name, html := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := parseSearch(strings.NewReader(html), base)
			if !errors.Is(err, provider.ErrParsing) || !errors.Is(err, provider.ErrUpstream) {
				t.Fatalf("expected upstream parsing error, got %v", err)
			}
		})
	}
}

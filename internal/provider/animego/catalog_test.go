package animego

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"animego/backend/internal/domain"
	"animego/backend/internal/provider"
)

const testAnimeID = "podnyatie-urovnya-v-odinochku-s1-2477"

func sendEnvelope(t *testing.T, w http.ResponseWriter, data any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": data}); err != nil {
		t.Error(err)
	}
}

func TestProviderCatalogFlow(t *testing.T) {
	animeHTML, episodesHTML, menuHTML := fixture(t, "anime.html"), fixture(t, "episodes.html"), fixture(t, "translations.html")
	var sequence []string
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sequence = append(sequence, r.URL.RequestURI())
		if r.Method != "GET" || r.Header.Get("User-Agent") != userAgent {
			t.Error("unexpected method or User-Agent")
		}
		if strings.HasPrefix(r.URL.Path, "/player/") {
			if r.Header.Get("Accept") != "application/json" || r.Header.Get("X-Requested-With") != "XMLHttpRequest" || r.Referer() != server.URL+"/anime/"+testAnimeID {
				t.Error("missing player request headers")
			}
		}
		switch r.URL.Path {
		case "/search/anime":
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, fixture(t, "search.html"))
		case "/anime/" + testAnimeID:
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, strings.ReplaceAll(animeHTML, "https://animego.me", server.URL))
		case "/player/2477/episodes":
			if r.URL.Query().Get("page") != "0" {
				t.Error("expected page=0")
			}
			sendEnvelope(t, w, episodePageResponse{Content: &episodesHTML, Page: ptr(0), PageSize: ptr(100), Total: ptr(2)})
		case "/player/videos/32410":
			sendEnvelope(t, w, videoResponse{Content: &menuHTML, NumVideos: ptr(3)})
		default:
			t.Errorf("unexpected upstream request: %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL, &http.Client{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	search, err := client.Search(ctx, "solo")
	if err != nil || len(search) != 1 {
		t.Fatalf("search: %#v, %v", search, err)
	}
	anime, err := client.GetAnime(ctx, search[0].ID)
	if err != nil || anime.ID != testAnimeID || anime.Year != 2024 {
		t.Fatalf("details: %#v, %v", anime, err)
	}
	episodes, err := client.GetEpisodes(ctx, anime.ID)
	if err != nil || len(episodes) != 2 || episodes[0].ID != "32410" {
		t.Fatalf("episodes: %#v, %v", episodes, err)
	}
	translations, err := client.GetTranslations(ctx, anime.ID, episodes[0].ID)
	if err != nil || len(translations) != 2 || translations[0].Players[1].ID != "32410:2:19" {
		t.Fatalf("translations: %#v, %v", translations, err)
	}
	publicJSON, _ := json.Marshal(translations)
	if strings.Contains(string(publicJSON), "test-reference") || strings.Contains(string(publicJSON), "http") || strings.Contains(string(publicJSON), "data-player") {
		t.Fatalf("private player reference leaked: %s", publicJSON)
	}
	want := []string{"/search/anime?q=solo", "/anime/" + testAnimeID, "/anime/" + testAnimeID, "/player/2477/episodes?page=0", "/anime/" + testAnimeID, "/player/2477/episodes?page=0", "/player/videos/32410"}
	if !reflect.DeepEqual(sequence, want) {
		t.Fatalf("request sequence: %#v", sequence)
	}
}

func TestProviderPaginationAndOwnership(t *testing.T) {
	for _, scenario := range []string{"valid", "duplicate", "wrong page", "changing total", "foreign episode"} {
		t.Run(scenario, func(t *testing.T) {
			var videoCalls int
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/anime/") {
					w.Header().Set("Content-Type", "text/html")
					_, _ = io.WriteString(w, strings.ReplaceAll(fixture(t, "anime.html"), "https://animego.me", server.URL))
					return
				}
				if strings.HasPrefix(r.URL.Path, "/player/videos/") {
					videoCalls++
					http.NotFound(w, r)
					return
				}
				page, id, number := 0, "32410", "1"
				if r.URL.Query().Get("page") == "1" {
					page, id, number = 1, "32411", "2"
				}
				total := 2
				if page == 1 {
					switch scenario {
					case "duplicate":
						id = "32410"
					case "wrong page":
						page = 0
					case "changing total":
						total = 3
					}
				}
				html := `<div class="player-video-bar__item" data-anime-player-episodes-target="episode" data-episode="` + id + `" data-episode-number="` + number + `"></div>`
				sendEnvelope(t, w, episodePageResponse{Content: &html, Page: &page, PageSize: ptr(1), Total: &total})
			}))
			defer server.Close()
			client, _ := NewClient(server.URL, &http.Client{Timeout: time.Second})
			if scenario == "foreign episode" {
				_, err := client.GetTranslations(context.Background(), testAnimeID, "999")
				if !errors.Is(err, domain.ErrEpisodeNotFound) || videoCalls != 0 {
					t.Fatalf("foreign episode queried: %v, calls %d", err, videoCalls)
				}
				return
			}
			got, err := client.GetEpisodes(context.Background(), testAnimeID)
			if scenario == "valid" {
				if err != nil || len(got) != 2 || got[1].ID != "32411" {
					t.Fatalf("pagination: %#v, %v", got, err)
				}
			} else if !errors.Is(err, provider.ErrParsing) {
				t.Fatalf("expected pagination error, got %v", err)
			}
		})
	}
}

func TestProviderNotFoundEmptyAndMalformed(t *testing.T) {
	for _, scenario := range []string{"anime not found", "empty episodes", "no player", "empty translations", "episode not found", "upstream error", "bad envelope"} {
		t.Run(scenario, func(t *testing.T) {
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/anime/") {
					if scenario == "anime not found" {
						http.NotFound(w, r)
						return
					}
					w.Header().Set("Content-Type", "text/html")
					html := strings.ReplaceAll(fixture(t, "anime.html"), "https://animego.me", server.URL)
					if scenario == "no player" {
						html = html[:strings.Index(html, `<div id="player">`)]
					}
					_, _ = io.WriteString(w, html)
					return
				}
				if strings.HasSuffix(r.URL.Path, "/episodes") {
					if scenario == "upstream error" {
						w.WriteHeader(503)
						return
					}
					if scenario == "bad envelope" {
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, `{"status":"success","data":null}`)
						return
					}
					html, total := fixture(t, "episodes.html"), 2
					if scenario == "empty episodes" {
						html, total = "", 0
					}
					sendEnvelope(t, w, episodePageResponse{Content: &html, Page: ptr(0), PageSize: ptr(100), Total: &total})
					return
				}
				if scenario == "episode not found" {
					http.NotFound(w, r)
					return
				}
				sendEnvelope(t, w, videoResponse{NumVideos: ptr(0)})
			}))
			defer server.Close()
			client, _ := NewClient(server.URL, &http.Client{Timeout: time.Second})
			switch scenario {
			case "anime not found":
				_, err := client.GetAnime(context.Background(), testAnimeID)
				if !errors.Is(err, domain.ErrAnimeNotFound) {
					t.Fatal(err)
				}
			case "episode not found", "empty translations":
				got, err := client.GetTranslations(context.Background(), testAnimeID, "32410")
				if scenario == "episode not found" {
					if !errors.Is(err, domain.ErrEpisodeNotFound) {
						t.Fatal(err)
					}
				} else if err != nil || got == nil || len(got) != 0 {
					t.Fatalf("empty translations: %#v, %v", got, err)
				}
			default:
				got, err := client.GetEpisodes(context.Background(), testAnimeID)
				if scenario == "upstream error" || scenario == "bad envelope" {
					if !errors.Is(err, provider.ErrUpstream) {
						t.Fatal(err)
					}
				} else if err != nil || got == nil || len(got) != 0 {
					t.Fatalf("empty episodes: %#v, %v", got, err)
				}
			}
		})
	}
}

func TestProviderRejectsUnsafeIdentifiersAndRedirects(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; http.NotFound(w, r) }))
	defer server.Close()
	client, _ := NewClient(server.URL, &http.Client{Timeout: time.Second})
	for _, id := range []string{"", "../secret", "https://evil.example", "foo/bar", "foo%2fbar", "foo?x=1", "foo#x", "foo\\bar", strings.Repeat("a", 301)} {
		if _, err := client.GetAnime(context.Background(), id); !errors.Is(err, domain.ErrInvalidIdentifier) {
			t.Errorf("accepted unsafe ID %q: %v", id, err)
		}
		if _, err := client.GetEpisodes(context.Background(), id); !errors.Is(err, domain.ErrInvalidIdentifier) {
			t.Errorf("accepted unsafe episode request %q: %v", id, err)
		}
	}
	for _, id := range []string{"", "../1", "0", "-1", "1?x=2", "1%2f2"} {
		if _, err := client.GetTranslations(context.Background(), testAnimeID, id); !errors.Is(err, domain.ErrInvalidIdentifier) {
			t.Errorf("accepted unsafe episode %q: %v", id, err)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid identifiers made %d requests", calls)
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer redirect.Close()
	injected := &http.Client{Timeout: time.Second}
	client, _ = NewClient(redirect.URL, injected)
	_, err := client.GetAnime(context.Background(), testAnimeID)
	if !errors.Is(err, provider.ErrHTTP) || calls != 0 || injected.CheckRedirect != nil {
		t.Fatalf("unsafe redirect or injected client mutation: %v, calls=%d", err, calls)
	}
}

func TestCatalogOperationDeadline(t *testing.T) {
	animeHTML := fixture(t, "anime.html")
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Each request fits the individual client timeout; together they exceed it.
		timer := time.NewTimer(150 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
			return
		case <-timer.C:
		}
		if strings.HasPrefix(r.URL.Path, "/anime/") {
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, strings.ReplaceAll(animeHTML, "https://animego.me", server.URL))
			return
		}
		sendEnvelope(t, w, episodePageResponse{Content: ptr(""), Page: ptr(0), PageSize: ptr(100), Total: ptr(0)})
	}))
	defer server.Close()
	client, _ := NewClient(server.URL, &http.Client{Timeout: 250 * time.Millisecond})
	_, err := client.GetEpisodes(context.Background(), testAnimeID)
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, provider.ErrHTTP) {
		t.Fatalf("whole operation deadline not propagated: %v", err)
	}
}

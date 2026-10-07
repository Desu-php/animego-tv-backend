package httptransport_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"animego/backend/internal/domain"
	"animego/backend/internal/player"
	"animego/backend/internal/player/aniboom"
	"animego/backend/internal/player/cvh"
	"animego/backend/internal/player/kodik"
	"animego/backend/internal/provider/animego"
	"animego/backend/internal/service"
	httptransport "animego/backend/internal/transport/http"
)

type fixtureFetcher struct{ server *httptest.Server }

func (f fixtureFetcher) PostForm(ctx context.Context, raw string, values url.Values, limit int64) ([]byte, error) {
	u, err := player.ValidateURL(raw, []string{kodik.EmbedHost})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.server.URL+u.RequestURI(), strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := f.server.Client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, limit+1))
}

func (f fixtureFetcher) Probe(ctx context.Context, raw string) error {
	u, err := player.ValidateMediaURL(raw)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, f.server.URL+u.RequestURI(), nil)
	if err != nil {
		return err
	}
	resp, err := f.server.Client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "video/mp4" {
		return fmt.Errorf("fixture MP4 unavailable")
	}
	return nil
}

func (f fixtureFetcher) Fetch(ctx context.Context, raw string, limit int64) ([]byte, error) {
	u, err := player.ValidateMediaURL(raw)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.server.URL+u.RequestURI(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := f.server.Client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("fixture status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("fixture exceeds limit")
	}
	return b, err
}

func TestSearchToPlaybackOffline(t *testing.T) {
	for _, host := range []string{"ella.ya-ligh123.site", "kira.ya-low1.site", "graham.ya-low1.site", "new-node.ya-low1.site", "media.future-cdn.example"} {
		t.Run(host, func(t *testing.T) { testSearchToPlaybackOffline(t, host) })
	}
}

func testSearchToPlaybackOffline(t *testing.T, mediaHost string) {
	read := func(path string) string {
		t.Helper()
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	catalogFixture := func(name string) string { return read("../../provider/animego/testdata/" + name) }
	embed := strings.ReplaceAll(read("../../player/aniboom/testdata/embed.html"), "master.m3u8", "master.m3u8?token=fixture-sensitive")
	embed = strings.ReplaceAll(embed, "ella.ya-ligh123.site", mediaHost)
	master := read("../../player/aniboom/testdata/master.m3u8")
	var mu sync.Mutex
	var playerRequests []string
	media := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		playerRequests = append(playerRequests, r.URL.Path)
		mu.Unlock()
		switch r.URL.Path {
		case "/embed/test-reference":
			io.WriteString(w, embed)
		case "/sample/audio/index.m3u8", "/sample/360/index.m3u8", "/sample/1080/index.m3u8":
			io.WriteString(w, "#EXTM3U\n#EXTINF:6,\nsegment.ts\n#EXT-X-ENDLIST\n")
		case "/sample/master.m3u8":
			io.WriteString(w, master)
		case "/seria/1/0123456789abcdef0123456789abcdef/720p":
			io.WriteString(w, `<script>var urlParams = '{"d":"animego.me","d_sign":"fixture-d-sign","pd":"kodikplayer.com","pd_sign":"fixture-pd-sign","ref":"https%3A%2F%2Fanimego.me%2F","ref_sign":"fixture-ref-sign"}';</script><script src="/assets/js/app.player_single.abc123.js"></script>`)
		case "/assets/js/app.player_single.abc123.js":
			io.WriteString(w, `$.ajax({url:atob("L2Z0b3I="),type:"POST"})`)
		case "/ftor":
			if r.Method != http.MethodPost || r.FormValue("id") != "1" || r.FormValue("d_sign") != "fixture-d-sign" {
				t.Error("bad Kodik source request")
			}
			json.NewEncoder(w).Encode(map[string]any{"links": map[string]any{"720": []any{map[string]string{"src": "https://" + mediaHost + "/sample/master.m3u8", "type": "application/x-mpegURL"}}}})
		case "/api/v1/player/sv/playlist":
			if r.URL.Query().Get("id") != "1" || r.URL.Query().Get("ep") != "1" {
				t.Error("bad CVH playlist request")
			}
			io.WriteString(w, `{"isSerial":true,"items":[{"vkId":"1001","season":1,"episode":1,"voiceStudio":"Test"}]}`)
		case "/api/v1/player/sv/video/1001":
			json.NewEncoder(w).Encode(map[string]any{"sources": map[string]string{"hlsUrl": "https://" + mediaHost + "/sample/master.m3u8"}})
		default:
			t.Errorf("unexpected media/segment request %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer media.Close()
	search, anime, episodes, translations := catalogFixture("search.html"), catalogFixture("anime.html"), catalogFixture("episodes.html"), catalogFixture("translations.html")
	translations = strings.ReplaceAll(translations, "/seria/1/test-reference/720p", "/seria/1/0123456789abcdef0123456789abcdef/720p")
	var upstream *httptest.Server
	upstream = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search/anime":
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, strings.ReplaceAll(search, "https://animego.me", upstream.URL))
		case "/anime/podnyatie-urovnya-v-odinochku-s1-2477":
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, strings.ReplaceAll(anime, "https://animego.me", upstream.URL))
		case "/player/2477/episodes":
			if r.Header.Get("X-Requested-With") != "XMLHttpRequest" || r.URL.Query().Get("page") != "0" {
				t.Error("missing episode request contract")
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"content": episodes, "page": 0, "pageSize": 100, "total": 2}})
		case "/player/videos/32410":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"content": strings.ReplaceAll(translations, "//animego.me/cdn-iframe/", upstream.URL+"/cdn-iframe/"), "numVideos": 3}})
		case "/cdn-iframe/1/Test/1/1":
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, `<video-player id="pcvh" data-title-id="1" data-publisher-id="1" episode="1" priority-voice="Test"></video-player>`)
		default:
			t.Errorf("unexpected AnimeGO request %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	upstreamHTTP := upstream.Client()
	upstreamHTTP.Timeout = time.Second
	client, err := animego.NewClient(upstream.URL, upstreamHTTP, animego.WithLogger(logger))
	if err != nil {
		t.Fatal(err)
	}
	cvhResolver, err := cvh.NewResolver(upstream.URL, client, fixtureFetcher{media}, fixtureFetcher{media})
	if err != nil {
		t.Fatal(err)
	}
	registry := player.NewRegistry(map[string]player.Resolver{
		"aniboom": aniboom.NewResolver(fixtureFetcher{media}, fixtureFetcher{media}),
		"kodik":   kodik.NewResolver(fixtureFetcher{media}, fixtureFetcher{media}),
		"cvh":     cvhResolver,
	})
	playback := service.NewPlaybackService(client, registry, time.Second, time.Second, logger)
	router := httptransport.NewRouter(service.NewAnimeService(client), logger, httptransport.WithPlayback(playback))
	get := func(path string, status int) []byte {
		t.Helper()
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != status {
			t.Fatalf("%s: status %d body %s", path, w.Code, w.Body.String())
		}
		if strings.HasSuffix(path, "/playback") && w.Header().Get("Cache-Control") != "no-store" {
			t.Error("playback cache policy missing")
		}
		return w.Body.Bytes()
	}
	var results struct{ Data []domain.Anime }
	json.Unmarshal(get("/api/v1/anime/search?q=solo", 200), &results)
	if len(results.Data) == 0 {
		t.Fatal("search empty")
	}
	id := results.Data[0].ID
	root := "/api/v1/anime/" + id
	var details struct{ Data domain.AnimeDetails }
	json.Unmarshal(get(root, 200), &details)
	if details.Data.ID != id {
		t.Fatal("detail ID mismatch")
	}
	var episodeList struct{ Data []domain.Episode }
	json.Unmarshal(get(root+"/episodes", 200), &episodeList)
	if len(episodeList.Data) != 2 {
		t.Fatal("episodes mismatch")
	}
	episodeID := episodeList.Data[0].ID
	var variants struct{ Data []domain.Translation }
	json.Unmarshal(get(root+"/episodes/"+episodeID+"/translations", 200), &variants)
	selected := ""
	for _, translation := range variants.Data {
		for _, ref := range translation.Players {
			if ref.Provider == "aniboom" {
				selected = ref.ID
			}
		}
	}
	if selected == "" {
		t.Fatal("AniBoom not discovered")
	}
	base := root + "/episodes/" + episodeID + "/players/"
	b := get(base+selected+"/playback", 200)
	var response struct{ Data domain.Playback }
	if err := json.Unmarshal(b, &response); err != nil {
		t.Fatal(err)
	}
	got := response.Data
	if got.AnimeID != id || got.EpisodeID != episodeID || got.TranslationID != "76" || got.PlayerID != selected || len(got.Sources) != 1 {
		t.Fatalf("bad normalized playback: %#v", got)
	}
	source := got.Sources[0]
	if source.Type != domain.VideoHLS || source.URL != "https://"+mediaHost+"/sample/master.m3u8?token=fixture-sensitive" || source.Quality != "auto" || len(source.Headers) != 0 || source.ExpiresAt != nil {
		t.Fatalf("bad source: %#v", source)
	}
	if bytes.Contains(b, []byte("embed/")) || bytes.Contains(b, []byte("data-parameters")) {
		t.Fatal("implementation details leaked")
	}
	for _, check := range []struct{ id, quality string }{{"32410:2:19", "720"}, {"32410:2:26", "auto"}} {
		var playback struct{ Data domain.Playback }
		if err := json.Unmarshal(get(base+check.id+"/playback", 200), &playback); err != nil {
			t.Fatal(err)
		}
		if playback.Data.TranslationID != "2" || playback.Data.PlayerID != check.id || len(playback.Data.Sources) != 1 || playback.Data.Sources[0].Quality != check.quality {
			t.Fatalf("bad normalized provider playback: %#v", playback)
		}
	}
	get(base+"32410:99:24/playback", 404)
	get(base+"32411:76:24/playback", 400)
	get(root+"/episodes/999/players/999:76:24/playback", 404)
	mu.Lock()
	defer mu.Unlock()
	if len(playerRequests) != 18 || playerRequests[0] != "/embed/test-reference" || playerRequests[1] != "/sample/master.m3u8" {
		t.Fatalf("unexpected player requests: %#v", playerRequests)
	}
	if strings.Contains(logs.String(), "fixture-sensitive") || strings.Contains(logs.String(), "https://"+mediaHost) || strings.Contains(logs.String(), "data-parameters") {
		t.Fatal("sensitive media metadata leaked in logs")
	}
	for _, field := range []string{`"stage":"player"`, `"provider":"aniboom"`, `"translation_id":"76"`, `"media_type":"hls"`, `"duration"`, `"status_code"`} {
		if !strings.Contains(logs.String(), field) {
			t.Errorf("missing observability field %s", field)
		}
	}
}

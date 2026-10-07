package aniboom

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"animego/backend/internal/domain"
	"animego/backend/internal/player"
	"animego/backend/internal/provider"
)

const fixtureMediaHost = "ella.ya-ligh123.site"

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Test-only routing keeps realistic allowed URLs in fixtures while all I/O is local.
type localFetcher struct{ server *httptest.Server }

func (f localFetcher) Fetch(ctx context.Context, raw string, limit int64) ([]byte, error) {
	u, err := player.ValidateMediaURL(raw)
	if err != nil {
		return nil, err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, f.server.URL+u.RequestURI(), nil)
	resp, err := f.server.Client().Do(req)
	if err != nil {
		return nil, &provider.HTTPError{Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, &provider.HTTPError{StatusCode: resp.StatusCode, Err: errors.New("fixture status")}
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if int64(len(b)) > limit {
		return nil, provider.ErrHTTP
	}
	return b, err
}

func configuration(hls, dash string) []byte {
	b, _ := json.Marshal(map[string]string{"hls": hls, "dash": dash})
	return []byte(`<video id="video" data-parameters="` + html.EscapeString(string(b)) + `"></video>`)
}
func sourceJSON(raw, mime string) string {
	b, _ := json.Marshal(map[string]string{"src": raw, "type": mime})
	return string(b)
}
func selection() domain.PlayerSelection {
	return domain.PlayerSelection{Player: domain.PlayerReference{Provider: "aniboom"}, EmbedURL: "https://aniboom.one/embed/test-reference?episode=1"}
}

func TestResolve(t *testing.T) {
	master := fixture(t, "master.m3u8")
	mpd := fixture(t, "manifest.mpd")
	embed := fixture(t, "embed.html")
	hls := sourceJSON("https://"+fixtureMediaHost+"/sample/master.m3u8", "application/x-mpegURL")
	dash := sourceJSON("https://"+fixtureMediaHost+"/sample/manifest.mpd", "application/dash+xml")
	tests := []struct {
		name            string
		embed, manifest []byte
		status          int
		kind            domain.VideoType
		want            error
	}{
		{name: "adaptive HLS preferred", embed: embed, manifest: master, kind: domain.VideoHLS},
		{name: "DASH when HLS absent", embed: configuration("", dash), manifest: mpd, kind: domain.VideoDASH},
		{name: "episode 32419 Kira HLS", embed: configuration(sourceJSON("https://kira.ya-low1.site/sample/master.m3u8", "application/x-mpegURL"), ""), manifest: master, kind: domain.VideoHLS},
		{name: "episode 32420 Graham HLS", embed: configuration(sourceJSON("https://graham.ya-low1.site/sample/master.m3u8", "application/x-mpegURL"), ""), manifest: master, kind: domain.VideoHLS},
		{name: "Kira DASH", embed: configuration("", sourceJSON("https://kira.ya-low1.site/sample/manifest.mpd", "application/dash+xml")), manifest: mpd, kind: domain.VideoDASH},
		{name: "Graham DASH", embed: configuration("", sourceJSON("https://graham.ya-low1.site/sample/manifest.mpd", "application/dash+xml")), manifest: mpd, kind: domain.VideoDASH},
		{name: "new CDN subdomain", embed: configuration(sourceJSON("https://new-node.ya-low1.site/sample/master.m3u8", "application/x-mpegURL"), ""), manifest: master, kind: domain.VideoHLS},
		{name: "entirely new CDN domain HLS", embed: configuration(sourceJSON("https://media.future-cdn.example/sample/master.m3u8?token=opaque", "application/x-mpegURL"), ""), manifest: master, kind: domain.VideoHLS},
		{name: "entirely new CDN domain DASH", embed: configuration("", sourceJSON("https://dash.future-cdn.example/sample/manifest.mpd", "application/dash+xml")), manifest: mpd, kind: domain.VideoDASH},
		{name: "HLS references another verified CDN", embed: embed, manifest: []byte("#EXTM3U\n#EXT-X-MEDIA:TYPE=AUDIO,URI=\"https://kira.ya-low1.site/audio.m3u8\"\n#EXT-X-STREAM-INF:BANDWIDTH=1\nhttps://graham.ya-low1.site/video.m3u8\n"), kind: domain.VideoHLS},
		{name: "DASH references another verified CDN", embed: configuration("", dash), manifest: []byte(`<MPD><BaseURL>https://kira.ya-low1.site/sample/</BaseURL><Representation><SegmentTemplate media="https://graham.ya-low1.site/sample/$Number$.m4s"/></Representation></MPD>`), kind: domain.VideoDASH},
		{name: "missing configuration", embed: []byte("<html></html>"), want: provider.ErrParsing},
		{name: "malformed configuration", embed: []byte(`<video id="video" data-parameters="broken"></video>`), want: provider.ErrParsing},
		{name: "malformed nested JSON", embed: configuration("broken", ""), want: provider.ErrParsing},
		{name: "missing source", embed: configuration("", ""), want: domain.ErrPlaybackUnavailable},
		{name: "unsupported format", embed: configuration(sourceJSON("https://"+fixtureMediaHost+"/movie.mp4", "video/mp4"), ""), want: domain.ErrPlaybackUnavailable},
		{name: "local CDN", embed: configuration(sourceJSON("https://localhost/master.m3u8?secret=redact", "application/x-mpegURL"), ""), want: provider.ErrParsing},
		{name: "literal metadata CDN", embed: configuration(sourceJSON("https://169.254.169.254/master.m3u8?secret=redact", "application/x-mpegURL"), ""), want: provider.ErrParsing},
		{name: "source credentials", embed: configuration(sourceJSON("https://user:secret@cdn.example/master.m3u8?secret=redact", "application/x-mpegURL"), ""), want: provider.ErrParsing},
		{name: "unexpected status", embed: embed, status: 503, want: provider.ErrUpstream},
		{name: "access denied", embed: embed, status: 403, want: domain.ErrPlaybackUnavailable},
		{name: "missing source", embed: embed, status: 404, want: domain.ErrPlaybackUnavailable},
		{name: "restricted source", embed: embed, status: 451, want: domain.ErrPlaybackUnavailable},
		{name: "manifest malformed", embed: configuration(hls, ""), manifest: []byte("<html>challenge</html>"), want: provider.ErrParsing},
		{name: "encrypted HLS", embed: embed, manifest: []byte("#EXTM3U\n#EXT-X-SESSION-KEY:METHOD=SAMPLE-AES,URI=\"key\"\n"), want: domain.ErrPlaybackUnavailable},
		{name: "foreign HLS rendition", embed: embed, manifest: []byte("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nhttps://127.0.0.1/index.m3u8\n"), want: provider.ErrParsing},
		{name: "local HLS audio", embed: embed, manifest: []byte("#EXTM3U\n#EXT-X-MEDIA:TYPE=AUDIO,URI=\"https://audio.local/audio\"\n#EXT-X-STREAM-INF:BANDWIDTH=1\nvideo.m3u8\n"), want: provider.ErrParsing},
		{name: "new HLS rendition CDN", embed: embed, manifest: []byte("#EXTM3U\n#EXT-X-MEDIA:TYPE=AUDIO,URI=\"https://audio.future-cdn.example/audio.m3u8\"\n#EXT-X-STREAM-INF:BANDWIDTH=1\nhttps://video.future-cdn.example/video.m3u8\n"), kind: domain.VideoHLS},
		{name: "DRM DASH", embed: configuration("", dash), manifest: []byte(`<MPD><ContentProtection/><Representation/></MPD>`), want: domain.ErrPlaybackUnavailable},
		{name: "malformed DASH", embed: configuration("", dash), manifest: []byte(`<MPD><Representation></MPD>`), want: provider.ErrParsing},
		{name: "foreign DASH template", embed: configuration("", dash), manifest: []byte(`<MPD><Representation><SegmentTemplate media="http://169.254.169.254/$Number$"/></Representation></MPD>`), want: provider.ErrParsing},
		{name: "multiple DASH roots", embed: configuration("", dash), manifest: []byte(`<MPD><Representation/></MPD><MPD/>`), want: provider.ErrParsing},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if tt.status != 0 {
					w.WriteHeader(tt.status)
					return
				}
				switch r.URL.Path {
				case "/embed/test-reference":
					w.Write(tt.embed)
				case "/sample/master.m3u8", "/sample/manifest.mpd":
					w.Write(tt.manifest)
				default:
					if strings.HasSuffix(r.URL.Path, ".m3u8") {
						_, _ = io.WriteString(w, "#EXTM3U\n#EXTINF:6,\nsegment.ts\n#EXT-X-ENDLIST\n")
						return
					}
					t.Errorf("unexpected request (segments must not be fetched): %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			got, err := NewResolver(localFetcher{server}, localFetcher{server}).Resolve(context.Background(), selection())
			if tt.want != nil {
				if !errors.Is(err, tt.want) {
					t.Fatalf("got %v, want %v", err, tt.want)
				}
				if strings.Contains(err.Error(), "secret=") {
					t.Fatal("URL leaked in error")
				}
				return
			}
			if err != nil || len(got) != 1 {
				t.Fatalf("got %#v, %v", got, err)
			}
			u, e := player.ValidateMediaURL(got[0].URL)
			if e != nil || u == nil || got[0].Type != tt.kind || got[0].Quality != "auto" || len(got[0].Headers) != 0 || got[0].ExpiresAt != nil || tt.kind == domain.VideoDASH && requests != 2 || tt.kind == domain.VideoHLS && requests < 3 {
				t.Fatalf("unexpected source/requests: %#v, %d", got, requests)
			}
		})
	}
}

func TestRejectEmbedBeforeFetching(t *testing.T) {
	for _, raw := range []string{"http://aniboom.one/embed/id", "https://aniboom.one.evil.example/embed/id", "https://localhost/embed/id", "https://aniboom.one/other", "https://aniboom.one/embed/../id", "https://aniboom.one/embed/", "https://user:password@aniboom.one/embed/id"} {
		s := selection()
		s.EmbedURL = raw
		_, err := NewResolver(nil, nil).Resolve(context.Background(), s)
		if !errors.Is(err, domain.ErrUnsupportedPlayer) {
			t.Errorf("accepted %q: %v", raw, err)
		}
	}
}

func TestResolveCancellation(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewResolver(localFetcher{server}, localFetcher{server}).Resolve(ctx, selection())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}
